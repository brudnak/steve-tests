// Package client provides a schema-discovering HTTP client for the Steve /v1 API.
// It is used by QA integration scenarios to create, list, and wait for Kubernetes
// resources as exposed by Steve, without hardcoding paths for each resource type.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/rancher/steve-tests/pkg/config"
)

type Client struct {
	BaseURL      string
	Token        string // optional Bearer token for authenticated endpoints
	HTTPClient   *http.Client
	Schemas      map[string]SchemaInfo
	PollInterval time.Duration
	WaitTimeout  time.Duration
	Verbose      bool // if true, log basic request/response info to aid debugging
}

type SchemaInfo struct {
	ID                string
	Namespaced        bool
	CollectionPath    string
	APIVersion        string
	Kind              string
	CollectionMethods []string
	ResourceMethods   []string
	Links             map[string]string
}

type SteveCollection struct {
	Type         string            `json:"type"`
	ResourceType string            `json:"resourceType"`
	Revision     string            `json:"revision"`
	Continue     string            `json:"continue"`
	Count        int               `json:"count"`
	Pages        int               `json:"pages"`
	Data         []map[string]any  `json:"data"`
	Links        map[string]string `json:"links"`
	CreateTypes  map[string]string `json:"createTypes"`
	Summary      []SummaryItem     `json:"summary"`
	dataPresent  bool
	dataNull     bool
}

type SummaryItem struct {
	Property string                  `json:"property"`
	Counts   map[string]SummaryCount `json:"counts"`
}

type SummaryCount struct {
	Total     int            `json:"total"`
	Namespace map[string]int `json:"namespace,omitempty"`
}

type StatusError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	if e == nil {
		return "Steve request failed with an unknown status"
	}
	return fmt.Sprintf("%s %s failed with status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

func IsStatus(err error, codes ...int) bool {
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr == nil {
		return false
	}
	for _, code := range codes {
		if statusErr.StatusCode == code {
			return true
		}
	}
	return false
}

type schemaCollection struct {
	Data []schemaResource `json:"data"`
}

type schemaResource struct {
	ID                string            `json:"id"`
	Links             map[string]string `json:"links"`
	CollectionMethods []string          `json:"collectionMethods"`
	ResourceMethods   []string          `json:"resourceMethods"`
	Attributes        struct {
		Group      string   `json:"group"`
		Version    string   `json:"version"`
		Kind       string   `json:"kind"`
		Resource   string   `json:"resource"`
		Namespaced bool     `json:"namespaced"`
		Verbs      []string `json:"verbs"`
	} `json:"attributes"`
}

func Discover(cfg *config.Config) (*Client, error) {
	return DiscoverContext(context.Background(), cfg)
}

func DiscoverContext(ctx context.Context, cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("Steve config is nil")
	}
	validated := *cfg
	if err := validated.Validate(); err != nil {
		return nil, fmt.Errorf("validate Steve config: %w", err)
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("http.DefaultTransport is not an *http.Transport")
	}
	transport = transport.Clone()
	tlsConfig := &tls.Config{
		InsecureSkipVerify: validated.Insecure, //nolint:gosec // explicit test-harness setting
		MinVersion:         tls.VersionTLS12,
	}
	if validated.CAFile != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system CA pool: %w", err)
		}
		pemBytes, err := os.ReadFile(validated.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read Steve CA file %s: %w", validated.CAFile, err)
		}
		if ok := pool.AppendCertsFromPEM(pemBytes); !ok {
			return nil, fmt.Errorf("Steve CA file %s does not contain a PEM certificate", validated.CAFile)
		}
		tlsConfig.RootCAs = pool
	}
	transport.TLSClientConfig = tlsConfig

	httpClient := &http.Client{
		Timeout:   validated.RequestTimeoutDuration(),
		Transport: transport,
	}

	c := &Client{
		BaseURL:      validated.URL,
		Token:        validated.Token,
		HTTPClient:   httpClient,
		Schemas:      make(map[string]SchemaInfo),
		PollInterval: validated.PollIntervalDuration(),
		WaitTimeout:  validated.WaitTimeoutDuration(),
		Verbose:      validated.Verbose,
	}

	resp, bodyBytes, err := c.GetContext(ctx, "v1/schemas")
	if err != nil {
		return nil, fmt.Errorf("fetch schemas from Steve at %s: %w", validated.URL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(http.MethodGet, "v1/schemas", resp.StatusCode, bodyBytes)
	}

	var schemas *schemaCollection
	if err := json.Unmarshal(bodyBytes, &schemas); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schemas: %w", err)
	}
	if schemas == nil {
		return nil, fmt.Errorf("failed to unmarshal schemas: response is null")
	}

	for _, s := range schemas.Data {
		namespaced := s.Attributes.Namespaced
		collPath := s.Links["collection"]
		if collPath == "" {
			collPath = "/v1/" + s.ID
		} else {
			collPath = ResolveRelativePath(collPath)
		}
		collPath = strings.TrimPrefix(collPath, "/")

		apiVersion := "v1"
		if s.Attributes.Group != "" {
			apiVersion = s.Attributes.Group + "/" + s.Attributes.Version
		} else if s.Attributes.Version != "" {
			apiVersion = s.Attributes.Version
		}

		c.Schemas[s.ID] = SchemaInfo{
			ID:                s.ID,
			Namespaced:        namespaced,
			CollectionPath:    collPath,
			APIVersion:        apiVersion,
			Kind:              s.Attributes.Kind,
			CollectionMethods: append([]string(nil), s.CollectionMethods...),
			ResourceMethods:   append([]string(nil), s.ResourceMethods...),
			Links:             cloneStringMap(s.Links),
		}
	}

	return c, nil
}

