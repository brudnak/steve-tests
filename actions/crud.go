package actions

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

const UpdatedLabel = "steve-test-updated"

// ResourceBodyFactory builds a namespaced fixture with the supplied labels.
type ResourceBodyFactory func(namespace string, name string, labels map[string]string) map[string]any

// RunNamespacedCRUDLifecycle checks create, get, update, filter, and delete
// through Steve's API. Each invocation owns its namespace and cleanup.
func RunNamespacedCRUDLifecycle(t *testing.T, cl *client.Client, resourceType string, casePrefix string, bodyFactory ResourceBodyFactory) {
	t.Helper()

	ns := CreateTestNamespace(t, cl)
	resources := cl.Resource(resourceType).Namespace(ns)
	name := UniqueName(t, casePrefix)
	caseID := name

	labels := map[string]string{
		CaseLabel: caseID,
	}
	CreateAndWait(t, resources, name, bodyFactory(ns, name, labels))

	t.Logf("Reading %q back through Steve...", name)
	fetched, err := resources.GetContext(t.Context(), name)
	if err != nil {
		t.Fatalf("failed to get %s %q after create: %v", resourceType, name, err)
	}
	assertMetadataString(t, fetched, "name", name)
	assertMetadataString(t, fetched, "namespace", ns)
	assertMetadataLabel(t, fetched, CaseLabel, caseID)

	t.Logf("Updating %q with label %s=true...", name, UpdatedLabel)
	applied := updateLabelWithConflictRetry(t, resources, resourceType, name, UpdatedLabel, "true")

	// applied carries the resourceVersion that was current when the update
	// succeeded, which is stale now, so replaying it must trip Kubernetes
	// optimistic concurrency through Steve as a 409.
	t.Logf("Replaying update of %q with a stale resourceVersion...", name)
	_, err = resources.UpdateContext(t.Context(), name, applied)
	if !client.IsStatus(err, http.StatusConflict) {
		t.Fatalf("expected stale-resourceVersion update of %s %q to return 409 Conflict, got: %v", resourceType, name, err)
	}

	query := fmt.Sprintf("filter=metadata.labels.%s=%s&filter=metadata.labels.%s=true", CaseLabel, caseID, UpdatedLabel)
	t.Logf("Waiting for %q to be filterable by its updated label...", name)
	if err := resources.WaitForListContainsContext(t.Context(), name, query, waitTimeout(resources)); err != nil {
		t.Fatalf("updated %s %q never matched filter %q: %v", resourceType, name, query, err)
	}

	t.Logf("Deleting %q...", name)
	if err := resources.DeleteContext(t.Context(), name); err != nil {
		t.Fatalf("failed to delete %s %q: %v", resourceType, name, err)
	}
	if err := resources.WaitForDeleteContext(t.Context(), name, waitTimeout(resources)); err != nil {
		t.Fatalf("%s %q never disappeared after delete: %v", resourceType, name, err)
	}
	if err := resources.WaitForListNotContainsContext(t.Context(), name, "", waitTimeout(resources)); err != nil {
		t.Fatalf("%s %q still present in collection after delete: %v", resourceType, name, err)
	}

	_, err = resources.GetContext(t.Context(), name)
	if !client.IsStatus(err, http.StatusNotFound, http.StatusGone) {
		t.Fatalf("expected GET of deleted %s %q to return 404/410, got: %v", resourceType, name, err)
	}
}

// updateLabelWithConflictRetry sets a label through a get-modify-put loop,
// retrying on 409. Controller-managed resources (e.g. deployments) get their
// resourceVersion bumped by status writes between our GET and PUT, so a
// conflict on a fresh read is normal, not a failure. Returns the request body
// that won, whose resourceVersion is stale once the update lands.
func updateLabelWithConflictRetry(t *testing.T, resources *client.ResourceClient, resourceType string, name string, labelKey string, labelValue string) map[string]any {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitTimeout(resources))
	defer cancel()

	attempts := 0
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			t.Fatalf("update of %s %q kept conflicting after %d attempt(s): %v (last conflict: %v)", resourceType, name, attempts, err, lastErr)
		}

		attempts++
		current, err := resources.GetContext(ctx, name)
		if err != nil {
			t.Fatalf("failed to get %s %q before update: %v", resourceType, name, err)
		}
		setMetadataLabel(t, current, labelKey, labelValue)

		updated, err := resources.UpdateContext(ctx, name, current)
		if err == nil {
			assertMetadataLabel(t, updated, labelKey, labelValue)
			return current
		}
		if ctx.Err() != nil {
			t.Fatalf("update of %s %q kept conflicting after %d attempt(s): %v (last conflict: %v)", resourceType, name, attempts, ctx.Err(), err)
		}
		if !client.IsStatus(err, http.StatusConflict) {
			t.Fatalf("failed to update %s %q: %v", resourceType, name, err)
		}
		lastErr = err
		t.Logf("Update of %q hit a resourceVersion conflict (attempt %d), refetching...", name, attempts)

		pollInterval := resources.PollInterval()
		if pollInterval <= 0 {
			pollInterval = 200 * time.Millisecond
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("update of %s %q kept conflicting after %d attempt(s): %v (last conflict: %v)", resourceType, name, attempts, ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func assertMetadataString(t *testing.T, resource map[string]any, key string, expected string) {
	t.Helper()
	actual := getNestedString(resource, "metadata", key)
	if actual != expected {
		t.Fatalf("Assertion failed: expected metadata.%s %q, got %q", key, expected, actual)
	}
}

func assertMetadataLabel(t *testing.T, resource map[string]any, key string, expected string) {
	t.Helper()
	actual := getNestedString(resource, "metadata", "labels", key)
	if actual != expected {
		t.Fatalf("Assertion failed: expected metadata.labels.%s %q, got %q", key, expected, actual)
	}
}

func setMetadataLabel(t *testing.T, resource map[string]any, key string, value string) {
	t.Helper()
	metadataValue := resource["metadata"]
	metadata, ok := metadataValue.(map[string]any)
	if !ok {
		t.Fatalf("resource metadata has type %T, want object", metadataValue)
	}
	labels, ok := metadata["labels"].(map[string]any)
	if !ok {
		labels = map[string]any{}
		metadata["labels"] = labels
	}
	labels[key] = value
}
