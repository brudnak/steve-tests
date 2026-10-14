package configmaps

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
)

func getTestClient(t *testing.T) *client.Client {
	return actions.IntegrationClient(t)
}

func newConfigMapBody(ns string, name string, labels map[string]string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    actions.ManagedLabels(labels),
		},
		"data": map[string]string{
			"value": name,
		},
	}
}