func (c *Client) Do(req *http.Request) (*http.Response, []byte, error) {
	if c == nil {
		return nil, nil, errors.New("Steve client is nil")
	}
	if c.HTTPClient == nil {
		return nil, nil, errors.New("Steve HTTP client is nil")
	}

	// Keep redirects on the origin that resolveEndpoint approved. net/http can
	// otherwise forward sensitive headers to redirects on related hosts. Use a
	// per-request client so the shared client remains safe for concurrent use
	// and preserve any stricter redirect policy supplied by the caller.
	httpClient := &http.Client{
		Transport:     c.HTTPClient.Transport,
		CheckRedirect: sameOriginRedirectPolicy(c.HTTPClient.CheckRedirect),
		Jar:           c.HTTPClient.Jar,
		Timeout:       c.HTTPClient.Timeout,
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	const maxResponseBytes = 64 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp, nil, err
	}
	if len(body) > maxResponseBytes {
		return resp, nil, fmt.Errorf("Steve response exceeded %d bytes", maxResponseBytes)
	}

	return resp, body, nil
}

func (c *Client) Request(method, path string, bodyAny any) (*http.Response, []byte, error) {
	return c.RequestContext(context.Background(), method, path, bodyAny)
}

func (c *Client) RequestContext(ctx context.Context, method, path string, bodyAny any) (*http.Response, []byte, error) {
	return c.requestContext(ctx, method, path, bodyAny, "")
}

func (c *Client) RequestWithContentType(method, path string, bodyAny any, contentType string) (*http.Response, []byte, error) {
	return c.RequestContextWithContentType(context.Background(), method, path, bodyAny, contentType)
}

func (c *Client) RequestContextWithContentType(ctx context.Context, method, path string, bodyAny any, contentType string) (*http.Response, []byte, error) {
	return c.requestContext(ctx, method, path, bodyAny, contentType)
}

func (c *Client) requestContext(ctx context.Context, method, path string, bodyAny any, contentType string) (*http.Response, []byte, error) {
	endpoint, err := c.resolveEndpoint(path)
	if err != nil {
		return nil, nil, err
	}

	var bodyReader io.Reader
	if bodyAny != nil {
		data, err := json.Marshal(bodyAny)
		if err != nil {
			return nil, nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "steve-tests (+https://github.com/rancher/steve-tests)")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if bodyAny != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}

	if c.Verbose {
		fmt.Printf("[steve] --> %s %s\n", method, endpoint)
	}

	resp, body, err := c.Do(req)

	if c.Verbose {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		fmt.Printf("[steve] <-- %s %s status=%d\n", method, endpoint, status)
	}

	return resp, body, err
}

