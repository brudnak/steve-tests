package actions

import (
	"strings"
	"testing"

	"github.com/rancher/steve-tests/pkg/client"
)

func TestValidateCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		collection *client.SteveCollection
		expect     CollectionExpectation
		wantErrors []string
	}{
		{
			name:       "explicit empty order matches empty collection",
			collection: namedCollection(),
			expect:     CollectionExpectation{Order: []string{}},
		},
		{
			name:       "explicit empty order rejects unexpected resources",
			collection: namedCollection("unexpected"),
			expect:     CollectionExpectation{Order: []string{}},
			wantErrors: []string{"expected exact collection order []"},
		},
		{
			name:       "empty order cannot also require a resource",
			collection: namedCollection(),
			expect:     CollectionExpectation{Order: []string{}, Contains: []string{"alpha"}},
			wantErrors: []string{"absent from the exact order"},
		},
		{
			name:       "complete expectation passes",
			collection: namedCollection("alpha", "bravo"),
			expect: CollectionExpectation{
				Length:      intPointer(2),
				Contains:    []string{"alpha", "bravo"},
				NotContains: []string{"charlie"},
				Order:       []string{"alpha", "bravo"},
			},
		},
		{
			name:       "nil collection",
			collection: nil,
			expect:     CollectionExpectation{Length: intPointer(0)},
			wantErrors: []string{"collection is nil"},
		},
		{
			name:       "missing expectation",
			collection: namedCollection("alpha"),
			expect:     CollectionExpectation{},
			wantErrors: []string{"invalid collection expectation", "at least one expectation is required"},
		},
		{
			name:       "negative expected length",
			collection: namedCollection("alpha"),
			expect:     CollectionExpectation{Length: intPointer(-1)},
			wantErrors: []string{"invalid collection expectation", "expected length cannot be negative"},
		},
		{
			name: "collection item without metadata",
			collection: &client.SteveCollection{Data: []map[string]any{
				{"id": "alpha"},
			}},
			expect:     CollectionExpectation{Length: intPointer(1)},
			wantErrors: []string{"collection item 0 has no metadata object"},
		},
		{
			name: "collection item without name",
			collection: &client.SteveCollection{Data: []map[string]any{
				{"metadata": map[string]any{"namespace": "default"}},
			}},
			expect:     CollectionExpectation{Length: intPointer(1)},
			wantErrors: []string{"collection item 0 has no non-empty metadata.name string"},
		},
		{
			name:       "length mismatch",
			collection: namedCollection("alpha"),
			expect:     CollectionExpectation{Length: intPointer(2)},
			wantErrors: []string{"expected collection length 2, got 1"},
		},
		{
			name:       "required item missing",
			collection: namedCollection("alpha"),
			expect:     CollectionExpectation{Contains: []string{"bravo"}},
			wantErrors: []string{`expected collection to contain "bravo"`},
		},
		{
			name:       "forbidden item present",
			collection: namedCollection("alpha"),
			expect:     CollectionExpectation{NotContains: []string{"alpha"}},
			wantErrors: []string{`expected collection not to contain "alpha"`},
		},
		{
			name:       "order rejects interleaved extra item",
			collection: namedCollection("alpha", "extra", "bravo"),
			expect:     CollectionExpectation{Order: []string{"alpha", "bravo"}},
			wantErrors: []string{"expected exact collection order [alpha bravo], got [alpha extra bravo]"},
		},
		{
			name:       "order rejects reversed items",
			collection: namedCollection("bravo", "alpha"),
			expect:     CollectionExpectation{Order: []string{"alpha", "bravo"}},
			wantErrors: []string{"expected exact collection order [alpha bravo], got [bravo alpha]"},
		},
		{
			name:       "reports every failed expectation",
			collection: namedCollection("alpha"),
			expect: CollectionExpectation{
				Length:      intPointer(3),
				Contains:    []string{"bravo"},
				NotContains: []string{"alpha"},
				Order:       []string{"bravo", "charlie", "delta"},
			},
			wantErrors: []string{
				"expected collection length 3, got 1",
				`expected collection to contain "bravo"`,
				`expected collection not to contain "alpha"`,
				"expected exact collection order [bravo charlie delta], got [alpha]",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateCollection(test.collection, test.expect)
			if len(test.wantErrors) == 0 {
				if err != nil {
					t.Fatalf("ValidateCollection returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected validation error containing %v, got nil", test.wantErrors)
			}
			for _, want := range test.wantErrors {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected error to contain %q, got %q", want, err)
				}
			}
		})
	}
}

func namedCollection(names ...string) *client.SteveCollection {
	collection := &client.SteveCollection{Data: make([]map[string]any, 0, len(names))}
	for _, name := range names {
		collection.Data = append(collection.Data, map[string]any{
			"metadata": map[string]any{"name": name},
		})
	}
	return collection
}
