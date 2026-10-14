package pods

import (
	"testing"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
)

func getTestClient(t *testing.T) *client.Client {
	return actions.RequireWorkloads(t).Client
}

func newNeverRestartPodBody(ns string, name string, labels map[string]string) map[string]any {
	return newPodBody(ns, name, labels, []string{"sh", "-c", "sleep 3600"})
}

func newRestartOncePodBody(ns string, name string, labels map[string]string) map[string]any {
	body := newPodBody(ns, name, labels, []string{"sh", "-c", "if [ ! -f /state/restarted ]; then touch /state/restarted; exit 1; fi; sleep 3600"})
	spec := body["spec"].(map[string]any)
	spec["volumes"] = []map[string]any{
		{
			"name":     "state",
			"emptyDir": map[string]any{},
		},
	}
	containers := spec["containers"].([]map[string]any)
	containers[0]["volumeMounts"] = []map[string]any{
		{
			"name":      "state",
			"mountPath": "/state",
		},
	}
	return body
}

func newPodBody(ns string, name string, labels map[string]string, command []string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    actions.ManagedLabels(labels),
		},
		"spec": map[string]any{
			"restartPolicy":                 "Always",
			"terminationGracePeriodSeconds": int64(0),
			"containers": []map[string]any{
				{
					"name":            "main",
					"image":           actions.WorkloadImage(),
					"imagePullPolicy": "IfNotPresent",
					"command":         command,
				},
			},
		},
	}
}