func (c *Client) resolveEndpoint(path string) (string, error) {
	if c == nil {
		return "", errors.New("Steve client is nil")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parse Steve base URL: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return "", fmt.Errorf("invalid Steve base URL %q", base.Redacted())
	}
	candidate, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parse Steve request path %q: %w", path, err)
	}
	if candidate.Fragment != "" {
		return "", fmt.Errorf("refusing Steve request path with fragment")
	}
	if candidate.IsAbs() {
		if candidate.User != nil {
			return "", fmt.Errorf("refusing Steve request URL with user info")
		}
		if !sameOrigin(base, candidate) {
			return "", fmt.Errorf("refusing cross-origin Steve request to %s", candidate.Redacted())
		}
		return candidate.String(), nil
	}
	if candidate.Host != "" {
		return "", fmt.Errorf("refusing host-relative Steve request %q", path)
	}
	return c.BaseURL + "/" + strings.TrimLeft(path, "/"), nil
}

func (c *Client) Get(path string) (*http.Response, []byte, error) {
	return c.Request(http.MethodGet, path, nil)
}

func (c *Client) GetContext(ctx context.Context, path string) (*http.Response, []byte, error) {
	return c.RequestContext(ctx, http.MethodGet, path, nil)
}

func (c *Client) Post(path string, body any) (*http.Response, []byte, error) {
	return c.Request(http.MethodPost, path, body)
}

func (c *Client) PostContext(ctx context.Context, path string, body any) (*http.Response, []byte, error) {
	return c.RequestContext(ctx, http.MethodPost, path, body)
}

func (c *Client) Put(path string, body any) (*http.Response, []byte, error) {
	return c.Request(http.MethodPut, path, body)
}

func (c *Client) PutContext(ctx context.Context, path string, body any) (*http.Response, []byte, error) {
	return c.RequestContext(ctx, http.MethodPut, path, body)
}

func (c *Client) Patch(path string, body any, contentType string) (*http.Response, []byte, error) {
	return c.PatchContext(context.Background(), path, body, contentType)
}

func (c *Client) PatchContext(ctx context.Context, path string, body any, contentType string) (*http.Response, []byte, error) {
	return c.RequestContextWithContentType(ctx, http.MethodPatch, path, body, contentType)
}

func (c *Client) Delete(path string) (*http.Response, []byte, error) {
	return c.Request(http.MethodDelete, path, nil)
}

func (c *Client) DeleteContext(ctx context.Context, path string) (*http.Response, []byte, error) {
	return c.RequestContext(ctx, http.MethodDelete, path, nil)
}

func (c *Client) findSchema(resourceType string) (SchemaInfo, bool) {
	if c == nil {
		return SchemaInfo{}, false
	}
	if info, ok := c.Schemas[resourceType]; ok {
		return info, true
	}
	if info, ok := c.Schemas[resourceType+"s"]; ok {
		return info, true
	}
	if strings.HasSuffix(resourceType, "s") {
		singular := strings.TrimSuffix(resourceType, "s")
		if info, ok := c.Schemas[singular]; ok {
			return info, true
		}
	}
	return SchemaInfo{}, false
}

// Schema resolves the discovered Steve schema ID, including the limited
// singular/plural aliases supported by Resource.
func (c *Client) Schema(resourceType string) (SchemaInfo, bool) {
	return c.findSchema(resourceType)
}

func (c *Client) Resource(resourceType string) *ResourceClient {
	if c == nil {
		return &ResourceClient{
			resourceType: resourceType,
			schemaErr:    errors.New("Steve client is nil"),
		}
	}
	info, ok := c.findSchema(resourceType)
	if !ok {
		return &ResourceClient{
			client:       c,
			resourceType: resourceType,
			schemaErr:    fmt.Errorf("Steve schema %q was not discovered", resourceType),
		}
	}

	return &ResourceClient{
		client:         c,
		resourceType:   resourceType,
		namespaced:     info.Namespaced,
		collectionPath: info.CollectionPath,
	}
}

