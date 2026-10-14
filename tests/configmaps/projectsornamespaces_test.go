package configmaps

import (
	"fmt"
	"testing"

	"github.com/rancher/steve-tests/actions"
)

// TestConfigMapsProjectsOrNamespacesParam checks the namespace forms of Steve's
// projectsornamespaces parameter using only core Kubernetes resources.
func TestConfigMapsProjectsOrNamespacesParam(t *testing.T) {
	t.Parallel()
	cl := getTestClient(t)
	nsOne := actions.CreateTestNamespace(t, cl)
	nsTwo := actions.CreateTestNamespace(t, cl)
	caseID := actions.UniqueName(t, "configmap-pon")
	labels := map[string]string{actions.CaseLabel: caseID}

	actions.CreateAndWait(t, cl.Resource("configmap").Namespace(nsOne), "pon-one", newConfigMapBody(nsOne, "pon-one", labels))
	actions.CreateAndWait(t, cl.Resource("configmap").Namespace(nsTwo), "pon-two", newConfigMapBody(nsTwo, "pon-two", labels))

	clusterWide := cl.Resource("configmap")
	caseFilter := fmt.Sprintf("filter=metadata.labels.%s=%s", actions.CaseLabel, caseID)

	tests := []struct {
		name, query string
		want        []string
	}{
		{"include single namespace", "&projectsornamespaces=" + nsOne, []string{"pon-one"}},
		{"include both namespaces", "&projectsornamespaces=" + nsOne + "," + nsTwo, []string{"pon-one", "pon-two"}},
		{"negated form excludes a namespace", "&projectsornamespaces!=" + nsOne, []string{"pon-two"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions.WaitForCollection(t, clusterWide, caseFilter+test.query+"&sort=metadata.name", actions.CollectionExpectation{Order: test.want})
		})
	}
}
