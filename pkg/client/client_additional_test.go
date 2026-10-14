package client

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRequestScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "RequestSetsHeadersAndPreservesPatchContentType", run: testRequestSetsHeadersAndPreservesPatchContentType},
		{name: "CrossOriginRequestNeverReachesTransport", run: testCrossOriginRequestNeverReachesTransport},
		{name: "CrossOriginRedirectNeverReceivesBearerToken", run: testCrossOriginRedirectNeverReceivesBearerToken},
		{name: "CallerRedirectRefusalPreservesLastResponse", run: testCallerRedirectRefusalPreservesLastResponse},
		{name: "CallerRedirectPolicyCannotMutateApprovedOrigin", run: testCallerRedirectPolicyCannotMutateApprovedOrigin},
		{name: "SameOriginRedirectPreservesConfiguredBehavior", run: testSameOriginRedirectPreservesConfiguredBehavior},
		{name: "UnknownSchemaFailsBeforeRequest", run: testUnknownSchemaFailsBeforeRequest},
		{name: "NilClientAndResourceReturnErrors", run: testNilClientAndResourceReturnErrors},
		{name: "ResourceClientDecodesSteveCollectionContract", run: testResourceClientDecodesSteveCollectionContract},
		{name: "ResourceClientPatchUsesNamespacedPath", run: testResourceClientPatchUsesNamespacedPath},
		{name: "DiscoverUsesCustomCertificateAuthority", run: testDiscoverUsesCustomCertificateAuthority},
		{name: "DiscoverRejectsInvalidCertificateAuthority", run: testDiscoverRejectsInvalidCertificateAuthority},
		{name: "WaitHelpersRetryUntilResourceAppearsAndDisappears", run: testWaitHelpersRetryUntilResourceAppearsAndDisappears},
		{name: "WaitForGetRetriesTransientTransportEOF", run: testWaitForGetRetriesTransientTransportEOF},
		{name: "WaitHelpersRejectInvalidContextAndTimeout", run: testWaitHelpersRejectInvalidContextAndTimeout},
		{name: "RequestContextHonorsCancellation", run: testRequestContextHonorsCancellation},
		{name: "StatusErrorTruncatesLargeBodies", run: testStatusErrorTruncatesLargeBodies},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestResolveEndpointPinsRequestsToConfiguredOrigin(t *testing.T) {
	t.Parallel()

	client := &Client{BaseURL: "https://steve.example.test/base"}
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr string
	}{
		{name: "relative", path: "v1/schemas", want: "https://steve.example.test/base/v1/schemas"},
		{name: "leading slash remains under base", path: "/v1/pods", want: "https://steve.example.test/base/v1/pods"},
		{name: "same origin absolute", path: "https://steve.example.test/v1/pods?page=2", want: "https://steve.example.test/v1/pods?page=2"},
		{name: "same host different scheme", path: "http://steve.example.test/v1", wantErr: "cross-origin"},
		{name: "same host different port", path: "https://steve.example.test:9443/v1", wantErr: "cross-origin"},
		{name: "different host", path: "https://attacker.example/v1", wantErr: "cross-origin"},
		{name: "same origin with user info", path: "https://user:secret@steve.example.test/v1", wantErr: "user info"},
		{name: "host relative", path: "//attacker.example/v1", wantErr: "host-relative"},
		{name: "fragment", path: "v1/pods#not-sent", wantErr: "fragment"},
		{name: "malformed", path: "%gh", wantErr: "parse Steve request path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := client.resolveEndpoint(test.path)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("resolveEndpoint() error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveEndpoint() returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveEndpoint() = %q, want %q", got, test.want)
			}
		})
	}
}

