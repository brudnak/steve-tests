package secrets

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
)

func getTestClient(t *testing.T) *client.Client {
	return actions.IntegrationClient(t)
}

// newSecretBody creates a v1 Secret using stringData for convenience (K8s base64-encodes on create).
// We only care about metadata for list/filter/sort scenarios.
func newSecretBody(ns string, name string, labels map[string]string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    actions.ManagedLabels(labels),
		},
		"type": "Opaque",
		"stringData": map[string]string{
			"example": name,
		},
	}
}
