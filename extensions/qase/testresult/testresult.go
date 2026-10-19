// Package testresult holds the go test -json event shape used by the Qase
// reporter.
package testresult

// GoTestOutput is one event from go test -json, as written by gotestsum.
type GoTestOutput struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// SubtestResult is the outcome of one subtest beneath a reported test.
type SubtestResult struct {
	Name   string
	Status string
}

// GoTestResult is the outcome of one top-level Go test, including the
// subtests beneath it. Qase cases map to top-level tests.
type GoTestResult struct {
	Name     string
	Package  string
	Status   string
	Elapsed  float64
	Output   string
	Subtests []SubtestResult
	// Interrupted is true when the test started but never reported a result,
	// which happens when the package panics or hits its timeout.
	Interrupted bool
}
