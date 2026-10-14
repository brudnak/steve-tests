package secrets

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
)

func TestSecretCRUDLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, resourceType, prefix string
		body                       actions.ResourceBodyFactory
	}{
		{name: "secret", resourceType: "secret", prefix: "secret-lifecycle", body: newSecretBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cl := getTestClient(t)
			actions.RunNamespacedCRUDLifecycle(t, cl, test.resourceType, test.prefix, test.body)
		})
	}
}
