package actions

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/steve-tests/pkg/client"
)

func getNestedString(m map[string]any, keys ...string) string {
	var current any = m
	for _, key := range keys {
		currMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = currMap[key]
	}
	s, _ := current.(string)
	return s
}

func AssertCollectionLength(t *testing.T, coll *client.SteveCollection, expected int) {
	t.Helper()
	expect := CollectionExpectation{Length: &expected}
	if err := ValidateCollection(coll, expect); err != nil {
		t.Fatalf("Assertion failed: %v", err)
	}
}

func AssertCollectionOrder(t *testing.T, coll *client.SteveCollection, expectedNames []string) {
	t.Helper()
	if err := ValidateCollection(coll, CollectionExpectation{Order: expectedNames}); err != nil {
		t.Fatalf("Assertion failed: %v", err)
	}
}

// ValidateCollection checks a complete collection expectation without
// invoking testing.T. Polling callers can use it repeatedly until Steve's
// eventually consistent list cache converges.
func ValidateCollection(coll *client.SteveCollection, expect CollectionExpectation) error {
	if err := validateCollectionExpectation(expect); err != nil {
		return fmt.Errorf("invalid collection expectation: %w", err)
	}
	if coll == nil {
		return fmt.Errorf("collection is nil")
	}

	names, err := validatedCollectionNames(coll)
	if err != nil {
		return err
	}
	var failures []string
	if expect.Length != nil && len(coll.Data) != *expect.Length {
		failures = append(failures, fmt.Sprintf("expected collection length %d, got %d", *expect.Length, len(coll.Data)))
	}
	for _, expected := range expect.Contains {
		if !containsString(names, expected) {
			failures = append(failures, fmt.Sprintf("expected collection to contain %q", expected))
		}
	}
	for _, forbidden := range expect.NotContains {
		if containsString(names, forbidden) {
			failures = append(failures, fmt.Sprintf("expected collection not to contain %q", forbidden))
		}
	}
	if expect.Order != nil && !equalStrings(names, expect.Order) {
		failures = append(failures, fmt.Sprintf("expected exact collection order %v, got %v", expect.Order, names))
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s (actual names: %v)", strings.Join(failures, "; "), names)
	}
	return nil
}

func validateCollectionExpectation(expect CollectionExpectation) error {
	if !hasCollectionExpectation(expect) {
		return fmt.Errorf("at least one expectation is required")
	}
	if expect.Length != nil && *expect.Length < 0 {
		return fmt.Errorf("expected length cannot be negative: %d", *expect.Length)
	}

	contains, err := validateExpectedNames("contains", expect.Contains)
	if err != nil {
		return err
	}
	notContains, err := validateExpectedNames("notContains", expect.NotContains)
	if err != nil {
		return err
	}
	order, err := validateExpectedNames("order", expect.Order)
	if err != nil {
		return err
	}

	for name := range contains {
		if _, forbidden := notContains[name]; forbidden {
			return fmt.Errorf("resource %q cannot appear in both contains and notContains", name)
		}
	}
	if expect.Length != nil && len(contains) > *expect.Length {
		return fmt.Errorf("contains requires %d distinct resources but expected length is %d", len(contains), *expect.Length)
	}
	if expect.Order == nil {
		return nil
	}
	if expect.Length != nil && len(expect.Order) != *expect.Length {
		return fmt.Errorf("order contains %d resources but expected length is %d", len(expect.Order), *expect.Length)
	}
	for name := range contains {
		if _, present := order[name]; !present {
			return fmt.Errorf("resource %q is required by contains but absent from the exact order", name)
		}
	}
	for name := range notContains {
		if _, present := order[name]; present {
			return fmt.Errorf("resource %q is forbidden by notContains but present in the exact order", name)
		}
	}
	return nil
}

func validateExpectedNames(field string, names []string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(names))
	for index, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, fmt.Errorf("%s entry %d has an empty resource name", field, index+1)
		}
		if trimmed != name {
			return nil, fmt.Errorf("%s entry %d resource name %q has surrounding whitespace", field, index+1, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("%s has duplicate resource name %q", field, name)
		}
		seen[name] = struct{}{}
	}
	return seen, nil
}

func validatedCollectionNames(coll *client.SteveCollection) ([]string, error) {
	names := make([]string, 0, len(coll.Data))
	for index, item := range coll.Data {
		metadata, ok := item["metadata"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("collection item %d has no metadata object", index)
		}
		name, ok := metadata["name"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("collection item %d has no non-empty metadata.name string", index)
		}
		names = append(names, name)
	}
	return names, nil
}

func collectionNames(coll *client.SteveCollection) []string {
	if coll == nil {
		return nil
	}
	names := make([]string, 0, len(coll.Data))
	for _, item := range coll.Data {
		names = append(names, getNestedString(item, "metadata", "name"))
	}
	return names
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
