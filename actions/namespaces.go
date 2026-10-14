package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/rancher/steve-tests/pkg/client"
)

func CreateNamespaceContext(ctx context.Context, c *client.Client, name string) error {
	if c == nil {
		return fmt.Errorf("Steve client is nil")
	}
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":   name,
			"labels": ManagedLabels(nil),
		},
	}
	_, err := c.Resource("namespace").CreateContext(ctx, body)
	return err
}

func DeleteNamespaceContext(ctx context.Context, c *client.Client, name string) error {
	if c == nil {
		return fmt.Errorf("Steve client is nil")
	}
	return c.Resource("namespace").DeleteContext(ctx, name)
}

func CreateTestNamespace(t *testing.T, c *client.Client) string {
	t.Helper()
	RequireMutations(t)
	if c == nil {
		t.Fatal("cannot create a test namespace with a nil Steve client")
	}

	name := UniqueName(t, "steve-test")
	namespaces := c.Resource("namespace")
	timeout := waitTimeout(namespaces)

	t.Logf("Creating temporary namespace %q...", name)
	err := CreateNamespaceContext(t.Context(), c, name)
	if err != nil {
		cleanupAmbiguousCreate(t, namespaces, name, err)
		t.Fatalf("failed to create namespace %s: %v", name, err)
	}

	RegisterCleanup(t, namespaces, name)

	t.Logf("Waiting for namespace %q to become ready in Steve...", name)
	if _, err := namespaces.WaitForGetContext(t.Context(), name, timeout); err != nil {
		t.Fatalf("namespace %s created but did not become ready/visible in Steve: %v", name, err)
	}
	t.Logf("Namespace %q is ready", name)
	return name
}
