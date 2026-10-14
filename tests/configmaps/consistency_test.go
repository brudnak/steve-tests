package configmaps

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/query"
)

// Once the SQL cache converges, repeated pages must not intermittently lose
// data or count. Fixtures are created here so this runs on standalone Steve.
func TestConfigMapsRepeatedFilteredPages(t *testing.T) {
	t.Parallel()
	cl := actions.RequireSQLMode(t).Client
	ns := actions.CreateTestNamespace(t, cl)
	caseID := actions.UniqueName(t, "repeated-pages")
	labels := map[string]string{actions.CaseLabel: caseID}
	want := []string{"reload-alpha", "reload-bravo"}
	for _, name := range want {
		actions.CreateAndWait(t, cl.Resource("configmap").Namespace(ns), name, newConfigMapBody(ns, name, labels))
	}

	// Exercise the cluster-wide list path without relying on ambient resources.
	resources := cl.Resource("configmap")
	absent := make([]string, 24)
	for i := range absent {
		absent[i] = fmt.Sprintf("%s-absent-%02d", ns, i)
	}
	notIn, err := query.NotIn("metadata.namespace", absent...)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"filter":   {"metadata.labels." + actions.CaseLabel + "=" + caseID, notIn},
		"sort":     {"metadata.name"},
		"page":     {"1"},
		"pagesize": {"100"},
		"exclude":  {"metadata.managedFields"},
	}
	rawQuery := values.Encode()
	actions.WaitForCollection(t, resources, rawQuery, actions.CollectionExpectation{Order: want})
	ctx, cancel := context.WithTimeout(t.Context(), cl.WaitTimeout)
	defer cancel()
	for reload := 1; reload <= 10; reload++ {
		// Do not retry these assertions: a transient empty page is the regression.
		collection, err := resources.ListContext(ctx, rawQuery)
		if err != nil {
			t.Fatalf("reload %d: %v", reload, err)
		}
		if err := actions.ValidateCollection(collection, actions.CollectionExpectation{Order: want}); err != nil {
			t.Fatalf("reload %d: %v", reload, err)
		}
		if collection.Count != len(want) {
			t.Fatalf("reload %d: count = %d, want %d", reload, collection.Count, len(want))
		}
	}
}