func testRequestSetsHeadersAndPreservesPatchContentType(t *testing.T) {
	t.Parallel()

	var gotMethod string
	var gotContentType string
	var gotAuthorization string
	var gotAccept string
	var gotUserAgent string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotAuthorization = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotUserAgent = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		writeJSON(t, w, map[string]any{"ok": true})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, Token: "token-abc:secret", HTTPClient: server.Client()}
	resp, _, err := client.RequestWithContentType(http.MethodPatch, "v1/configmaps/qa/example", map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"patched": "true"}},
	}, "application/merge-patch+json")
	if err != nil {
		t.Fatalf("RequestWithContentType() returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Fatalf("content type = %q", gotContentType)
	}
	if gotAuthorization != "Bearer token-abc:secret" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if gotAccept != "application/json" {
		t.Fatalf("accept = %q", gotAccept)
	}
	if !strings.HasPrefix(gotUserAgent, "steve-tests") {
		t.Fatalf("user agent = %q", gotUserAgent)
	}
	if nested, _ := gotBody["metadata"].(map[string]any); nested == nil {
		t.Fatalf("body = %#v", gotBody)
	}
}

func testCrossOriginRequestNeverReachesTransport(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	client := &Client{
		BaseURL: "https://steve.example.test",
		Token:   "do-not-leak",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, errors.New("transport must not be called")
		})},
	}
	_, _, err := client.Get("https://elsewhere.example/v1/secrets")
	if err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("Get() error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("transport received %d request(s)", requests.Load())
	}
}

func testCrossOriginRedirectNeverReceivesBearerToken(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		writeJSON(t, w, map[string]any{"data": []map[string]any{}})
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/schemas", http.StatusFound)
	}))
	defer source.Close()

	client := &Client{
		BaseURL:    source.URL,
		Token:      "do-not-leak",
		HTTPClient: source.Client(),
	}
	_, _, err := client.Get("v1/schemas")
	if err == nil || !strings.Contains(err.Error(), "cross-origin Steve redirect") {
		t.Fatalf("Get() error = %v", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d request(s)", got)
	}
}

func testCallerRedirectRefusalPreservesLastResponse(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		writeJSON(t, w, map[string]any{"unexpected": true})
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/schemas", http.StatusFound)
	}))
	defer source.Close()

	configuredHTTPClient := source.Client()
	var policyCalls atomic.Int32
	configuredHTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		policyCalls.Add(1)
		return http.ErrUseLastResponse
	}
	client := &Client{
		BaseURL:    source.URL,
		Token:      "do-not-leak",
		HTTPClient: configuredHTTPClient,
	}
	response, _, err := client.Get("v1/schemas")
	if err != nil {
		t.Fatalf("Get() returned error instead of the refused redirect response: %v", err)
	}
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusFound)
	}
	if policyCalls.Load() != 1 {
		t.Fatalf("configured redirect policy calls = %d, want 1", policyCalls.Load())
	}
	if targetRequests.Load() != 0 {
		t.Fatalf("refused redirect target received %d request(s)", targetRequests.Load())
	}
}

func testCallerRedirectPolicyCannotMutateApprovedOrigin(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		writeJSON(t, w, map[string]any{"unexpected": true})
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/schemas", http.StatusFound)
	}))
	defer source.Close()

	configuredHTTPClient := source.Client()
	configuredHTTPClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		// A hostile or buggy policy must not be able to redefine the origin the
		// Steve wrapper approved before it returns nil to follow the redirect.
		via[0].URL = request.URL
		return nil
	}
	client := &Client{
		BaseURL:    source.URL,
		Token:      "do-not-leak",
		HTTPClient: configuredHTTPClient,
	}
	_, _, err := client.Get("v1/schemas")
	if err == nil || !strings.Contains(err.Error(), "cross-origin Steve redirect") {
		t.Fatalf("Get() error = %v, want cross-origin refusal", err)
	}
	if targetRequests.Load() != 0 {
		t.Fatalf("redirect target received %d request(s)", targetRequests.Load())
	}
}

func testSameOriginRedirectPreservesConfiguredBehavior(t *testing.T) {
	t.Parallel()

	var sawAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/schemas":
			http.Redirect(w, r, "/redirected/schemas", http.StatusFound)
		case "/redirected/schemas":
			sawAuthorization = r.Header.Get("Authorization")
			writeJSON(t, w, map[string]any{"data": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		Token:      "token-abc:secret",
		HTTPClient: server.Client(),
	}
	resp, _, err := client.Get("v1/schemas")
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("redirected status = %d", resp.StatusCode)
	}
	if sawAuthorization != "Bearer token-abc:secret" {
		t.Fatalf("redirected authorization = %q", sawAuthorization)
	}
}

func testUnknownSchemaFailsBeforeRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	client := &Client{
		BaseURL: "https://steve.example.test",
		Schemas: map[string]SchemaInfo{},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, errors.New("transport must not be called")
		})},
	}
	_, err := client.Resource("typo.resource").List("")
	if err == nil || !strings.Contains(err.Error(), `schema "typo.resource" was not discovered`) {
		t.Fatalf("List() error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("transport received %d request(s)", requests.Load())
	}
}

func TestResourceOperationsRejectUnsafePathSegmentsBeforeRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	client := &Client{
		BaseURL: "https://steve.example.test",
		Schemas: map[string]SchemaInfo{
			"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, errors.New("transport must not be called")
		})},
	}
	resource := client.Resource("configmap").Namespace("qa")
	tests := []struct {
		name      string
		operation func() error
	}{
		{name: "get empty name", operation: func() error {
			_, err := resource.Get("")
			return err
		}},
		{name: "update empty name", operation: func() error {
			_, err := resource.Update("", map[string]any{})
			return err
		}},
		{name: "patch dot name", operation: func() error {
			_, err := resource.Patch(".", map[string]any{}, "application/merge-patch+json")
			return err
		}},
		{name: "delete parent name", operation: func() error { return resource.Delete("..") }},
		{name: "get embedded parent traversal", operation: func() error {
			_, err := resource.Get("../namespace/victim")
			return err
		}},
		{name: "delete embedded child traversal", operation: func() error {
			return resource.Delete("safe/../../victim")
		}},
		{name: "encoded path separator", operation: func() error {
			_, err := resource.Get("safe%2Fvictim")
			return err
		}},
		{name: "backslash path separator", operation: func() error {
			_, err := resource.Get(`safe\..\victim`)
			return err
		}},
		{name: "parent namespace", operation: func() error {
			_, err := client.Resource("configmap").Namespace("..").List("")
			return err
		}},
		{name: "embedded parent namespace", operation: func() error {
			_, err := client.Resource("configmap").Namespace("qa/../system").List("")
			return err
		}},
		{name: "encoded separator namespace", operation: func() error {
			_, err := client.Resource("configmap").Namespace("qa%2Fsystem").List("")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.operation(); err == nil {
				t.Fatal("operation unexpectedly succeeded")
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("transport received %d request(s)", got)
	}
}

func testNilClientAndResourceReturnErrors(t *testing.T) {
	t.Parallel()

	var nilClient *Client
	if _, _, err := nilClient.Get("v1/schemas"); err == nil || !strings.Contains(err.Error(), "Steve client is nil") {
		t.Fatalf("nil client request error = %v", err)
	}
	if _, err := nilClient.Resource("configmap").List(""); err == nil || !strings.Contains(err.Error(), "Steve client is nil") {
		t.Fatalf("nil client error = %v", err)
	}

	var nilResource *ResourceClient
	if _, err := nilResource.Namespace("qa").List(""); err == nil || !strings.Contains(err.Error(), "resource client is nil") {
		t.Fatalf("nil resource error = %v", err)
	}
}

func testResourceClientDecodesSteveCollectionContract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/configmaps/qa" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("pagesize") != "1" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]any{
			"type":         "collection",
			"resourceType": "configmap",
			"revision":     "1233",
			"count":        11,
			"pages":        11,
			"data":         []map[string]any{{"metadata": map[string]any{"name": "one"}}},
			"links":        map[string]string{"self": serverURL(r) + "/v1/configmaps/qa"},
			"createTypes":  map[string]string{"configmap": serverURL(r) + "/v1/configmaps/qa"},
			"summary": []map[string]any{
				{
					"property": "metadata.namespace",
					"counts": map[string]any{
						"qa": map[string]any{"total": 1},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Schemas: map[string]SchemaInfo{
			"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
		},
	}
	collection, err := client.Resource("configmap").Namespace("qa").ListValues(url.Values{"pagesize": {"1"}})
	if err != nil {
		t.Fatalf("ListValues() returned error: %v", err)
	}
	if collection.ResourceType != "configmap" || collection.Revision != "1233" {
		t.Fatalf("collection metadata = %#v", collection)
	}
	if collection.Count != 11 || collection.Pages != 11 || len(collection.Data) != 1 {
		t.Fatalf("collection paging fields = %#v", collection)
	}
	if collection.Links["self"] == "" || collection.CreateTypes["configmap"] == "" || len(collection.Summary) != 1 {
		t.Fatalf("collection extension fields = %#v", collection)
	}
	if collection.Summary[0].Counts["qa"].Total != 1 {
		t.Fatalf("collection summary = %#v", collection.Summary)
	}
}

func TestResourceClientRejectsNullSuccessResponses(t *testing.T) {
	t.Parallel()

	client := &Client{
		BaseURL: "https://steve.example.test",
		Schemas: map[string]SchemaInfo{
			"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("null")),
			}, nil
		})},
	}
	resource := client.Resource("configmap").Namespace("qa")
	tests := []struct {
		name      string
		operation func() error
	}{
		{name: "create", operation: func() error {
			_, err := resource.Create(map[string]any{"metadata": map[string]any{"name": "example"}})
			return err
		}},
		{name: "get", operation: func() error {
			_, err := resource.Get("example")
			return err
		}},
		{name: "update", operation: func() error {
			_, err := resource.Update("example", map[string]any{})
			return err
		}},
		{name: "patch", operation: func() error {
			_, err := resource.Patch("example", map[string]any{}, "application/merge-patch+json")
			return err
		}},
		{name: "list", operation: func() error {
			_, err := resource.List("")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.operation(); err == nil || !strings.Contains(err.Error(), "response is null") {
				t.Fatalf("operation error = %v", err)
			}
		})
	}
}

