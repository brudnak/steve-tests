package stevetests_test

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// GoLand normally gives a test ten minutes; the full suite needs more time.
func TestMain(m *testing.M) {
	flag.Parse()
	if allTestsSelected() && flag.Lookup("test.timeout").Value.String() == "10m0s" {
		if err := flag.Set("test.timeout", "30m"); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

func allTestsSelected() bool {
	pattern := flag.Lookup("test.run").Value.String()
	pattern = strings.ReplaceAll(pattern, `\Q`, "")
	pattern = strings.ReplaceAll(pattern, `\E`, "")
	return strings.TrimSuffix(strings.TrimPrefix(pattern, "^"), "$") == "TestAll"
}

// TestAll runs the full suite with mutations, workloads, and CRDs enabled.
// For individual results in GoLand, use the "All Steve tests" run configuration.
func TestAll(t *testing.T) {
	if !allTestsSelected() {
		t.Skip("select TestAll directly to run the full suite with all permissions")
	}
	if os.Getenv("STEVE_CONFIG") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("STEVE_CONFIG", filepath.Join(home, "steve-config.yaml"))
	}
	t.Setenv("STEVE_TEST_ALLOW_MUTATIONS", "true")
	t.Setenv("STEVE_TEST_ALLOW_WORKLOADS", "true")
	t.Setenv("STEVE_TEST_ALLOW_CRDS", "true")

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root")
	}
	// Use the SDK selected by GoLand; PATH may contain a different Go version.
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.CommandContext(t.Context(), goBinary, "test", "-count=1", "-p=4", "-parallel=4", "-timeout=20m", "-v", "./...")
	cmd.Dir = filepath.Dir(source)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("full suite failed: %v", err)
	}
}
