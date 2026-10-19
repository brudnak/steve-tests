package testresult

import (
	"strings"
	"testing"
)

const sample = `{"Action":"start","Package":"example/pods"}
{"Action":"run","Package":"example/pods","Test":"TestPods"}
{"Action":"output","Package":"example/pods","Test":"TestPods","Output":"=== RUN   TestPods\n"}
{"Action":"run","Package":"example/pods","Test":"TestPods/create"}
{"Action":"output","Package":"example/pods","Test":"TestPods/create","Output":"create ok\n"}
{"Action":"pass","Package":"example/pods","Test":"TestPods/create","Elapsed":0.5}
{"Action":"run","Package":"example/pods","Test":"TestPods/delete"}
{"Action":"output","Package":"example/pods","Test":"TestPods/delete","Output":"pods_test.go:9: delete failed\n"}
{"Action":"fail","Package":"example/pods","Test":"TestPods/delete","Elapsed":1.25}
{"Action":"run","Package":"example/pods","Test":"TestPods/delete/again"}
{"Action":"output","Package":"example/pods","Test":"TestPods/delete/again","Output":"upstream bug still open\n"}
{"Action":"skip","Package":"example/pods","Test":"TestPods/delete/again","Elapsed":0}
{"Action":"fail","Package":"example/pods","Test":"TestPods","Elapsed":2}
{"Action":"run","Package":"example/secrets","Test":"TestSecrets"}
{"Action":"pass","Package":"example/secrets","Test":"TestSecrets","Elapsed":0.1}
{"Action":"run","Package":"example/jobs","Test":"TestJobs"}
{"Action":"run","Package":"example/jobs","Test":"TestJobs/wait"}
{"Action":"output","Package":"example/jobs","Test":"TestJobs/wait","Output":"panic: test timed out\n"}
{"Action":"fail","Package":"example/jobs","Elapsed":30}
`

func TestParse(t *testing.T) {
	results, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	pods, secrets, jobs := results[0], results[1], results[2]

	if pods.Name != "TestPods" || pods.Package != "example/pods" || pods.Status != "fail" || pods.Elapsed != 2 || pods.Interrupted {
		t.Errorf("unexpected pods result: %+v", pods)
	}
	if passed, failed, skipped := pods.Counts(); passed != 1 || failed != 1 || skipped != 1 {
		t.Errorf("pods counts = %d/%d/%d, want 1/1/1", passed, failed, skipped)
	}
	if strings.Contains(pods.Output, "create ok") {
		t.Errorf("output of a passing subtest should be left out:\n%s", pods.Output)
	}
	for _, want := range []string{"=== RUN   TestPods", "delete failed", "upstream bug still open"} {
		if !strings.Contains(pods.Output, want) {
			t.Errorf("output is missing %q:\n%s", want, pods.Output)
		}
	}

	if secrets.Status != "pass" || len(secrets.Subtests) != 0 {
		t.Errorf("unexpected secrets result: %+v", secrets)
	}

	if !jobs.Interrupted || jobs.Status != "fail" {
		t.Errorf("a test with no result should be an interrupted failure: %+v", jobs)
	}
	if len(jobs.Subtests) != 1 || jobs.Subtests[0].Status != "fail" || !strings.Contains(jobs.Output, "timed out") {
		t.Errorf("unexpected jobs subtests: %+v", jobs)
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	if _, err := Parse(strings.NewReader("{\"Action\":\"run\"}\nnot json\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("expected a line 2 parse error, got %v", err)
	}
}