func testResourceClientPatchUsesNamespacedPath(t *testing.T) {
	t.Parallel()

	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/configmaps/qa/example" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		gotContentType = r.Header.Get("Content-Type")
		writeJSON(t, w, map[string]any{"metadata": map[string]any{"name": "example", "namespace": "qa"}})
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Schemas: map[string]SchemaInfo{
			"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
		},
	}
	patched, err := client.Resource("configmap").Namespace("qa").Patch("example", map[string]any{}, "application/json-patch+json")
	if err != nil {
		t.Fatalf("Patch() returned error: %v", err)
	}
	if gotContentType != "application/json-patch+json" {
		t.Fatalf("content type = %q", gotContentType)
	}
	metadata, _ := patched["metadata"].(map[string]any)
	if metadata["name"] != "example" {
		t.Fatalf("patched body = %#v", patched)
	}
}

func TestResourceDeleteTreatsAbsentStatusesAsSuccess(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/configmaps/qa/example" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
				Schemas: map[string]SchemaInfo{
					"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
				},
			}
			if err := client.Resource("configmap").Namespace("qa").Delete("example"); err != nil {
				t.Fatalf("Delete() for status %d returned error: %v", status, err)
			}
		})
	}
}

func testDiscoverUsesCustomCertificateAuthority(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"data": []map[string]any{}})
	}))
	defer server.Close()

	certificate, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatalf("parse test server certificate: %v", err)
	}
	caPath := t.TempDir() + "/ca.pem"
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := osWriteFile(caPath, pemBytes); err != nil {
		t.Fatalf("write test CA: %v", err)
	}

	cfg := testConfig(server.URL)
	cfg.Insecure = false
	cfg.CAFile = caPath
	if _, err := Discover(cfg); err != nil {
		t.Fatalf("Discover() with custom CA returned error: %v", err)
	}
}

func testDiscoverRejectsInvalidCertificateAuthority(t *testing.T) {
	t.Parallel()

	caPath := t.TempDir() + "/ca.pem"
	if err := osWriteFile(caPath, []byte("not a certificate")); err != nil {
		t.Fatalf("write invalid CA: %v", err)
	}
	cfg := testConfig("https://127.0.0.1:9443")
	cfg.Insecure = false
	cfg.CAFile = caPath
	_, err := Discover(cfg)
	if err == nil || !strings.Contains(err.Error(), "does not contain a PEM certificate") {
		t.Fatalf("Discover() error = %v", err)
	}
}

