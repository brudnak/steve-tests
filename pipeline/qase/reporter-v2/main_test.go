package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rancher/steve-tests/extensions/qase/testresult"
)

const results = `{"Action":"run","Package":"steve/tests/pods","Test":"TestPods"}
{"Action":"run","Package":"steve/tests/pods","Test":"TestPods/create"}
{"Action":"pass","Package":"steve/tests/pods","Test":"TestPods/create","Elapsed":0.4}
{"Action":"run","Package":"steve/tests/pods","Test":"TestPods/delete"}
{"Action":"output","Package":"steve/tests/pods","Test":"TestPods/delete","Output":"pods_test.go:9: delete failed\n"}
{"Action":"fail","Package":"steve/tests/pods","Test":"TestPods/delete","Elapsed":1}
{"Action":"fail","Package":"steve/tests/pods","Test":"TestPods","Elapsed":1.5}
{"Action":"run","Package":"steve/tests/secrets","Test":"TestSecrets"}
{"Action":"pass","Package":"steve/tests/secrets","Test":"TestSecrets","Elapsed":0.25}
{"Action":"run","Package":"steve/tests/jobs","Test":"TestJobs"}
{"Action":"output","Package":"steve/tests/jobs","Test":"TestJobs","Output":"needs non-SQL mode\n"}
{"Action":"skip","Package":"steve/tests/jobs","Test":"TestJobs","Elapsed":0}
{"Action":"run","Package":"steve/pkg/config","Test":"TestLoad"}
{"Action":"pass","Package":"steve/pkg/config","Test":"TestLoad","Elapsed":0}
`

const schema = `- projects: [RM]
  suite: Go Automation/Steve/all
  cases:
    - title: TestPods
      parameters:
        - title: cache
          values: [sql]
    - title: TestSecrets
    - title: TestJobs
    - title: TestNotRunHere
`

type fakeQase struct {
	mu            sync.Mutex
	cases         map[string]int64
	results       []map[string]any
	completed     bool
	createdRun    string
	duplicateCase string
	missingSuite  bool
	requests      int
}

