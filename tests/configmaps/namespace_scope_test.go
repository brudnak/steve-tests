package configmaps

import (
	"fmt"
	"testing"

	"github.com/rancher/steve-tests/actions"
)

// TestConfigMapsNamespaceScoping creates the same-labelled configmap in two
// namespaces and verifies that Steve's namespace-scoped collection path
// (/v1/configmaps/{ns}) only returns the resource from that namespace, while
// the cluster-wide collection returns both.
func TestConfigMapsNamespaceScoping(t *testing.T) {
	t.Parallel()
	cl := getTestClient(t)
	nsOne := actions.CreateTestNamespace(t, cl)
	nsTwo := actions.CreateTestNamespace(t, cl)
	caseID := actions.UniqueName(t, "configmap-scope")
	labels := map[string]string{actions.CaseLabel: caseID}

	resourcesOne := cl.Resource("configmap").Namespace(nsOne)
	resourcesTwo := cl.Resource("configmap").Namespace(nsTwo)
	const sharedName = "same-name"
	actions.CreateAndWait(t, resourcesOne, sharedName, newConfigMapBody(nsOne, sharedName, labels))
	actions.CreateAndWait(t, resourcesTwo, sharedName, newConfigMapBody(nsTwo, sharedName, labels))

	query := fmt.Sprintf("filter=metadata.labels.%s=%s", actions.CaseLabel, caseID)

	tests := []struct {
		name, namespace string
		wantNamespaces  []string
	}{
		{"first namespace", nsOne, []string{nsOne}},
		{"second namespace", nsTwo, []string{nsTwo}},
		{name: "all namespaces", wantNamespaces: []string{nsOne, nsTwo}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := cl.Resource("configmap")
			if test.namespace != "" {
				resources = resources.Namespace(test.namespace)
			}
			length := len(test.wantNamespaces)
			coll := actions.WaitForCollection(t, resources, query, actions.CollectionExpectation{Length: &length, Contains: []string{sharedName}})
			assertNamespaces(t, coll.Data, test.wantNamespaces)
		})
	}

}

func assertNamespaces(t *testing.T, items []map[string]any, expected []string) {
	t.Helper()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		metadata, _ := item["metadata"].(map[string]any)
		namespace, _ := metadata["namespace"].(string)
		seen[namespace] = true
	}
	for _, namespace := range expected {
		if !seen[namespace] {
			t.Fatalf("expected collection namespaces %v, got %v", expected, seen)
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("expected exactly namespaces %v, got %v", expected, seen)
	}
}