func testWaitHelpersRetryUntilResourceAppearsAndDisappears(t *testing.T) {
	t.Parallel()

	var gets atomic.Int32
	var lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/configmaps/qa/example":
			if gets.Add(1) == 1 {
				http.NotFound(w, r)
				return
			}
			writeJSON(t, w, map[string]any{"metadata": map[string]any{"name": "example"}})
		case r.URL.Path == "/v1/configmaps/qa":
			call := lists.Add(1)
			data := []map[string]any{}
			if call == 2 {
				data = append(data, map[string]any{"metadata": map[string]any{"name": "example", "namespace": "qa"}})
			}
			writeJSON(t, w, map[string]any{"type": "collection", "data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
		WaitTimeout:  time.Second,
		Schemas: map[string]SchemaInfo{
			"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
		},
	}
	resources := client.Resource("configmap").Namespace("qa")
	if _, err := resources.WaitForGet("example", time.Second); err != nil {
		t.Fatalf("WaitForGet() returned error: %v", err)
	}
	if err := resources.WaitForListContains("example", "", time.Second); err != nil {
		t.Fatalf("WaitForListContains() returned error: %v", err)
	}
	if err := resources.WaitForListNotContains("example", "", time.Second); err != nil {
		t.Fatalf("WaitForListNotContains() returned error: %v", err)
	}
}

func TestWaitHelpersRejectMalformedCollectionItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		payload string
		wantErr string
	}{
		{name: "missing data", payload: `{"type":"collection"}`, wantErr: "data field is missing"},
		{name: "null data", data: `null`, wantErr: "data field is null"},
		{name: "null item", data: `[null]`, wantErr: "collection item 0 is null"},
		{name: "missing metadata", data: `[{"id":"qa/example"}]`, wantErr: "has no metadata object"},
		{name: "missing name", data: `[{"metadata":{"namespace":"qa"}}]`, wantErr: "metadata.name"},
		{name: "missing namespace", data: `[{"metadata":{"name":"other"}}]`, wantErr: "metadata.namespace"},
		{name: "malformed item after match", data: `[{"metadata":{"name":"example","namespace":"qa"}},null]`, wantErr: "collection item 1 is null"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				payload := test.payload
				if payload == "" {
					payload = `{"type":"collection","data":` + test.data + `}`
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()

			client := &Client{
				BaseURL:      server.URL,
				HTTPClient:   server.Client(),
				PollInterval: time.Millisecond,
				Schemas: map[string]SchemaInfo{
					"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
				},
			}
			resources := client.Resource("configmap").Namespace("qa")
			for _, wait := range []struct {
				name string
				call func() error
			}{
				{name: "contains", call: func() error { return resources.WaitForListContains("example", "", time.Second) }},
				{name: "not contains", call: func() error { return resources.WaitForListNotContains("example", "", time.Second) }},
			} {
				err := wait.call()
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("%s error = %v, want substring %q", wait.name, err, test.wantErr)
				}
			}
		})
	}
}