func (f *fakeQase) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/suite/RM":
		entities := []map[string]any{}
		if !f.missingSuite {
			switch r.URL.Query().Get("search") {
			case "Go Automation":
				entities = append(entities, map[string]any{"id": 10, "title": "Go Automation", "parent_id": nil})
			case "Steve":
				entities = append(entities, map[string]any{"id": 20, "title": "Steve", "parent_id": 10})
			case "all":
				// An identically named suite under another parent is unrelated.
				entities = append(entities, map[string]any{"id": 40, "title": "all", "parent_id": 99}, map[string]any{"id": 30, "title": "all", "parent_id": 20})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "result": map[string]any{"entities": entities}})
	case r.Method == http.MethodGet && r.URL.Path == "/case/RM":
		search := r.URL.Query().Get("search")
		entities := []map[string]any{}
		// A partial-title match must not be mistaken for the case.
		entities = append(entities, map[string]any{"id": 999, "title": search + "Extended", "suite_id": 30})
		// A Wrangler case with the same Go test name must never receive Steve results.
		entities = append(entities, map[string]any{"id": 998, "title": search, "suite_id": 40})
		if id, ok := f.cases[search]; ok {
			entities = append(entities, map[string]any{"id": id, "title": search, "suite_id": 30})
		}
		if search == f.duplicateCase {
			entities = append(entities, map[string]any{"id": 997, "title": search, "suite_id": 30})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "result": map[string]any{"entities": entities}})
	case r.Method == http.MethodPost && r.URL.Path == "/result/RM/42":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.results = append(f.results, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "result": map[string]any{"case_id": body["case_id"], "hash": "x"}})
	case r.Method == http.MethodPost && r.URL.Path == "/run/RM":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.createdRun, _ = body["title"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "result": map[string]any{"id": 42}})
	case r.Method == http.MethodPost && r.URL.Path == "/run/RM/42/complete":
		f.completed = true
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true})
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func setup(t *testing.T, cases map[string]int64) (*fakeQase, string, string) {
	t.Helper()
	fake := &fakeQase{cases: cases}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	schemaDir := filepath.Join(dir, "schemas")
	if err := os.WriteFile(resultsPath, []byte(results), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(schemaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "steve_schemas.yaml"), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("QASE_API_URL", server.URL)
	t.Setenv("QASE_AUTOMATION_TOKEN", "test-token")
	t.Setenv("QASE_PROJECT_ID", "RM")
	t.Setenv("QASE_TEST_RUN_ID", "42")
	t.Setenv("TEST_RUN_NAME", "")
	t.Setenv("QASE_TEST_RUN_COMPLETE", "true")
	t.Setenv("QASE_SCHEMA_PREFIX", "")
	t.Setenv("QASE_CUSTOM_FIELD_FILTER", "")
	return fake, resultsPath, schemaDir
}

func TestRunReportsSchemaTestsOnly(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, map[string]int64{"TestPods": 1, "TestSecrets": 2, "TestJobs": 3})

	if err := run(context.Background(), resultsPath, schemaDir); err != nil {
		t.Fatal(err)
	}

	byCase := map[float64]map[string]any{}
	for _, r := range fake.results {
		byCase[r["case_id"].(float64)] = r
	}
	if len(fake.results) != 3 {
		t.Fatalf("reported %d results, want 3 (TestLoad is not in the schema): %v", len(fake.results), fake.results)
	}

	pods := byCase[1]
	if pods["status"] != "failed" || pods["time_ms"].(float64) != 1500 {
		t.Errorf("unexpected TestPods result: %v", pods)
	}
	if comment := pods["comment"].(string); !strings.Contains(comment, "1 passed, 1 failed, 0 skipped") || !strings.Contains(comment, "delete failed") {
		t.Errorf("TestPods comment is missing the subtest summary or output:\n%s", comment)
	}
	if !strings.Contains(pods["stacktrace"].(string), "delete failed") {
		t.Errorf("TestPods stacktrace is missing the failure: %v", pods["stacktrace"])
	}
	if params, _ := pods["param"].(map[string]any); params["cache"] != "sql" {
		t.Errorf("TestPods params = %v, want cache=sql", pods["param"])
	}

	if byCase[2]["status"] != "passed" || byCase[2]["stacktrace"] != nil {
		t.Errorf("unexpected TestSecrets result: %v", byCase[2])
	}
	if byCase[3]["status"] != "skipped" || !strings.Contains(byCase[3]["comment"].(string), "needs non-SQL mode") {
		t.Errorf("unexpected TestJobs result: %v", byCase[3])
	}
	if !fake.completed {
		t.Error("run was not completed although QASE_TEST_RUN_COMPLETE=true")
	}
}

func TestRunFailsWhenACaseIsMissingInQase(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, map[string]int64{"TestPods": 1, "TestSecrets": 2})

	err := run(context.Background(), resultsPath, schemaDir)
	if err == nil || !strings.Contains(err.Error(), "TestJobs") {
		t.Fatalf("expected an error naming TestJobs, got %v", err)
	}
	if len(fake.results) != 2 {
		t.Errorf("the other results should still be reported; got %d", len(fake.results))
	}
	if fake.completed {
		t.Error("a run with reporting errors must stay open")
	}
}

func TestRunCreatesNamedRun(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, map[string]int64{"TestPods": 1, "TestSecrets": 2, "TestJobs": 3})
	t.Setenv("QASE_TEST_RUN_ID", "")
	t.Setenv("TEST_RUN_NAME", "Steve_Tests - v0.10.9")

	if err := run(context.Background(), resultsPath, schemaDir); err != nil {
		t.Fatal(err)
	}
	if fake.createdRun != "Steve_Tests - v0.10.9" || len(fake.results) != 3 {
		t.Errorf("created run %q with %d results", fake.createdRun, len(fake.results))
	}
}

func TestRunWithoutRunIsANoop(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, nil)
	t.Setenv("QASE_TEST_RUN_ID", "")

	if err := run(context.Background(), resultsPath, schemaDir); err != nil {
		t.Fatal(err)
	}
	if len(fake.results) != 0 {
		t.Errorf("nothing should be reported without a run")
	}
}

