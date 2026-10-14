package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

const (
	ManagedLabel = "steve-test.cattle.io/managed"
	RunIDLabel   = "steve-test.cattle.io/run-id"
	CaseLabel    = "steve-test-case"
)

var (
	runIDOnce sync.Once
	runID     string
)

func RunID() string {
	runIDOnce.Do(func() {
		runID = randomHex(5)
	})
	return runID
}

func ManagedLabels(extra map[string]string) map[string]string {
	labels := make(map[string]string, len(extra)+2)
	for key, value := range extra {
		labels[key] = value
	}
	labels[ManagedLabel] = "true"
	labels[RunIDLabel] = RunID()
	return labels
}

func UniqueName(t *testing.T, prefix string) string {
	t.Helper()
	suffix := randomHex(5)
	prefix = sanitizeName(prefix)
	maxPrefixLen := 63 - len(suffix) - 1
	if len(prefix) > maxPrefixLen {
		prefix = strings.Trim(prefix[:maxPrefixLen], "-")
	}
	return fmt.Sprintf("%s-%s", prefix, suffix)
}

func CreateAndWait(t *testing.T, rc *client.ResourceClient, name string, body map[string]any) map[string]any {
	t.Helper()
	if err := validateCreateBodyName(name, body); err != nil {
		t.Fatalf("invalid create body for %q: %v", name, err)
	}
	t.Logf("Creating %q...", name)
	created, err := rc.CreateContext(t.Context(), body)
	if err != nil {
		cleanupAmbiguousCreate(t, rc, name, err)
		t.Fatalf("failed to create %q: %v", name, err)
	}

	RegisterCleanup(t, rc, name)

	t.Logf("Waiting for %q to become readable...", name)
	if _, err := rc.WaitForGetContext(t.Context(), name, waitTimeout(rc)); err != nil {
		t.Fatalf("failed waiting for %q to become readable: %v", name, err)
	}
	t.Logf("Waiting for %q to appear in collection results...", name)
	if err := rc.WaitForListContainsContext(t.Context(), name, "", waitTimeout(rc)); err != nil {
		t.Fatalf("failed waiting for %q to appear in collection results: %v", name, err)
	}

	return created
}

func validateCreateBodyName(name string, body map[string]any) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("cleanup name must not be empty")
	}
	if body == nil {
		return fmt.Errorf("resource body is nil")
	}
	bodyName := getNestedString(body, "metadata", "name")
	if bodyName == "" {
		return fmt.Errorf("resource body has no non-empty metadata.name string")
	}
	if bodyName != name {
		return fmt.Errorf("resource body metadata.name %q does not match cleanup name %q", bodyName, name)
	}
	return nil
}

func RegisterCleanup(t *testing.T, rc *client.ResourceClient, name string) {
	t.Helper()
	t.Cleanup(func() {
		timeout := waitTimeout(rc)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		t.Logf("Cleaning up %q...", name)
		if err := rc.DeleteContext(ctx, name); err != nil {
			t.Errorf("cleanup failed to delete %q: %v", name, err)
			return
		}
		if err := rc.WaitForDeleteContext(ctx, name, timeout); err != nil {
			t.Errorf("cleanup failed waiting for %q deletion: %v", name, err)
		}
	})
}

// cleanupAmbiguousCreate handles the case where Steve or the network fails
// after Kubernetes may already have committed a POST. It deletes only objects
// carrying this process's ownership labels, so a conflict or name collision can
// never cause the harness to remove somebody else's resource.
func cleanupAmbiguousCreate(t *testing.T, rc *client.ResourceClient, name string, createErr error) {
	t.Helper()
	if !isAmbiguousCreateError(createErr) {
		return
	}

	timeout := waitTimeout(rc)
	lookupCtx, cancelLookup := context.WithTimeout(context.Background(), timeout)
	resource, err := rc.WaitForGetContext(lookupCtx, name, timeout)
	cancelLookup()
	if err != nil {
		t.Logf("Warning: could not determine whether failed create of %q committed: %v", name, err)
		return
	}
	if !resourceOwnedByCurrentRun(resource) {
		t.Logf("Warning: refusing to clean up %q after an ambiguous create failure because its ownership labels do not match this run", name)
		return
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), timeout)
	defer cancelCleanup()
	t.Logf("Cleaning up %q after an ambiguous create failure...", name)
	if err := rc.DeleteContext(cleanupCtx, name); err != nil {
		t.Logf("Warning: failed to delete %q after an ambiguous create failure: %v", name, err)
		return
	}
	if err := rc.WaitForDeleteContext(cleanupCtx, name, timeout); err != nil {
		t.Logf("Warning: failed waiting for %q deletion after an ambiguous create failure: %v", name, err)
	}
}

func isAmbiguousCreateError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *client.StatusError
	if !errors.As(err, &statusErr) || statusErr == nil {
		return true
	}
	return statusErr.StatusCode >= http.StatusInternalServerError ||
		statusErr.StatusCode == http.StatusRequestTimeout ||
		statusErr.StatusCode == http.StatusTooEarly ||
		statusErr.StatusCode == http.StatusTooManyRequests
}

func resourceOwnedByCurrentRun(resource map[string]any) bool {
	return getNestedString(resource, "metadata", "labels", ManagedLabel) == "true" &&
		getNestedString(resource, "metadata", "labels", RunIDLabel) == RunID()
}

func waitTimeout(rc *client.ResourceClient) time.Duration {
	if rc == nil {
		return 30 * time.Second
	}
	timeout := rc.WaitTimeout()
	if timeout <= 0 {
		return 30 * time.Second
	}
	return timeout
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("failed to generate random hex: %v", err))
	}
	return hex.EncodeToString(buf)
}

func sanitizeName(raw string) string {
	raw = strings.ToLower(raw)
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "steve-test"
	}
	return name
}
