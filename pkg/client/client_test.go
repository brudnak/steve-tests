package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/config"
)

func TestClientDiscoveryScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "DiscoverResolvesSchemaLinksAndHeaders", run: testDiscoverResolvesSchemaLinksAndHeaders},
		{name: "ResourceClientUsesNamespacedStevePath", run: testResourceClientUsesNamespacedStevePath},
		{name: "StatusErrorSupportsStatusMatching", run: testStatusErrorSupportsStatusMatching},
		{name: "WaitForDeleteReturnsAfterNotFound", run: testWaitForDeleteReturnsAfterNotFound},
		{name: "ClientSendsBearerTokenWhenConfigured", run: testClientSendsBearerTokenWhenConfigured},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func testDiscoverResolvesSchemaLinksAndHeaders(t *testing.T) {
	var sawAccept bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/json" {
			sawAccept = true
		}
		if r.URL.Path != "/v1/schemas" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{
					"id": "configmap",
					"links": map[string]string{
						"collection": serverURL(r) + "/v1/configmaps",
					},
					"attributes": map[string]any{
						"group":      "",
						"version":    "v1",
						"kind":       "ConfigMap",
						"namespaced": true,
						"verbs":      []string{"list"},
					},
				},
			},
		})
	}))
	defer server.Close()

	cl, err := Discover(&config.Config{
		URL:            server.URL,
		Insecure:       true,
		RequestTimeout: "5s",
		WaitTimeout:    "5s",
		PollInterval:   "10ms",
		// Verbose left false here so unit tests don't spam stdout with [steve] logs.
		// Set STEVE_VERBOSE=true (or config) + run against real Steve for debugging.
	})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if !sawAccept {
		t.Fatal("expected Discover request to ask for JSON")
	}

	schema, ok := cl.Schemas["configmap"]
	if !ok {
		t.Fatal("expected configmap schema to be discovered")
	}
	if schema.CollectionPath != "v1/configmaps" {
		t.Fatalf("expected collection path v1/configmaps, got %q", schema.CollectionPath)
	}
	if !schema.Namespaced {
		t.Fatal("expected configmap schema to be namespaced")
	}
	if schema.APIVersion != "v1" || schema.Kind != "ConfigMap" {
		t.Fatalf("unexpected schema metadata: %#v", schema)
	}
}

func testResourceClientUsesNamespacedStevePath(t *testing.T) {
	var sawListPath bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/schemas":
			writeJSON(t, w, map[string]any{
				"data": []map[string]any{
					{
						"id":    "configmap",
						"links": map[string]string{"collection": "/v1/configmaps"},
						"attributes": map[string]any{
							"version":    "v1",
							"kind":       "ConfigMap",
							"namespaced": true,
							"verbs":      []string{"list"},
						},
					},
				},
			})
		case "/v1/configmaps/default":
			sawListPath = true
			if r.URL.RawQuery != "pagesize=1" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"type": "collection", "data": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cl, err := Discover(testConfig(server.URL))
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if _, err := cl.Resource("configmap").Namespace("default").List("pagesize=1"); err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if !sawListPath {
		t.Fatal("expected namespaced list path to be requested")
	}
}

func testStatusErrorSupportsStatusMatching(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/schemas":
			writeJSON(t, w, map[string]any{
				"data": []map[string]any{
					{
						"id":    "configmap",
						"links": map[string]string{"collection": "/v1/configmaps"},
						"attributes": map[string]any{
							"version":    "v1",
							"kind":       "ConfigMap",
							"namespaced": true,
						},
					},
				},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	cl, err := Discover(testConfig(server.URL))
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	_, err = cl.Resource("configmap").Namespace("default").Get("missing")
	if err == nil {
		t.Fatal("expected get to fail")
	}
	if !IsStatus(err, http.StatusNotFound) {
		t.Fatalf("expected typed not-found error, got %v", err)
	}
}

func testWaitForDeleteReturnsAfterNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/schemas":
			writeJSON(t, w, map[string]any{
				"data": []map[string]any{
					{
						"id":    "configmap",
						"links": map[string]string{"collection": "/v1/configmaps"},
						"attributes": map[string]any{
							"version":    "v1",
							"kind":       "ConfigMap",
							"namespaced": true,
						},
					},
				},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	cl, err := Discover(testConfig(server.URL))
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if err := cl.Resource("configmap").Namespace("default").WaitForDelete("missing", time.Second); err != nil {
		t.Fatalf("WaitForDelete returned error: %v", err)
	}
}

func testClientSendsBearerTokenWhenConfigured(t *testing.T) {
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		writeJSON(t, w, map[string]any{"data": []map[string]any{}})
	}))
	defer server.Close()

	cfg := testConfig(server.URL)
	cfg.Token = "token-abc:secret"
	if _, err := Discover(cfg); err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if sawAuth != "Bearer token-abc:secret" {
		t.Fatalf("expected Authorization header with bearer token, got %q", sawAuth)
	}
}

func testConfig(url string) *config.Config {
	return &config.Config{
		URL:            url,
		Insecure:       true,
		RequestTimeout: "5s",
		WaitTimeout:    "5s",
		PollInterval:   "10ms",
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("failed to write json: %v", err)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
