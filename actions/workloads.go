package actions

import (
	"os"
	"strings"
)

const (
	// EnvTestPodImage overrides the image used by fixtures that execute a
	// BusyBox-compatible shell command.
	EnvTestPodImage = "STEVE_TEST_POD_IMAGE"
	// DefaultWorkloadImage is intentionally pinned so repeated QA runs do not
	// drift when an upstream floating tag changes.
	DefaultWorkloadImage = "busybox:1.36"
)

// WorkloadImage returns the configured image for command-running Pod, Job,
// Deployment, and ReplicaSet fixtures.
func WorkloadImage() string {
	if image := strings.TrimSpace(os.Getenv(EnvTestPodImage)); image != "" {
		return image
	}
	return DefaultWorkloadImage
}
