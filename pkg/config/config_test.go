package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigLoadingScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "LoadFromUsesSafeDefaultsWithoutEnablingIntegration", run: testLoadFromUsesSafeDefaultsWithoutEnablingIntegration},
		{name: "LoadFromStopsConfigSearchAtModuleRoot", run: testLoadFromStopsConfigSearchAtModuleRoot},
		{name: "LoadFromAppliesStrictRepositoryConfigAndEnvironment", run: testLoadFromAppliesStrictRepositoryConfigAndEnvironment},
		{name: "LoadFromExplicitRelativeConfigPath", run: testLoadFromExplicitRelativeConfigPath},
		{name: "ConfigValidateNilAndFailedValidationAreNonDestructive", run: testConfigValidateNilAndFailedValidationAreNonDestructive},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func testLoadFromUsesSafeDefaultsWithoutEnablingIntegration(t *testing.T) {
	t.Parallel()

	root := newModule(t)
	cfg, err := loadFrom(root, envLookup(nil))
	if err != nil {
		t.Fatalf("loadFrom() returned error: %v", err)
	}
	if cfg.URL != "http://localhost:8080" {
		t.Fatalf("default URL = %q", cfg.URL)
	}
	if cfg.Insecure {
		t.Fatal("TLS verification should be enabled by default")
	}
	if cfg.ExplicitTarget {
		t.Fatal("defaults must not opt into integration tests")
	}
}

func testLoadFromStopsConfigSearchAtModuleRoot(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	mustMkdir(t, filepath.Join(root, "tests", "schemas"))
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.test/repo\n")
	mustWrite(t, filepath.Join(parent, "steve-config.yaml"), "this is: [broken\n")

	cfg, err := loadFrom(filepath.Join(root, "tests", "schemas"), envLookup(nil))
	if err != nil {
		t.Fatalf("loadFrom() crossed the module boundary: %v", err)
	}
	if cfg.ConfigPath != "" || cfg.ExplicitTarget {
		t.Fatalf("unexpected parent config selection: %#v", cfg)
	}
}

func testLoadFromAppliesStrictRepositoryConfigAndEnvironment(t *testing.T) {
	t.Parallel()

	root := newModule(t)
	child := filepath.Join(root, "tests", "configmaps")
	mustMkdir(t, child)
	configPath := filepath.Join(root, "steve-config.yaml")
	mustWrite(t, configPath, strings.Join([]string{
		"url: https://from-file.example/base",
		"insecure: false",
		"requestTimeout: 5s",
		"waitTimeout: 9s",
		"pollInterval: 50ms",
	}, "\n")+"\n")

	cfg, err := loadFrom(child, envLookup(map[string]string{
		EnvURL:            "https://from-env.example/steve/",
		EnvToken:          "token-abc:secret",
		EnvWaitTimeout:    "45s",
		EnvPollInterval:   "100ms",
		EnvAllowMutations: "true",
		EnvVerbose:        "true",
	}))
	if err != nil {
		t.Fatalf("loadFrom() returned error: %v", err)
	}
	if cfg.URL != "https://from-env.example/steve" {
		t.Fatalf("URL = %q", cfg.URL)
	}
	if cfg.Token != "token-abc:secret" {
		t.Fatalf("token override was not retained")
	}
	if cfg.RequestTimeoutDuration() != 5*time.Second {
		t.Fatalf("request timeout = %s", cfg.RequestTimeoutDuration())
	}
	if cfg.WaitTimeoutDuration() != 45*time.Second {
		t.Fatalf("wait timeout = %s", cfg.WaitTimeoutDuration())
	}
	if cfg.PollIntervalDuration() != 100*time.Millisecond {
		t.Fatalf("poll interval = %s", cfg.PollIntervalDuration())
	}
	if !cfg.ExplicitTarget || cfg.ConfigPath != configPath {
		t.Fatalf("target provenance = explicit:%v path:%q", cfg.ExplicitTarget, cfg.ConfigPath)
	}
	if !cfg.AllowMutations || !cfg.Verbose {
		t.Fatalf("boolean environment overrides were not applied: %#v", cfg)
	}
}

func testLoadFromExplicitRelativeConfigPath(t *testing.T) {
	t.Parallel()

	root := newModule(t)
	mustWrite(t, filepath.Join(root, "local.yaml"), "url: http://custom.example:1234\n")
	cfg, err := loadFrom(root, envLookup(map[string]string{EnvConfigPath: "local.yaml"}))
	if err != nil {
		t.Fatalf("loadFrom() returned error: %v", err)
	}
	if cfg.URL != "http://custom.example:1234" {
		t.Fatalf("URL = %q", cfg.URL)
	}
	if cfg.ConfigPath != filepath.Join(root, "local.yaml") {
		t.Fatalf("config path = %q", cfg.ConfigPath)
	}
}

func TestLoadFromRejectsInvalidConfigFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "unknown field", content: "url: http://localhost:8080\ntimeuot: 2s\n", wantErr: "field timeuot not found"},
		{name: "multiple documents", content: "url: http://localhost:8080\n---\nurl: http://other\n", wantErr: "multiple YAML documents"},
		{name: "malformed yaml", content: "url: [broken\n", wantErr: "parse config file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := newModule(t)
			mustWrite(t, filepath.Join(root, "steve-config.yaml"), test.content)
			_, err := loadFrom(root, envLookup(nil))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("loadFrom() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadFromRejectsInvalidEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "invalid cache mode", env: map[string]string{EnvExpectedCacheMode: "automatic"}, wantErr: "expectedCacheMode"},
		{name: "empty URL", env: map[string]string{EnvURL: "  "}, wantErr: "STEVE_URL is set but empty"},
		{name: "empty config path", env: map[string]string{EnvConfigPath: ""}, wantErr: "STEVE_CONFIG is set but empty"},
		{name: "invalid insecure", env: map[string]string{EnvInsecure: "sometimes"}, wantErr: "invalid STEVE_INSECURE"},
		{name: "invalid verbose", env: map[string]string{EnvVerbose: "yes please"}, wantErr: "invalid STEVE_VERBOSE"},
		{name: "invalid mutation", env: map[string]string{EnvAllowMutations: "enabled"}, wantErr: "invalid STEVE_TEST_ALLOW_MUTATIONS"},
		{name: "invalid duration", env: map[string]string{EnvRequestTimeout: "later"}, wantErr: "invalid requestTimeout"},
		{name: "zero duration", env: map[string]string{EnvWaitTimeout: "0s"}, wantErr: "duration must be greater than zero"},
		{name: "workload without mutation", env: map[string]string{EnvAllowWorkloads: "true"}, wantErr: "requires STEVE_TEST_ALLOW_MUTATIONS"},
		{name: "CRDs without mutation", env: map[string]string{EnvAllowClusterScopedCRDs: "true"}, wantErr: "requires STEVE_TEST_ALLOW_MUTATIONS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadFrom(newModule(t), envLookup(test.env))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("loadFrom() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestConfigValidateRejectsUnsafeOrAmbiguousURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "relative", mutate: func(c *Config) { c.URL = "/v1" }, wantErr: "scheme must be http or https"},
		{name: "missing host", mutate: func(c *Config) { c.URL = "https:///v1" }, wantErr: "host is required"},
		{name: "userinfo", mutate: func(c *Config) { c.URL = "https://user:pass@example.test" }, wantErr: "user info is not allowed"},
		{name: "query", mutate: func(c *Config) { c.URL = "https://example.test?token=nope" }, wantErr: "query strings and fragments"},
		{name: "empty query", mutate: func(c *Config) { c.URL = "https://example.test?" }, wantErr: "query strings and fragments"},
		{name: "fragment", mutate: func(c *Config) { c.URL = "https://example.test/#frag" }, wantErr: "query strings and fragments"},
		{name: "empty fragment", mutate: func(c *Config) { c.URL = "https://example.test/#" }, wantErr: "query strings and fragments"},
		{name: "CA plus insecure", mutate: func(c *Config) { c.CAFile = "ca.pem"; c.Insecure = true }, wantErr: "mutually exclusive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestConfigValidateDoesNotExposeURLCredentials(t *testing.T) {
	t.Parallel()
	const secret = "credential-that-must-not-appear"
	tests := []struct{ name, url string }{
		{"userinfo", "https://user:" + secret + "@example.test"},
		{"query", "https://example.test?token=" + secret},
		{"fragment", "https://example.test/#" + secret},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.URL = test.url
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() accepted URL containing credentials")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("Validate() error exposed URL credentials")
			}
		})
	}
}

func testConfigValidateNilAndFailedValidationAreNonDestructive(t *testing.T) {
	t.Parallel()

	var nilConfig *Config
	if err := nilConfig.Validate(); err == nil || !strings.Contains(err.Error(), "config is nil") {
		t.Fatalf("nil Validate() error = %v", err)
	}
	if got := nilConfig.RequestTimeoutDuration(); got != 30*time.Second {
		t.Fatalf("nil request timeout = %s", got)
	}
	if got := nilConfig.WaitTimeoutDuration(); got != 30*time.Second {
		t.Fatalf("nil wait timeout = %s", got)
	}
	if got := nilConfig.PollIntervalDuration(); got != 200*time.Millisecond {
		t.Fatalf("nil poll interval = %s", got)
	}

	cfg := Default()
	cfg.URL = "https://example.test/base/"
	cfg.WaitTimeout = "not-a-duration"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() should reject an invalid duration")
	}
	if cfg.URL != "https://example.test/base/" {
		t.Fatalf("failed validation mutated URL to %q", cfg.URL)
	}
}

func TestNewInsecureVariableTakesPrecedenceOverLegacyName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "new false overrides legacy true", env: map[string]string{EnvInsecure: "true", EnvInsecureSkipVerify: "false"}},
		{name: "new true overrides legacy false", env: map[string]string{EnvInsecure: "false", EnvInsecureSkipVerify: "true"}, want: true},
		{name: "legacy only", env: map[string]string{EnvInsecure: "true"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := loadFrom(newModule(t), envLookup(test.env))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Insecure != test.want {
				t.Fatalf("Insecure = %t, want %t", cfg.Insecure, test.want)
			}
		})
	}
}

func newModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.test/config\n")
	return root
}

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create directory %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
