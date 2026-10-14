package deployments

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
)

// Each row owns a namespace. A case label also excludes controller-created resources.
type collectionSeed struct {
	name   string
	labels map[string]string
}

func TestDeploymentsCollectionScenarios(t *testing.T) {
	t.Parallel()
	cl := getTestClient(t)
	tests := []struct {
		name  string
		seeds []collectionSeed
		query string
		want  actions.CollectionExpectation
	}{
		{name: "deployment label filter with reverse name sort and pagesize",
			seeds: []collectionSeed{
				{name: "dep-zeta", labels: map[string]string{"steve-test-poc": "include"}},
				{name: "dep-alpha", labels: map[string]string{"steve-test-poc": "include"}},
				{name: "dep-omega", labels: map[string]string{"steve-test-poc": "include"}},
				{name: "dep-skip", labels: map[string]string{"steve-test-poc": "ignore"}},
			},
			query: "filter=metadata.labels.steve-test-poc=include&sort=-metadata.name&pagesize=2",
			want:  actions.CollectionExpectation{Order: []string{"dep-zeta", "dep-omega"}},
		},
		{name: "deployment name sort ascending",
			seeds: []collectionSeed{
				{name: "dep-c", labels: map[string]string{"steve-test-poc": "name-sort"}},
				{name: "dep-a", labels: map[string]string{"steve-test-poc": "name-sort"}},
				{name: "dep-b", labels: map[string]string{"steve-test-poc": "name-sort"}},
			},
			query: "filter=metadata.labels.steve-test-poc=name-sort&sort=metadata.name",
			want:  actions.CollectionExpectation{Order: []string{"dep-a", "dep-b", "dep-c"}},
		},
		{name: "deployment filter with no matches yields empty",
			seeds: []collectionSeed{
				{name: "dep-lonely", labels: map[string]string{"steve-test-poc": "present"}},
			},
			query: "filter=metadata.labels.steve-test-poc=absent",
			want:  actions.CollectionExpectation{Order: []string{}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ns := actions.CreateTestNamespace(t, cl)
			resources := cl.Resource("apps.deployment").Namespace(ns)
			for _, seed := range test.seeds {
				labels := map[string]string{actions.CaseLabel: "collection"}
				for key, value := range seed.labels {
					labels[key] = value
				}
				actions.CreateAndWait(t, resources, seed.name, newDeploymentBody(ns, seed.name, labels))
			}
			query := "filter=metadata.labels." + actions.CaseLabel + "=collection&" + test.query
			actions.WaitForCollection(t, resources, query, test.want)
		})
	}
}

func TestDeploymentCRUDLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, resourceType, prefix string
		body                       actions.ResourceBodyFactory
	}{
		{name: "apps.deployment", resourceType: "apps.deployment", prefix: "deployment-lifecycle", body: newDeploymentBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cl := getTestClient(t)
			actions.RunNamespacedCRUDLifecycle(t, cl, test.resourceType, test.prefix, test.body)
		})
	}
}
