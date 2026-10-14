package actions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

const (
	sqlModeProbeResource = "configmap"
	sqlModeProbeSort     = "spec.steveTestsSQLModeProbe"
)

type sqlModeLister interface {
	ListContext(context.Context, string) (*client.SteveCollection, error)
}

// RequireSQLMode uses a read-only list request to distinguish Steve's SQL
// list processor from its non-SQL processor. It skips the calling test when
// the target is known to be non-SQL and fails when the mode cannot be
// established reliably.
func RequireSQLMode(t *testing.T) *IntegrationSetup {
	t.Helper()
	return requireSQLMode(t, true)
}

// RequireNonSQLMode is the inverse capability gate. It is used for regressions
// in Steve's in-memory list path, and skips before fixtures are created when
// the target is SQL-backed.
func RequireNonSQLMode(t *testing.T) *IntegrationSetup {
	t.Helper()
	return requireSQLMode(t, false)
}

func requireSQLMode(t *testing.T, wantSQL bool) *IntegrationSetup {
	t.Helper()
	setup := SetupIntegration(t)

	sqlMode := setup.CacheMode == "sql"
	if wantSQL && !sqlMode {
		t.Skip("test requires Steve's SQL list processor; read-only capability probe identified non-SQL mode")
	}
	if !wantSQL && sqlMode {
		t.Skip("test requires Steve's non-SQL list processor; read-only capability probe identified SQL mode")
	}
	return setup
}

// DetectCacheMode verifies the observed API behavior against an optional mode
// selected by a runner. A mismatch fails before tests can create resources.
func DetectCacheMode(ctx context.Context, cl *client.Client, expected string) (string, error) {
	if expected != "" && expected != "sql" && expected != "non-sql" {
		return "", fmt.Errorf("expected cache mode must be sql or non-sql")
	}
	sql, err := ProbeSQLMode(ctx, cl)
	if err != nil {
		return "", fmt.Errorf("detect Steve cache mode: %w", err)
	}
	mode := "non-sql"
	if sql {
		mode = "sql"
	}
	if expected != "" && mode != expected {
		return "", fmt.Errorf("Steve cache mode is %s, expected %s; configure a separate target for that mode", mode, expected)
	}
	return mode, nil
}

// ProbeSQLMode detects SQL mode without creating or changing resources.
//
// Non-SQL Steve accepts sorting by arbitrary fields. SQL-backed Steve limits
// sorting to indexed fields and returns 422 for the deliberately unsupported
// probe field. This probe is independent of the SQL feature under test, so a
// later 422 from that feature remains a regression rather than a capability
// skip.
func ProbeSQLMode(ctx context.Context, cl *client.Client) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("SQL-mode probe context is nil")
	}
	if cl == nil {
		return false, fmt.Errorf("Steve client is nil")
	}
	if _, ok := cl.Schema(sqlModeProbeResource); !ok {
		return false, fmt.Errorf("Steve schema %q is required for the SQL-mode probe", sqlModeProbeResource)
	}

	resource := cl.Resource(sqlModeProbeResource)
	return probeSQLMode(ctx, resource, resource.PollInterval())
}

func probeSQLMode(ctx context.Context, lister sqlModeLister, pollInterval time.Duration) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("SQL-mode probe context is nil")
	}
	if lister == nil {
		return false, fmt.Errorf("SQL-mode probe lister is nil")
	}
	if pollInterval <= 0 {
		pollInterval = 200 * time.Millisecond
	}

	values := make(url.Values)
	values.Set("pagesize", "1")
	values.Set("sort", sqlModeProbeSort)
	probeQuery := values.Encode()

	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, sqlModeProbeTimeout(attempt-1, lastErr, err)
		}

		_, err := lister.ListContext(ctx, probeQuery)
		switch {
		case err == nil:
			return false, nil
		case isSQLProbeRejection(err):
			return true, nil
		case isSQLCacheSyncPending(err):
			lastErr = err
		default:
			return false, fmt.Errorf("read-only SQL-mode probe failed: %w", err)
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, sqlModeProbeTimeout(attempt, lastErr, ctx.Err())
		case <-timer.C:
		}
	}
}

// An unrelated 422 (for example from a proxy) must not identify SQL mode.
func isSQLProbeRejection(err error) bool {
	var statusErr *client.StatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != http.StatusUnprocessableEntity {
		return false
	}
	return strings.Contains(statusErr.Body, sqlModeProbeSort) &&
		strings.Contains(strings.ToLower(statusErr.Body), "column is invalid")
}

func isSQLCacheSyncPending(err error) bool {
	var statusErr *client.StatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != http.StatusInternalServerError {
		return false
	}
	body := strings.ToLower(statusErr.Body)
	return strings.Contains(body, "sql cache sync") ||
		strings.Contains(body, "cache context canceled")
}

func sqlModeProbeTimeout(attempts int, lastErr error, cause error) error {
	if lastErr == nil {
		return fmt.Errorf("read-only SQL-mode probe ended after %d attempt(s): %w", attempts, cause)
	}
	return fmt.Errorf("read-only SQL-mode probe ended after %d attempt(s); last error: %v: %w", attempts, lastErr, cause)
}
