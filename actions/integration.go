package actions

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/rancher/steve-tests/pkg/client"
	"github.com/rancher/steve-tests/pkg/config"
)

type IntegrationSetup struct {
	Config    *config.Config
	Client    *client.Client
	CacheMode string
}

var (
	integrationOnce  sync.Once
	preflightLogOnce sync.Once
	integrationSetup *IntegrationSetup
	integrationErr   error
)

func SetupIntegration(t *testing.T) *IntegrationSetup {
	t.Helper()

	integrationOnce.Do(func() {
		cfg, err := config.Load()
		if err != nil {
			integrationErr = fmt.Errorf("failed to load config: %w", err)
			return
		}

		integrationSetup = &IntegrationSetup{Config: cfg}
		if !cfg.ExplicitTarget {
			return
		}

		cl, err := client.Discover(cfg)
		if err != nil {
			integrationErr = fmt.Errorf("failed to discover Steve schemas from %s: %w", cfg.URL, err)
			return
		}

		integrationSetup.Client = cl
		ctx, cancel := context.WithTimeout(t.Context(), cfg.WaitTimeoutDuration())
		defer cancel()
		mode, err := DetectCacheMode(ctx, cl, cfg.ExpectedCacheMode)
		if err != nil {
			integrationErr = err
			return
		}
		integrationSetup.CacheMode = mode
	})

	if integrationErr != nil {
		t.Fatalf("%v", integrationErr)
	}
	if integrationSetup == nil || integrationSetup.Config == nil {
		t.Fatal("integration setup completed without configuration")
	}
	if !integrationSetup.Config.ExplicitTarget {
		t.Skipf("Steve integration target is not configured; set %s or %s explicitly", config.EnvURL, config.EnvConfigPath)
	}
	if integrationSetup.Client == nil {
		t.Fatal("integration setup completed without a Steve client")
	}

	logIntegrationPreflight(t, integrationSetup)
	return integrationSetup
}

func IntegrationClient(t *testing.T) *client.Client {
	t.Helper()
	return SetupIntegration(t).Client
}

// RequireMutations skips the calling test unless resource mutations were
// explicitly enabled for the configured Steve target.
func RequireMutations(t *testing.T) *IntegrationSetup {
	t.Helper()
	setup := SetupIntegration(t)
	if !setup.Config.AllowMutations {
		t.Skipf("Steve resource mutations are disabled; set %s=true to enable this test", config.EnvAllowMutations)
	}
	return setup
}

// RequireWorkloads skips the calling test unless workload creation was
// explicitly enabled for the configured Steve target.
func RequireWorkloads(t *testing.T) *IntegrationSetup {
	t.Helper()
	setup := RequireMutations(t)
	if !setup.Config.AllowWorkloads {
		t.Skipf("Steve workload tests are disabled; set %s=true to enable this test", config.EnvAllowWorkloads)
	}
	return setup
}

// RequireClusterScopedCRDs skips the calling test unless cluster-scoped CRD
// mutations were explicitly enabled for the configured Steve target.
func RequireClusterScopedCRDs(t *testing.T) *IntegrationSetup {
	t.Helper()
	setup := RequireMutations(t)
	if !setup.Config.AllowClusterScopedCRDs {
		t.Skipf("Steve cluster-scoped CRD mutations are disabled; set %s=true to enable this test", config.EnvAllowClusterScopedCRDs)
	}
	return setup
}

func logIntegrationPreflight(t *testing.T, setup *IntegrationSetup) {
	t.Helper()
	preflightLogOnce.Do(func() {
		if setup == nil || setup.Config == nil || setup.Client == nil {
			return
		}

		t.Logf("Steve integration preflight: url=%s cacheMode=%s schemas=%d runID=%s mutations=%t workloads=%t clusterScopedCRDs=%t",
			setup.Config.URL,
			setup.CacheMode,
			len(setup.Client.Schemas),
			RunID(),
			setup.Config.AllowMutations,
			setup.Config.AllowWorkloads,
			setup.Config.AllowClusterScopedCRDs,
		)
	})
}