type ResourceClient struct {
	client         *Client
	resourceType   string
	namespace      string
	namespaced     bool
	collectionPath string
	schemaErr      error
}

func (rc *ResourceClient) Namespace(ns string) *ResourceClient {
	if rc == nil {
		return &ResourceClient{schemaErr: errors.New("Steve resource client is nil")}
	}
	next := *rc
	if rc.namespaced && ns != "" {
		if err := validateStevePathSegment("namespace", ns); err != nil {
			next.schemaErr = err
			return &next
		}
	}
	next.namespace = ns
	return &next
}

func (rc *ResourceClient) WaitTimeout() time.Duration {
	if rc == nil || rc.client == nil || rc.client.WaitTimeout <= 0 {
		return 30 * time.Second
	}
	return rc.client.WaitTimeout
}

func (rc *ResourceClient) PollInterval() time.Duration {
	return rc.pollInterval()
}

func (rc *ResourceClient) getCollectionPath() string {
	if rc.namespaced && rc.namespace != "" {
		return rc.collectionPath + "/" + url.PathEscape(rc.namespace)
	}
	return rc.collectionPath
}

func (rc *ResourceClient) getResourcePath(name string) string {
	if rc.namespaced && rc.namespace != "" {
		return rc.collectionPath + "/" + url.PathEscape(rc.namespace) + "/" + url.PathEscape(name)
	}
	return rc.collectionPath + "/" + url.PathEscape(name)
}

func (rc *ResourceClient) Create(body map[string]any) (map[string]any, error) {
	return rc.CreateContext(context.Background(), body)
}

func (rc *ResourceClient) CreateContext(ctx context.Context, body map[string]any) (map[string]any, error) {
	if err := rc.validate(); err != nil {
		return nil, err
	}
	path := rc.getCollectionPath()
	resp, bodyBytes, err := rc.client.PostContext(ctx, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, statusError(http.MethodPost, path, resp.StatusCode, bodyBytes)
	}
	return decodeResourceResponse(http.MethodPost, path, bodyBytes)
}

func (rc *ResourceClient) Get(name string) (map[string]any, error) {
	return rc.GetContext(context.Background(), name)
}

func (rc *ResourceClient) GetContext(ctx context.Context, name string) (map[string]any, error) {
	if err := rc.validateResourceName(name); err != nil {
		return nil, err
	}
	path := rc.getResourcePath(name)
	resp, bodyBytes, err := rc.client.GetContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(http.MethodGet, path, resp.StatusCode, bodyBytes)
	}
	return decodeResourceResponse(http.MethodGet, path, bodyBytes)
}

func (rc *ResourceClient) Update(name string, body map[string]any) (map[string]any, error) {
	return rc.UpdateContext(context.Background(), name, body)
}

func (rc *ResourceClient) UpdateContext(ctx context.Context, name string, body map[string]any) (map[string]any, error) {
	if err := rc.validateResourceName(name); err != nil {
		return nil, err
	}
	path := rc.getResourcePath(name)
	resp, bodyBytes, err := rc.client.PutContext(ctx, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(http.MethodPut, path, resp.StatusCode, bodyBytes)
	}
	return decodeResourceResponse(http.MethodPut, path, bodyBytes)
}

func (rc *ResourceClient) Patch(name string, body any, contentType string) (map[string]any, error) {
	return rc.PatchContext(context.Background(), name, body, contentType)
}

func (rc *ResourceClient) PatchContext(ctx context.Context, name string, body any, contentType string) (map[string]any, error) {
	if err := rc.validateResourceName(name); err != nil {
		return nil, err
	}
	path := rc.getResourcePath(name)
	resp, bodyBytes, err := rc.client.PatchContext(ctx, path, body, contentType)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(http.MethodPatch, path, resp.StatusCode, bodyBytes)
	}
	return decodeResourceResponse(http.MethodPatch, path, bodyBytes)
}

