package qase

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSchemaUsesQaseFieldNames(t *testing.T) {
	suites, err := ParseSchema([]byte(`
- projects: [RM]
  suite: Go Automation/Steve/pods
  cases:
    - title: TestPods
      priority: 4
      type: 8
      is_flaky: 1
      automation: 2
      custom_field:
        "15": TestPodsAlias
      parameters:
        - title: cache
          values: [sql]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) != 1 || len(suites[0].Cases) != 1 {
		t.Fatalf("unexpected suites: %+v", suites)
	}
	c := suites[0].Cases[0]
	if c.IsFlaky == nil || *c.IsFlaky != 1 {
		t.Errorf("is_flaky was not decoded: %v", c.IsFlaky)
	}
	if c.Priority == nil || *c.Priority != 4 || c.Automation == nil || *c.Automation != 2 {
		t.Errorf("priority or automation was not decoded: %+v", c)
	}
	if c.CustomField == nil || (*c.CustomField)["15"] != "TestPodsAlias" {
		t.Errorf("custom_field was not decoded: %v", c.CustomField)
	}
	if len(c.Parameters) != 1 || c.Parameters[0].ParameterSingle == nil || c.Parameters[0].ParameterSingle.Values[0] != "sql" {
		t.Errorf("parameters were not decoded: %+v", c.Parameters)
	}

	if found, err := GetTestSchema("TestPodsAlias", suites); err != nil || found.Title != "TestPods" {
		t.Errorf("lookup by automation test name failed: %v %v", found, err)
	}
}

func TestParseSchemaRejectsUnknownFields(t *testing.T) {
	_, err := ParseSchema([]byte("- suite: x\n  cases:\n    - title: T\n      isflaky: 0\n"))
	if err == nil || !strings.Contains(err.Error(), "isflaky") {
		t.Fatalf("expected an unknown field error, got %v", err)
	}
}

func TestGetSchemasByPrefix(t *testing.T) {
	dir := t.TempDir()
	write := func(name, suite string) {
		t.Helper()
		body := "- suite: " + suite + "\n  cases:\n    - title: T" + suite + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("steve_schemas.yaml", "steve")
	write("other_schemas.yaml", "other")
	write("notes.yaml", "ignored")

	all, err := GetSchemasByPrefix(dir, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("got %d suites (%v), want 2", len(all), err)
	}
	steve, err := GetSchemasByPrefix(dir, "steve")
	if err != nil || len(steve) != 1 || steve[0].Suite != "steve" {
		t.Fatalf("prefix filter failed: %+v %v", steve, err)
	}
}

func TestGetTestSchemaWithSuiteRetainsIdentityAndRejectsAmbiguity(t *testing.T) {
	suites, err := ParseSchema([]byte(`
- projects: [RM]
  suite: Go Automation/Steve/pods
  cases:
    - title: Pod lifecycle
      custom_field:
        "15": TestPods
`))
	if err != nil {
		t.Fatal(err)
	}
	testCase, suite, err := GetTestSchemaWithSuite("TestPods", suites)
	if err != nil || testCase.Title != "Pod lifecycle" || suite.Suite != "Go Automation/Steve/pods" || suite.Projects[0] != "RM" {
		t.Fatalf("schema identity was lost: case=%+v, suite=%+v, error=%v", testCase, suite, err)
	}
	if _, _, err := GetTestSchemaWithSuite("TestUnmapped", suites); !errors.Is(err, ErrTestSchemaNotFound) {
		t.Fatalf("expected an unmapped test error, got %v", err)
	}
	suites = append(suites, suites[0])
	suites[1].Suite = "Go Automation/Steve/other"
	if _, _, err := GetTestSchemaWithSuite("TestPods", suites); err == nil || errors.Is(err, ErrTestSchemaNotFound) || !strings.Contains(err.Error(), "multiple schema cases") {
		t.Fatalf("ambiguous schemas must fail instead of being silently skipped: %v", err)
	}
}