func testWaitForGetRetriesTransientTransportEOF(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	client := &Client{
		BaseURL:      "https://steve.example.test",
		PollInterval: time.Millisecond,
		WaitTimeout:  time.Second,
		Schemas: map[string]SchemaInfo{
			"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 {
				return nil, io.EOF
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"metadata":{"name":"example"}}`)),
				Request:    request,
			}, nil
		})},
	}

	resource, err := client.Resource("configmap").Namespace("qa").WaitForGet("example", time.Second)
	if err != nil {
		t.Fatalf("WaitForGet() returned error after transient EOF: %v", err)
	}
	metadata, _ := resource["metadata"].(map[string]any)
	if metadata["name"] != "example" {
		t.Fatalf("WaitForGet() resource metadata = %#v", metadata)
	}
	if requests.Load() != 2 {
		t.Fatalf("transport requests = %d, want 2", requests.Load())
	}
}

func TestWaitHelpersValidateBeforeStartingTimer(t *testing.T) {
	t.Parallel()

	invalid := &ResourceClient{}
	tests := []struct {
		name string
		wait func() error
	}{
		{name: "delete", wait: func() error { return invalid.WaitForDelete("example", time.Second) }},
		{name: "get", wait: func() error {
			_, err := invalid.WaitForGet("example", time.Second)
			return err
		}},
		{name: "list contains", wait: func() error {
			return invalid.WaitForListContains("example", "", time.Second)
		}},
		{name: "list not contains", wait: func() error {
			return invalid.WaitForListNotContains("example", "", time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.wait(); err == nil || !strings.Contains(err.Error(), "Steve client is nil") {
				t.Fatalf("wait error = %v", err)
			}
		})
	}
}

func testWaitHelpersRejectInvalidContextAndTimeout(t *testing.T) {
	t.Parallel()

	resource := &ResourceClient{client: &Client{}}
	if err := resource.WaitForDeleteContext(nil, "example", time.Second); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("nil-context error = %v", err)
	}
	if _, err := resource.WaitForGetContext(context.Background(), "example", 0); err == nil || !strings.Contains(err.Error(), "greater than zero") {
		t.Fatalf("zero-timeout error = %v", err)
	}

	var nilResource *ResourceClient
	if got := nilResource.WaitTimeout(); got != 30*time.Second {
		t.Fatalf("nil resource wait timeout = %s", got)
	}
	if got := nilResource.PollInterval(); got != 200*time.Millisecond {
		t.Fatalf("nil resource poll interval = %s", got)
	}
}

func testRequestContextHonorsCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _, err := client.GetContext(ctx, "v1/slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetContext() error = %v", err)
	}
}

func testStatusErrorTruncatesLargeBodies(t *testing.T) {
	t.Parallel()

	err := statusError(http.MethodGet, "v1/secrets", http.StatusInternalServerError, []byte(strings.Repeat("x", 5000)))
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error type = %T", err)
	}
	if len(statusErr.Body) >= 5000 || !strings.HasSuffix(statusErr.Body, "... (truncated)") {
		t.Fatalf("status body was not truncated: length=%d suffix=%q", len(statusErr.Body), statusErr.Body[len(statusErr.Body)-20:])
	}
	if !IsStatus(fmt.Errorf("wrapped: %w", err), http.StatusInternalServerError) {
		t.Fatal("IsStatus() should match wrapped StatusError")
	}
	var nilStatus *StatusError
	if IsStatus(nilStatus, http.StatusInternalServerError) {
		t.Fatal("IsStatus() should not match a typed-nil StatusError")
	}
	if got := nilStatus.Error(); !strings.Contains(got, "unknown status") {
		t.Fatalf("typed-nil StatusError message = %q", got)
	}
}

func TestRetryableWaitStatusMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusNotFound, want: false},
		{status: http.StatusConflict, want: false},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
		{status: http.StatusBadGateway, want: true},
		{status: http.StatusServiceUnavailable, want: true},
		{status: http.StatusGatewayTimeout, want: true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			err := &StatusError{StatusCode: test.status}
			if got := isRetryableWaitError(err); got != test.want {
				t.Fatalf("isRetryableWaitError(%d) = %v, want %v", test.status, got, test.want)
			}
		})
	}
}

func TestRetryableWaitTransportErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "EOF", err: io.EOF, want: true},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "closed pipe", err: io.ErrClosedPipe, want: true},
		{name: "closed network", err: net.ErrClosed, want: true},
		{name: "network operation", err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}, want: true},
		{name: "wrapped EOF", err: fmt.Errorf("request: %w", io.EOF), want: true},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "context deadline", err: context.DeadlineExceeded, want: false},
		{name: "permanent application error", err: errors.New("invalid response"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryableWaitError(test.err); got != test.want {
				t.Fatalf("isRetryableWaitError(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestResolveRelativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		link string
		want string
	}{
		{name: "relative", link: "/v1/pods?page=2", want: "/v1/pods?page=2"},
		{name: "absolute", link: "https://steve.example/v1/pods?page=2", want: "/v1/pods?page=2"},
		{name: "absolute no query", link: "http://steve.example/v1/schemas", want: "/v1/schemas"},
		{name: "invalid absolute", link: "http://[::1", want: "http://[::1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveRelativePath(test.link); got != test.want {
				t.Fatalf("ResolveRelativePath(%q) = %q, want %q", test.link, got, test.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// Keep file writes behind one small helper so tests remain easy to audit.
func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