func (rc *ResourceClient) Delete(name string) error {
	return rc.DeleteContext(context.Background(), name)
}

func (rc *ResourceClient) DeleteContext(ctx context.Context, name string) error {
	if err := rc.validateResourceName(name); err != nil {
		return err
	}
	path := rc.getResourcePath(name)
	resp, bodyBytes, err := rc.client.DeleteContext(ctx, path)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusNotFound &&
		resp.StatusCode != http.StatusGone {
		return statusError(http.MethodDelete, path, resp.StatusCode, bodyBytes)
	}
	return nil
}

func (rc *ResourceClient) List(query string) (*SteveCollection, error) {
	return rc.ListContext(context.Background(), query)
}

func (rc *ResourceClient) ListValues(values url.Values) (*SteveCollection, error) {
	return rc.ListValuesContext(context.Background(), values)
}

func (rc *ResourceClient) ListValuesContext(ctx context.Context, values url.Values) (*SteveCollection, error) {
	if values == nil {
		return rc.ListContext(ctx, "")
	}
	return rc.ListContext(ctx, values.Encode())
}

func (rc *ResourceClient) ListContext(ctx context.Context, query string) (*SteveCollection, error) {
	if err := rc.validate(); err != nil {
		return nil, err
	}
	path := rc.getCollectionPath()
	if query != "" {
		path = appendRawQuery(path, query)
	}
	resp, bodyBytes, err := rc.client.GetContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(http.MethodGet, path, resp.StatusCode, bodyBytes)
	}
	return decodeCollectionResponse(path, bodyBytes)
}

func (rc *ResourceClient) validate() error {
	if rc == nil {
		return errors.New("Steve resource client is nil")
	}
	if rc.schemaErr != nil {
		return rc.schemaErr
	}
	if rc.client == nil {
		return errors.New("Steve client is nil")
	}
	return nil
}

func (rc *ResourceClient) validateResourceName(name string) error {
	if err := rc.validate(); err != nil {
		return err
	}
	if name == "" {
		return errors.New("Steve resource name is empty")
	}
	return validateStevePathSegment("resource name", name)
}

func validateStevePathSegment(kind string, value string) error {
	if value == "." || value == ".." {
		return fmt.Errorf("invalid Steve %s %q", kind, value)
	}
	if strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("invalid Steve %s %q: path separators are not allowed", kind, value)
	}
	// PathEscape protects one decoding pass. Reject a literal percent as well
	// so a proxy/router that performs an unsafe second decode cannot turn an
	// encoded separator into a new path segment.
	if strings.Contains(value, "%") {
		return fmt.Errorf("invalid Steve %s %q: percent escapes are not allowed", kind, value)
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return fmt.Errorf("invalid Steve %s %q: control characters are not allowed", kind, value)
		}
	}
	return nil
}

func (rc *ResourceClient) WaitForDelete(name string, timeout time.Duration) error {
	return rc.WaitForDeleteContext(context.Background(), name, timeout)
}

