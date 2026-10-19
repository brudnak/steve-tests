package main

import (
	"testing"

	"github.com/rancher/steve-tests/extensions/qase"
)

func TestValidateProject(t *testing.T) {
	for _, tc := range []struct {
		name     string
		targets  [][]string
		wantFail bool
	}{
		{name: "matching suites", targets: [][]string{{"RM"}, {"RM"}}},
		{name: "different project", targets: [][]string{{"OTHER"}}, wantFail: true},
		{name: "later suite differs", targets: [][]string{{"RM"}, {"OTHER"}}, wantFail: true},
		{name: "additional project", targets: [][]string{{"RM", "OTHER"}}, wantFail: true},
		{name: "no target", targets: [][]string{{}}, wantFail: true},
		{name: "no schemas", wantFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schemas []qase.TestSuiteSchema
			for _, projects := range tc.targets {
				schemas = append(schemas, qase.TestSuiteSchema{Suite: "Go Automation/Steve/example", Projects: projects})
			}
			if err := validateProject(schemas, "RM"); (err != nil) != tc.wantFail {
				t.Fatalf("validateProject error = %v, want failure %v", err, tc.wantFail)
			}
		})
	}
}
