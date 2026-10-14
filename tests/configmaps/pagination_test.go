package configmaps

import (
	"fmt"
	"sort"
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
)

// TestConfigMapsPaginationWalk checks full and partial pages of five
// ConfigMaps. Pages must be disjoint, complete, and globally sorted by name. The revision from page one is threaded into later pages
// so the walk is stable on Steve deployments that require it.
func TestConfigMapsPaginationWalk(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pageSize int
	}{
		{name: "partial last page", pageSize: 2},
		{name: "one item per page", pageSize: 1},
		{name: "single full page", pageSize: 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cl := getTestClient(t)
			ns := actions.CreateTestNamespace(t, cl)
			resources := cl.Resource("configmap").Namespace(ns)
			caseID := actions.UniqueName(t, "configmap-paging")

			seedNames := []string{"walk-01", "walk-02", "walk-03", "walk-04", "walk-05"}
			for _, name := range seedNames {
				labels := map[string]string{actions.CaseLabel: caseID}
				actions.CreateAndWait(t, resources, name, newConfigMapBody(ns, name, labels))
			}

			pageSize := test.pageSize
			pages := (len(seedNames) + pageSize - 1) / pageSize
			baseQuery := fmt.Sprintf("filter=metadata.labels.%s=%s&sort=metadata.name&pagesize=%d", actions.CaseLabel, caseID, pageSize)

			var walked []string
			revision := ""
			for page := 1; page <= pages; page++ {
				query := fmt.Sprintf("%s&page=%d", baseQuery, page)
				if revision != "" {
					query += "&revision=" + revision
				}

				t.Logf("Requesting page %d with query: %s", page, query)
				coll, err := resources.List(query)
				if err != nil {
					t.Fatalf("failed to list page %d: %v", page, err)
				}
				if revision == "" {
					revision = coll.Revision
				}
				if coll.Count != len(seedNames) {
					t.Fatalf("expected collection count %d on page %d (total matches across pages), got %d", len(seedNames), page, coll.Count)
				}

				expectedLen := pageSize
				if remaining := len(seedNames) - (page-1)*pageSize; remaining < expectedLen {
					expectedLen = remaining
				}
				actions.AssertCollectionLength(t, coll, expectedLen)
				walked = append(walked, collectionNames(coll)...)
			}

			if !sort.StringsAreSorted(walked) {
				t.Fatalf("expected walked pages to be globally sorted by name, got %v", walked)
			}

			seen := map[string]bool{}
			for _, name := range walked {
				if seen[name] {
					t.Fatalf("expected pages to be disjoint, but %q appeared twice in %v", name, walked)
				}
				seen[name] = true
			}
			for _, name := range seedNames {
				if !seen[name] {
					t.Fatalf("expected pagination walk to cover %q, walked %v", name, walked)
				}
			}
		})
	}
}

func collectionNames(coll *client.SteveCollection) []string {
	var names []string
	for _, item := range coll.Data {
		metadata, ok := item["metadata"].(map[string]any)
		if !ok {
			continue
		}
		if name, _ := metadata["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}