func TestRunRejectsEmptySelectionBeforeCreatingRun(t *testing.T) {
	for _, testResults := range []string{
		"",
		`{"Action":"pass","Package":"steve/pkg/config","Test":"TestLoad"}`,
	} {
		t.Run(testResults, func(t *testing.T) {
			fake, resultsPath, schemaDir := setup(t, nil)
			t.Setenv("QASE_TEST_RUN_ID", "")
			t.Setenv("TEST_RUN_NAME", "must not be created")
			if err := os.WriteFile(resultsPath, []byte(testResults), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := run(context.Background(), resultsPath, schemaDir); err == nil || !strings.Contains(err.Error(), "no test results match") {
				t.Fatalf("expected an empty selection error, got %v", err)
			}
			if fake.requests != 0 || fake.createdRun != "" || fake.completed {
				t.Fatalf("empty selection made Qase requests: %+v", fake)
			}
		})
	}
}

func TestRunAllowsNonemptyPartialSelection(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, map[string]int64{"TestPods": 1})
	if err := os.WriteFile(resultsPath, []byte(`{"Action":"pass","Package":"steve/tests/pods","Test":"TestPods"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), resultsPath, schemaDir); err != nil {
		t.Fatal(err)
	}
	if len(fake.results) != 1 || !fake.completed {
		t.Fatalf("partial selection should report and complete: %+v", fake)
	}
}

func TestRunRejectsSchemaProjectMismatchBeforeCreatingRun(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, nil)
	t.Setenv("QASE_PROJECT_ID", "OTHER")
	t.Setenv("QASE_TEST_RUN_ID", "")
	t.Setenv("TEST_RUN_NAME", "must not be created")
	err := run(context.Background(), resultsPath, schemaDir)
	if err == nil || !strings.Contains(err.Error(), "not configured Qase project OTHER") {
		t.Fatalf("expected a project mismatch error, got %v", err)
	}
	if fake.requests != 0 {
		t.Fatalf("project mismatch made %d Qase requests", fake.requests)
	}
}

func TestRunRejectsDuplicateCasesWithinSuite(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, map[string]int64{"TestPods": 1, "TestSecrets": 2, "TestJobs": 3})
	fake.duplicateCase = "TestPods"
	err := run(context.Background(), resultsPath, schemaDir)
	if err == nil || !strings.Contains(err.Error(), "TestPods: 2 Qase cases match") {
		t.Fatalf("expected an ambiguous case error, got %v", err)
	}
	if len(fake.results) != 2 || fake.completed {
		t.Fatalf("duplicate case should be skipped and the run kept open: %+v", fake)
	}
	for _, result := range fake.results {
		if result["case_id"] == float64(1) || result["case_id"] == float64(997) {
			t.Fatalf("reported to an ambiguous case: %+v", result)
		}
	}
}

func TestRunDoesNotCreateMissingSuites(t *testing.T) {
	fake, resultsPath, schemaDir := setup(t, map[string]int64{"TestPods": 1, "TestSecrets": 2, "TestJobs": 3})
	fake.missingSuite = true
	err := run(context.Background(), resultsPath, schemaDir)
	if err == nil || !strings.Contains(err.Error(), "upload the schemas first") {
		t.Fatalf("expected a missing suite error, got %v", err)
	}
	if len(fake.results) != 0 || fake.completed {
		t.Fatalf("missing suite should leave the run open without results: %+v", fake)
	}
}

func TestCommentForInterruptedTest(t *testing.T) {
	comment := Comment(&testresult.GoTestResult{Name: "TestJobs", Package: "p", Status: "fail", Interrupted: true, Output: "panic: test timed out"})
	for _, want := range []string{"Result: failed", "never reported a result", "panic: test timed out"} {
		if !strings.Contains(comment, want) {
			t.Errorf("comment is missing %q:\n%s", want, comment)
		}
	}
}

func TestTruncateHeadKeepsTheEnd(t *testing.T) {
	out := truncateHead("line one\nline two\nline three\n", 12)
	if !strings.HasPrefix(out, "[earlier output truncated]\n") || !strings.HasSuffix(out, "line three\n") || strings.Contains(out, "one") {
		t.Errorf("unexpected truncation: %q", out)
	}
	if got := truncateHead("short", 100); got != "short" {
		t.Errorf("short input changed: %q", got)
	}
}
