package secrets

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
)

// Each row owns a namespace. A case label also excludes controller-created resources.
type collectionSeed struct {
	name   string
	labels map[string]string
}

func TestSecretsCollectionScenarios(t *testing.T) {
	t.Parallel()
	cl := getTestClient(t)
	tests := []struct {
		name  string
		seeds []collectionSeed
		query string
		want  actions.CollectionExpectation
	}{
		{name: "secret label filter + reverse name sort + pagesize",
			seeds: []collectionSeed{
				{name: "sec-zeta", labels: map[string]string{"steve-test-poc": "include"}},
				{name: "sec-alpha", labels: map[string]string{"steve-test-poc": "include"}},
				{name: "sec-omega", labels: map[string]string{"steve-test-poc": "include"}},
				{name: "sec-skip", labels: map[string]string{"steve-test-poc": "ignore"}},
			},
			query: "filter=metadata.labels.steve-test-poc=include&sort=-metadata.name&pagesize=2",
			want:  actions.CollectionExpectation{Order: []string{"sec-zeta", "sec-omega"}},
		},
		{name: "secret sort ascending name limited by pagesize",
			seeds: []collectionSeed{
				{name: "s-03", labels: map[string]string{"steve-test-poc": "paging"}},
				{name: "s-01", labels: map[string]string{"steve-test-poc": "paging"}},
				{name: "s-02", labels: map[string]string{"steve-test-poc": "paging"}},
			},
			query: "filter=metadata.labels.steve-test-poc=paging&sort=metadata.name&pagesize=2",
			want:  actions.CollectionExpectation{Order: []string{"s-01", "s-02"}},
		},
		{name: "secret filter no matches",
			seeds: []collectionSeed{
				{name: "lonely", labels: map[string]string{"steve-test-poc": "present"}},
			},
			query: "filter=metadata.labels.steve-test-poc=absent&sort=metadata.name",
			want:  actions.CollectionExpectation{Order: []string{}},
		},
		{name: "secret contains after multi-label filter",
			seeds: []collectionSeed{
				{name: "multi-a", labels: map[string]string{"steve-test-poc": "multi", "tier": "high"}},
				{name: "multi-b", labels: map[string]string{"steve-test-poc": "multi", "tier": "high"}},
				{name: "multi-low", labels: map[string]string{"steve-test-poc": "multi", "tier": "low"}},
			},
			query: "filter=metadata.labels.steve-test-poc=multi&filter=metadata.labels.tier=high",
			want:  actions.CollectionExpectation{Length: new(2), Contains: []string{"multi-a", "multi-b"}},
		},
		{name: "secret OR filter matches either tier value",
			seeds: []collectionSeed{
				{name: "or-high", labels: map[string]string{"steve-test-poc": "or-tier", "tier": "high"}},
				{name: "or-mid", labels: map[string]string{"steve-test-poc": "or-tier", "tier": "mid"}},
				{name: "or-low", labels: map[string]string{"steve-test-poc": "or-tier", "tier": "low"}},
			},
			query: "filter=metadata.labels.tier=high,metadata.labels.tier=low&sort=metadata.name",
			want:  actions.CollectionExpectation{Order: []string{"or-high", "or-low"}},
		},
		{name: "secret second page returns the remainder",
			seeds: []collectionSeed{
				{name: "page-01", labels: map[string]string{"steve-test-poc": "paging-2"}},
				{name: "page-02", labels: map[string]string{"steve-test-poc": "paging-2"}},
				{name: "page-03", labels: map[string]string{"steve-test-poc": "paging-2"}},
			},
			query: "filter=metadata.labels.steve-test-poc=paging-2&sort=metadata.name&pagesize=2&page=2",
			want:  actions.CollectionExpectation{Order: []string{"page-03"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ns := actions.CreateTestNamespace(t, cl)
			resources := cl.Resource("secret").Namespace(ns)
			for _, seed := range test.seeds {
				labels := map[string]string{actions.CaseLabel: "collection"}
				for key, value := range seed.labels {
					labels[key] = value
				}
				actions.CreateAndWait(t, resources, seed.name, newSecretBody(ns, seed.name, labels))
			}
			query := "filter=metadata.labels." + actions.CaseLabel + "=collection&" + test.query
			actions.WaitForCollection(t, resources, query, test.want)
		})
	}
}
