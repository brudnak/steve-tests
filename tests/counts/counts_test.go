// Package counts validates Steve's /v1/counts resource, the aggregate the
// Rancher UI uses for sidebar resource counts.
package counts

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
)

func getTestClient(t *testing.T) *client.Client {
	return actions.IntegrationClient(t)
}

type countsCollection struct {
	Data []countsItem `json:"data"`
}

type countsItem struct {
	ID     string                   `json:"id"`
	Counts map[string]resourceCount `json:"counts"`
}

type resourceCount struct {
	Summary struct {
		Count int `json:"count"`
	} `json:"summary"`
	Namespaces map[string]struct {
		Count int `json:"count"`
	} `json:"namespaces"`
}

func TestCountsResourceReportsCoreTypes(t *testing.T) {
	cl := getTestClient(t)

	resp, body, err := cl.Get("v1/counts")
	if err != nil {
		t.Fatalf("failed to GET v1/counts: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected GET v1/counts to return 200, got %d: %s", resp.StatusCode, string(body))
	}

	var counts countsCollection
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatalf("failed to unmarshal v1/counts response: %v", err)
	}
	if len(counts.Data) == 0 {
		t.Fatal("expected v1/counts to return at least one count item")
	}

	item := counts.Data[0]
	// Every cluster has namespaces and configmaps (kube-system at minimum),
	// so these counts must be present and positive.
	for _, resourceType := range []string{"namespace", "configmap"} {
		t.Run(resourceType, func(t *testing.T) {
			count, ok := item.Counts[resourceType]
			if !ok {
				t.Fatalf("expected v1/counts to include a count for %q, got types: %v", resourceType, countTypes(item))
			}
			if count.Summary.Count < 1 {
				t.Fatalf("expected count for %q to be at least 1, got %d", resourceType, count.Summary.Count)
			}
		})
	}
}

// TestCountsReflectCreatedResource uses Steve's per-namespace count, not the
// cluster-wide total. The namespace is unique to this test, so unrelated
// cluster activity cannot satisfy the assertion.
func TestCountsReflectCreatedResource(t *testing.T) {
	tests := []struct {
		name  string
		names []string
	}{
		{name: "two configmaps", names: []string{"counts-probe-a", "counts-probe-b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cl := getTestClient(t)
			ns := actions.CreateTestNamespace(t, cl)
			resources := cl.Resource("configmap").Namespace(ns)
			before := namespaceConfigMapCount(t, cl, ns)

			names := test.names
			for _, name := range names {
				actions.CreateAndWait(t, resources, name, map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name":      name,
						"namespace": ns,
						"labels":    actions.ManagedLabels(nil),
					},
					"data": map[string]string{"value": name},
				})
			}

			afterCreate := waitForNamespaceConfigMapCount(t, cl, ns, func(count int) bool {
				return count >= before+len(names)
			})

			for _, name := range names {
				if err := resources.Delete(name); err != nil {
					t.Fatalf("delete configmap %q: %v", name, err)
				}
				if err := resources.WaitForDelete(name, resources.WaitTimeout()); err != nil {
					t.Fatalf("wait for configmap %q deletion: %v", name, err)
				}
			}
			waitForNamespaceConfigMapCount(t, cl, ns, func(count int) bool {
				return count <= afterCreate-len(names)
			})
		})
	}
}

func namespaceConfigMapCount(t *testing.T, cl *client.Client, namespace string) int {
	t.Helper()
	counts := fetchCounts(t, cl)
	return counts.Data[0].Counts["configmap"].Namespaces[namespace].Count
}

func fetchCounts(t *testing.T, cl *client.Client) countsCollection {
	t.Helper()
	resp, body, err := cl.Get("v1/counts")
	if err != nil {
		t.Fatalf("failed to GET v1/counts: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected GET v1/counts to return 200, got %d", resp.StatusCode)
	}
	var counts countsCollection
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatalf("failed to unmarshal v1/counts response: %v", err)
	}
	if len(counts.Data) == 0 {
		t.Fatal("expected v1/counts to return at least one count item")
	}
	return counts
}

func waitForNamespaceConfigMapCount(t *testing.T, cl *client.Client, namespace string, condition func(int) bool) int {
	t.Helper()
	deadline := time.Now().Add(cl.WaitTimeout)
	last := 0
	for {
		last = namespaceConfigMapCount(t, cl, namespace)
		if condition(last) {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for configmap count in namespace %q; last count was %d", namespace, last)
		}
		time.Sleep(cl.PollInterval)
	}
}

func countTypes(item countsItem) []string {
	types := make([]string, 0, len(item.Counts))
	for resourceType := range item.Counts {
		types = append(types, resourceType)
	}
	sort.Strings(types)
	return types
}
