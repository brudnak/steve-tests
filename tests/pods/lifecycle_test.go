package pods

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
)

// TestPodCRUDLifecycle keeps a mode-independent workload smoke test in the
// package. The SQL-only array and printer-field regressions can capability
// skip, but an explicitly enabled workload run should still prove that a Pod
// can be created, read, metadata-updated, filtered, conflict-checked, and
// deleted through Steve.
func TestPodCRUDLifecycle(t *testing.T) {
	tests := []struct {
		name, resourceType, prefix string
		body                       actions.ResourceBodyFactory
	}{
		{name: "pod", resourceType: "pod", prefix: "pod-lifecycle", body: newNeverRestartPodBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cl := getTestClient(t)
			actions.RunNamespacedCRUDLifecycle(t, cl, test.resourceType, test.prefix, test.body)
		})
	}
}
