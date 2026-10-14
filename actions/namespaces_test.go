package actions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/steve-tests/pkg/client"
)

func TestNamespaceScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "NamespaceContextHelpersRejectNilClient", run: testNamespaceContextHelpersRejectNilClient},
		{name: "NamespaceContextHelpersUseSteveCollectionAndResourcePaths", run: testNamespaceContextHelpersUseSteveCollectionAndResourcePaths},
		{name: "NamespaceContextHelperHonorsCancellation", run: testNamespaceContextHelperHonorsCancellation},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func testNamespaceContextHelpersRejectNilClient(t *testing.T) {
	t.Parallel()

	if err := CreateNamespaceContext(context.Background(), nil, "example"); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("expected nil client create error, got %v", err)
	}
	if err := DeleteNamespaceContext(context.Background(), nil, "example"); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("expected nil client delete error, got %v", err)
	}
}

func testNamespaceContextHelpersUseSteveCollectionAndResourcePaths(t *testing.T) {
	t.Parallel()

	var methods []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		switch request.Method {
		case http.MethodPost:
			if request.URL.Path != "/v1/namespaces" {
				t.Errorf("create path = %q, want /v1/namespaces", request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode namespace body: %v", err)
			}
			if got := getNestedString(body, "metadata", "name"); got != "example" {
				t.Errorf("namespace body name = %q, want example", got)
			}
			if got := getNestedString(body, "metadata", "labels", ManagedLabel); got != "true" {
				t.Errorf("namespace managed label = %q, want true", got)
			}
			return response(http.StatusCreated, `{"metadata":{"name":"example"}}`), nil
		case http.MethodDelete:
			if request.URL.Path != "/v1/namespaces/example" {
				t.Errorf("delete path = %q, want /v1/namespaces/example", request.URL.Path)
			}
			return response(http.StatusNoContent, ""), nil
		default:
			t.Errorf("unexpected method %s", request.Method)
			return response(http.StatusMethodNotAllowed, ""), nil
		}
	})

	steveClient := &client.Client{
		BaseURL:    "https://steve.example.test",
		HTTPClient: &http.Client{Transport: transport},
		Schemas: map[string]client.SchemaInfo{
			"namespace": {
				ID:             "namespace",
				CollectionPath: "v1/namespaces",
			},
		},
	}
	if err := CreateNamespaceContext(t.Context(), steveClient, "example"); err != nil {
		t.Fatalf("CreateNamespaceContext returned error: %v", err)
	}
	if err := DeleteNamespaceContext(t.Context(), steveClient, "example"); err != nil {
		t.Fatalf("DeleteNamespaceContext returned error: %v", err)
	}

	want := []string{"POST /v1/namespaces", "DELETE /v1/namespaces/example"}
	if !equalStrings(methods, want) {
		t.Fatalf("request sequence = %v, want %v", methods, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testNamespaceContextHelperHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	steveClient := &client.Client{
		BaseURL:    "http://127.0.0.1:1",
		HTTPClient: http.DefaultClient,
		Schemas: map[string]client.SchemaInfo{
			"namespace": {ID: "namespace", CollectionPath: "v1/namespaces"},
		},
	}
	err := CreateNamespaceContext(ctx, steveClient, "example")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
