package configmaps

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/query"
)

// The non-SQL sorter must handle boolean values. SQL targets can reject this
// unindexed field, so cache mode is checked before creating fixtures.
func TestConfigMapsSortBooleanField(t *testing.T) {
	t.Parallel()
	cl := actions.RequireNonSQLMode(t).Client
	ns := actions.CreateTestNamespace(t, cl)
	resources := cl.Resource("configmap").Namespace(ns)
	caseID := actions.UniqueName(t, "boolean-sort")

	for _, seed := range []struct {
		name      string
		immutable bool
	}{
		{name: "immutable-false", immutable: false},
		{name: "immutable-true", immutable: true},
	} {
		body := newConfigMapBody(ns, seed.name, map[string]string{actions.CaseLabel: caseID})
		body["immutable"] = seed.immutable
		actions.CreateAndWait(t, resources, seed.name, body)
	}

	tests := []struct {
		name string
		sort string
		want []string
	}{
		{name: "ascending", sort: "immutable", want: []string{"immutable-false", "immutable-true"}},
		{name: "descending", sort: "-immutable", want: []string{"immutable-true", "immutable-false"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := (query.ListOptions{
				Filters: []string{query.Equal("metadata.labels."+actions.CaseLabel, caseID)},
				Sort:    []string{tc.sort},
			}).Encode()
			if err != nil {
				t.Fatalf("encode list query: %v", err)
			}

			coll, err := resources.List(values)
			if err != nil {
				t.Fatalf("list ConfigMaps sorted by %q: %v", tc.sort, err)
			}
			actions.AssertCollectionLength(t, coll, len(tc.want))
			actions.AssertCollectionOrder(t, coll, tc.want)
		})
	}
}
