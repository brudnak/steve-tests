package actions

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

// A child test process lets us check testing.T's final result after cleanup.
// Everything uses a fake HTTP transport; no Steve target is needed.
func TestCleanupFailureStatus(t *testing.T) {
	const childEnv = "STEVE_CLEANUP_UNIT_CHILD"
	if mode := os.Getenv(childEnv); mode != "" {
		cl := &client.Client{
			BaseURL: "https://steve.example.test",
			HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if err := req.Context().Err(); err != nil {
					return nil, err
				}
				if req.Method == http.MethodDelete {
					if mode == "delete refused" {
						return response(http.StatusForbidden, `{}`), nil
					}
					return response(http.StatusNoContent, ""), nil
				}
				if mode == "deletion never completes" {
					return response(http.StatusOK, `{"metadata":{"name":"fixture"}}`), nil
				}
				return response(http.StatusNotFound, `{}`), nil
			})},
			WaitTimeout:  20 * time.Millisecond,
			PollInterval: time.Millisecond,
			Schemas: map[string]client.SchemaInfo{
				"namespace": {ID: "namespace", CollectionPath: "v1/namespaces"},
			},
		}
		RegisterCleanup(t, cl.Resource("namespace"), "fixture")
		return
	}

	for _, test := range []struct {
		name, wantError string
	}{
		{name: "deleted"},
		{name: "delete refused", wantError: "cleanup failed to delete"},
		{name: "deletion never completes", wantError: "cleanup failed waiting"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCleanupFailureStatus$", "-test.v")
			cmd.Env = append(os.Environ(), childEnv+"="+test.name)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("cleanup child test exceeded deadline: %s", output)
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("successful cleanup failed: %v\n%s", err, output)
				}
				return
			}
			if err == nil || !strings.Contains(string(output), test.wantError) {
				t.Fatalf("want failed test with %q; exit=%v\n%s", test.wantError, err, output)
			}
		})
	}
}
