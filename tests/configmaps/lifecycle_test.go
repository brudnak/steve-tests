package configmaps

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
)

func TestConfigMapCRUDLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, resourceType, prefix string
		body                       actions.ResourceBodyFactory
	}{
		{name: "configmap", resourceType: "configmap", prefix: "configmap-lifecycle", body: newConfigMapBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cl := getTestClient(t)
			actions.RunNamespacedCRUDLifecycle(t, cl, test.resourceType, test.prefix, test.body)
		})
	}
}
