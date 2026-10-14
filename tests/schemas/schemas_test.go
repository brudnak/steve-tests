// Package schemas validates Steve's schema discovery surface itself: the
// /v1/schemas collection that every other suite in this harness depends on.
package schemas

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
)

func getTestClient(t *testing.T) *client.Client {
	return actions.IntegrationClient(t)
}

func TestSchemasContainCoreResources(t *testing.T) {
	cl := getTestClient(t)

	cases := []struct {
		id         string
		namespaced bool
		kind       string
	}{
		{id: "configmap", namespaced: true, kind: "ConfigMap"},
		{id: "secret", namespaced: true, kind: "Secret"},
		{id: "pod", namespaced: true, kind: "Pod"},
		{id: "namespace", namespaced: false, kind: "Namespace"},
		{id: "apps.deployment", namespaced: true, kind: "Deployment"},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			info, ok := cl.Schemas[tc.id]
			if !ok {
				t.Fatalf("expected /v1/schemas to expose schema %q", tc.id)
			}
			if info.Namespaced != tc.namespaced {
				t.Fatalf("expected schema %q namespaced=%v, got %v", tc.id, tc.namespaced, info.Namespaced)
			}
			if info.Kind != tc.kind {
				t.Fatalf("expected schema %q kind=%q, got %q", tc.id, tc.kind, info.Kind)
			}
			if info.CollectionPath == "" {
				t.Fatalf("expected schema %q to resolve a collection path", tc.id)
			}
			if !containsMethod(info.CollectionMethods, http.MethodGet) {
				t.Fatalf("expected schema %q collection methods to include GET, got %v", tc.id, info.CollectionMethods)
			}
			if !containsMethod(info.ResourceMethods, http.MethodGet) {
				t.Fatalf("expected schema %q resource methods to include GET, got %v", tc.id, info.ResourceMethods)
			}
			if info.Links["collection"] == "" {
				t.Fatalf("expected schema %q to include a collection link", tc.id)
			}
		})
	}
}

func TestSchemaByIDIsFetchable(t *testing.T) {
	tests := []struct{ name, id, kind string }{
		{name: "configmap", id: "configmap", kind: "ConfigMap"},
		{name: "secret", id: "secret", kind: "Secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cl := getTestClient(t)

			resp, body, err := cl.Get("v1/schemas/" + test.id)
			if err != nil {
				t.Fatalf("failed to GET v1/schemas/configmap: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected GET v1/schemas/configmap to return 200, got %d: %s", resp.StatusCode, string(body))
			}
			var schema struct {
				ID                string            `json:"id"`
				Type              string            `json:"type"`
				CollectionMethods []string          `json:"collectionMethods"`
				ResourceMethods   []string          `json:"resourceMethods"`
				Links             map[string]string `json:"links"`
				Attributes        struct {
					Kind       string `json:"kind"`
					Version    string `json:"version"`
					Namespaced bool   `json:"namespaced"`
				} `json:"attributes"`
			}
			if err := json.Unmarshal(body, &schema); err != nil {
				t.Fatalf("decode configmap schema: %v", err)
			}
			if schema.ID != test.id || schema.Type != "schema" {
				t.Fatalf("unexpected schema identity: id=%q type=%q", schema.ID, schema.Type)
			}
			if schema.Attributes.Kind != test.kind || schema.Attributes.Version != "v1" || !schema.Attributes.Namespaced {
				t.Fatalf("unexpected configmap attributes: %#v", schema.Attributes)
			}
			if !containsMethod(schema.CollectionMethods, http.MethodGet) || !containsMethod(schema.CollectionMethods, http.MethodPost) {
				t.Fatalf("unexpected collection methods: %v", schema.CollectionMethods)
			}
			for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				if !containsMethod(schema.ResourceMethods, method) {
					t.Fatalf("resource methods %v do not include %s", schema.ResourceMethods, method)
				}
			}
			if schema.Links["self"] == "" || schema.Links["collection"] == "" {
				t.Fatalf("schema links are incomplete: %v", schema.Links)
			}
		})
	}
}

func containsMethod(methods []string, expected string) bool {
	for _, method := range methods {
		if method == expected {
			return true
		}
	}
	return false
}

func TestUnknownSchemaReturnsNotFound(t *testing.T) {
	tests := []struct{ name, id string }{
		{name: "unknown core schema", id: "steve-test-definitely-not-a-resource"},
		{name: "unknown grouped schema", id: "steve-tests.invalid.missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cl := getTestClient(t)

			resp, _, err := cl.Get("v1/schemas/" + test.id)
			if err != nil {
				t.Fatalf("failed to GET unknown schema: %v", err)
			}
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("expected GET of unknown schema to return 404, got %d", resp.StatusCode)
			}
		})
	}
}
