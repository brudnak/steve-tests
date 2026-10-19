package testresult

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	actionRun    = "run"
	actionOutput = "output"
	actionPass   = "pass"
	actionFail   = "fail"
	actionSkip   = "skip"
)

type testState struct {
	status  string
	elapsed float64
	output  strings.Builder
}

type rootState struct {
	pkg      string
	name     string
	order    int
	tests    map[string]*testState
	subOrder []string
}

// Parse reads go test -json events and returns one result per top-level test,
// in the order the tests started. Subtest outcomes are folded into their
// top-level test, because Qase cases map to top-level tests.
func Parse(r io.Reader) ([]*GoTestResult, error) {
	roots := map[string]*rootState{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var event GoTestOutput
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if event.Test == "" {
			continue // package-level event
		}

		rootName, _, _ := strings.Cut(event.Test, "/")
		key := event.Package + "\x00" + rootName
		root, ok := roots[key]
		if !ok {
			root = &rootState{pkg: event.Package, name: rootName, order: len(roots), tests: map[string]*testState{}}
			roots[key] = root
		}
		test, ok := root.tests[event.Test]
		if !ok {
			test = &testState{}
			root.tests[event.Test] = test
			if event.Test != rootName {
				root.subOrder = append(root.subOrder, event.Test)
			}
		}

		switch event.Action {
		case actionOutput:
			test.output.WriteString(event.Output)
		case actionPass, actionFail, actionSkip:
			test.status = event.Action
			test.elapsed = event.Elapsed
			test.output.WriteString(event.Output)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	ordered := make([]*rootState, 0, len(roots))
	for _, root := range roots {
		ordered = append(ordered, root)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })

	results := make([]*GoTestResult, 0, len(ordered))
	for _, root := range ordered {
		results = append(results, root.result())
	}
	return results, nil
}

func (r *rootState) result() *GoTestResult {
	result := &GoTestResult{Name: r.name, Package: r.pkg}
	var output strings.Builder

	if top, ok := r.tests[r.name]; ok {
		result.Status = top.status
		result.Elapsed = top.elapsed
		output.WriteString(top.output.String())
	}
	if result.Status == "" {
		result.Status = actionFail
		result.Interrupted = true
	}

	// Passing subtests add noise; keep output for the ones that need a look.
	for _, name := range r.subOrder {
		sub := r.tests[name]
		status := sub.status
		if status == "" {
			status = actionFail
		}
		result.Subtests = append(result.Subtests, SubtestResult{Name: name, Status: status})
		if status != actionPass {
			output.WriteString(sub.output.String())
		}
	}

	result.Output = output.String()
	return result
}

// Counts returns how many subtests passed, failed, and skipped.
func (r *GoTestResult) Counts() (passed, failed, skipped int) {
	for _, sub := range r.Subtests {
		switch sub.Status {
		case actionPass:
			passed++
		case actionSkip:
			skipped++
		default:
			failed++
		}
	}
	return passed, failed, skipped
}
