package indexedfields

import "fmt"

// HPAs target a nonexistent Deployment, so these fixtures create no workloads.
func hpa(minReplicas, maxReplicas int) map[string]any {
	return map[string]any{
		"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler",
		"spec": map[string]any{
			"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "steve-test-nonexistent"},
			"minReplicas":    minReplicas, "maxReplicas": maxReplicas,
			"metrics": []any{map[string]any{
				"type": "Resource",
				"resource": map[string]any{
					"name":   "cpu",
					"target": map[string]any{"type": "Utilization", "averageUtilization": 50},
				},
			}},
		},
	}
}

func configMap(keys int) map[string]any {
	data := make(map[string]string, keys)
	for i := 0; i < keys; i++ {
		data[fmt.Sprintf("key-%02d", i)] = "value"
	}
	return map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": data}
}