func (rc *ResourceClient) WaitForDeleteContext(ctx context.Context, name string, timeout time.Duration) error {
	if err := rc.validateWait(ctx, name, timeout); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(rc.pollInterval())
	defer ticker.Stop()

	var lastErr error
	for {
		_, err := rc.GetContext(ctx, name)
		if IsStatus(err, http.StatusNotFound, http.StatusGone) {
			return nil
		}
		if err != nil && !isRetryableWaitError(err) {
			if ctx.Err() != nil {
				return waitTimeoutError(fmt.Sprintf("waiting for deletion of %s/%s", rc.resourceType, name), ctx.Err(), err)
			}
			return err
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return waitTimeoutError(fmt.Sprintf("waiting for deletion of %s/%s", rc.resourceType, name), ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (rc *ResourceClient) WaitForGet(name string, timeout time.Duration) (map[string]any, error) {
	return rc.WaitForGetContext(context.Background(), name, timeout)
}

func (rc *ResourceClient) WaitForGetContext(ctx context.Context, name string, timeout time.Duration) (map[string]any, error) {
	if err := rc.validateWait(ctx, name, timeout); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(rc.pollInterval())
	defer ticker.Stop()

	var lastErr error
	for {
		resource, err := rc.GetContext(ctx, name)
		if err == nil {
			return resource, nil
		}
		if !IsStatus(err, http.StatusNotFound, http.StatusGone) && !isRetryableWaitError(err) {
			if ctx.Err() != nil {
				return nil, waitTimeoutError(fmt.Sprintf("waiting for %s/%s to become visible", rc.resourceType, name), ctx.Err(), err)
			}
			return nil, err
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return nil, waitTimeoutError(fmt.Sprintf("waiting for %s/%s to become visible", rc.resourceType, name), ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (rc *ResourceClient) WaitForListContains(name string, query string, timeout time.Duration) error {
	return rc.WaitForListContainsContext(context.Background(), name, query, timeout)
}

func (rc *ResourceClient) WaitForListContainsContext(ctx context.Context, name string, query string, timeout time.Duration) error {
	if err := rc.validateWait(ctx, name, timeout); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(rc.pollInterval())
	defer ticker.Stop()

	var lastErr error
	for {
		coll, err := rc.ListContext(ctx, query)
		if err == nil {
			contains, validationErr := collectionContains(coll, name, rc.namespace)
			if validationErr != nil {
				return fmt.Errorf("inspect %s collection while waiting for %q: %w", rc.resourceType, name, validationErr)
			}
			if contains {
				return nil
			}
		}
		if err != nil && !isRetryableWaitError(err) {
			if ctx.Err() != nil {
				return waitTimeoutError(fmt.Sprintf("waiting for %s/%s to appear in list", rc.resourceType, name), ctx.Err(), err)
			}
			return err
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return waitTimeoutError(fmt.Sprintf("waiting for %s/%s to appear in list", rc.resourceType, name), ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (rc *ResourceClient) WaitForListNotContains(name string, query string, timeout time.Duration) error {
	return rc.WaitForListNotContainsContext(context.Background(), name, query, timeout)
}

func (rc *ResourceClient) WaitForListNotContainsContext(ctx context.Context, name string, query string, timeout time.Duration) error {
	if err := rc.validateWait(ctx, name, timeout); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(rc.pollInterval())
	defer ticker.Stop()

	var lastErr error
	for {
		coll, err := rc.ListContext(ctx, query)
		if err == nil {
			contains, validationErr := collectionContains(coll, name, rc.namespace)
			if validationErr != nil {
				return fmt.Errorf("inspect %s collection while waiting for %q: %w", rc.resourceType, name, validationErr)
			}
			if !contains {
				return nil
			}
		}
		if err != nil && !isRetryableWaitError(err) {
			if ctx.Err() != nil {
				return waitTimeoutError(fmt.Sprintf("waiting for %s/%s to disappear from list", rc.resourceType, name), ctx.Err(), err)
			}
			return err
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return waitTimeoutError(fmt.Sprintf("waiting for %s/%s to disappear from list", rc.resourceType, name), ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (rc *ResourceClient) pollInterval() time.Duration {
	if rc == nil || rc.client == nil || rc.client.PollInterval <= 0 {
		return 200 * time.Millisecond
	}
	return rc.client.PollInterval
}

func (rc *ResourceClient) validateWait(ctx context.Context, name string, timeout time.Duration) error {
	if err := rc.validateResourceName(name); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("Steve wait context is nil")
	}
	if timeout <= 0 {
		return fmt.Errorf("Steve wait timeout must be greater than zero, got %s", timeout)
	}
	return nil
}

func isRetryableWaitError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		if statusErr == nil {
			return false
		}
		switch statusErr.StatusCode {
		case http.StatusRequestTimeout,
			http.StatusTooEarly,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}
	var operationErr *net.OpError
	if errors.As(err, &operationErr) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func waitTimeoutError(operation string, contextErr error, lastErr error) error {
	if lastErr != nil && !errors.Is(lastErr, contextErr) {
		return fmt.Errorf("timed out %s; last error: %v: %w", operation, lastErr, contextErr)
	}
	return fmt.Errorf("timed out %s: %w", operation, contextErr)
}

func collectionContains(coll *SteveCollection, name string, namespace string) (bool, error) {
	if coll == nil {
		return false, errors.New("collection is nil")
	}
	if coll.dataNull {
		return false, errors.New("collection data field is null")
	}
	if !coll.dataPresent && coll.Data == nil {
		return false, errors.New("collection data field is missing")
	}
	found := false
	for index, item := range coll.Data {
		if item == nil {
			return false, fmt.Errorf("collection item %d is null", index)
		}
		metadata, ok := item["metadata"].(map[string]any)
		if !ok {
			return false, fmt.Errorf("collection item %d has no metadata object", index)
		}
		itemName, ok := metadata["name"].(string)
		if !ok || itemName == "" {
			return false, fmt.Errorf("collection item %d has no non-empty metadata.name string", index)
		}
		if namespace == "" {
			if itemName == name {
				found = true
			}
			continue
		}
		itemNamespace, ok := metadata["namespace"].(string)
		if !ok || itemNamespace == "" {
			return false, fmt.Errorf("collection item %d has no non-empty metadata.namespace string", index)
		}
		if itemName == name && itemNamespace == namespace {
			found = true
		}
	}
	return found, nil
}

func decodeResourceResponse(method string, path string, body []byte) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode Steve %s %s response: %w", method, path, err)
	}
	if result == nil {
		return nil, fmt.Errorf("decode Steve %s %s response: response is null", method, path)
	}
	return result, nil
}

func decodeCollectionResponse(path string, body []byte) (*SteveCollection, error) {
	var result *SteveCollection
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode Steve GET %s response: %w", path, err)
	}
	if result == nil {
		return nil, fmt.Errorf("decode Steve GET %s response: response is null", path)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("inspect Steve GET %s collection response: %w", path, err)
	}
	rawData, present := fields["data"]
	result.dataPresent = present
	result.dataNull = present && bytes.Equal(bytes.TrimSpace(rawData), []byte("null"))
	return result, nil
}

func statusError(method, path string, code int, body []byte) error {
	const maxErrorBodyBytes = 4096
	preview := body
	truncated := false
	if len(preview) > maxErrorBodyBytes {
		preview = preview[:maxErrorBodyBytes]
		truncated = true
	}
	bodyText := strings.TrimSpace(string(preview))
	if truncated {
		bodyText += "... (truncated)"
	}
	return &StatusError{
		Method:     method,
		Path:       path,
		StatusCode: code,
		Body:       bodyText,
	}
}

func appendRawQuery(path string, query string) string {
	query = strings.TrimLeft(query, "?&")
	if query == "" {
		return path
	}
	if strings.Contains(path, "?") {
		return path + "&" + query
	}
	return path + "?" + query
}

// ResolveRelativePath extracts the path and query string from a possibly absolute URL.
// If the input is not a valid URL or is already relative, it returns the input.
func ResolveRelativePath(link string) string {
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	p := u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

func sameOrigin(left *url.URL, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func sameOriginRedirectPolicy(callerPolicy func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 || via[0] == nil || via[0].URL == nil {
			return errors.New("Steve redirect has no originating request")
		}
		// CheckRedirect receives mutable request pointers. Snapshot the approved
		// origin before a caller policy can modify via[0] or the target request.
		origin := &url.URL{Scheme: via[0].URL.Scheme, Host: via[0].URL.Host}
		if callerPolicy != nil {
			if err := callerPolicy(req, via); err != nil {
				// In particular, preserve http.ErrUseLastResponse: a caller that
				// refuses every redirect should receive the original 3xx response
				// without our policy converting it into an error.
				return err
			}
		}
		// Check after the caller policy. A custom policy is allowed to mutate
		// req, but it must not be able to redirect the bearer token off-origin.
		if req == nil || req.URL == nil {
			return errors.New("Steve redirect target URL is nil")
		}
		if req.URL.User != nil || !sameOrigin(origin, req.URL) {
			return fmt.Errorf("refusing cross-origin Steve redirect to %s", req.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
