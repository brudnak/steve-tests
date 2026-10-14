// Package config loads and validates runtime settings for the Steve test harness.
package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	EnvURL                    = "STEVE_URL"
	EnvToken                  = "STEVE_TOKEN"
	EnvCAFile                 = "STEVE_CA_FILE"
	EnvInsecure               = "STEVE_INSECURE"
	EnvInsecureSkipVerify     = "STEVE_INSECURE_SKIP_VERIFY"
	EnvRequestTimeout         = "STEVE_REQUEST_TIMEOUT"
	EnvWaitTimeout            = "STEVE_WAIT_TIMEOUT"
	EnvPollInterval           = "STEVE_POLL_INTERVAL"
	EnvVerbose                = "STEVE_VERBOSE"
	EnvConfigPath             = "STEVE_CONFIG"
	EnvExpectedCacheMode      = "STEVE_EXPECT_CACHE_MODE"
	EnvAllowMutations         = "STEVE_TEST_ALLOW_MUTATIONS"
	EnvAllowWorkloads         = "STEVE_TEST_ALLOW_WORKLOADS"
	EnvAllowClusterScopedCRDs = "STEVE_TEST_ALLOW_CRDS"
)

// Config is the complete connection and test-safety configuration.
//
// ExplicitTarget is true only when a repository config file or STEVE_URL was
// supplied. Integration tests use it to skip instead of accidentally calling a
// developer's localhost when no target was requested.
type Config struct {
	URL               string `yaml:"url"`
	Token             string `yaml:"token"`
	CAFile            string `yaml:"caFile"`
	Insecure          bool   `yaml:"insecure"`
	RequestTimeout    string `yaml:"requestTimeout"`
	WaitTimeout       string `yaml:"waitTimeout"`
	PollInterval      string `yaml:"pollInterval"`
	Verbose           bool   `yaml:"verbose"`
	ExpectedCacheMode string `yaml:"expectedCacheMode"`

	ExplicitTarget         bool   `yaml:"-"`
	ConfigPath             string `yaml:"-"`
	AllowMutations         bool   `yaml:"-"`
	AllowWorkloads         bool   `yaml:"-"`
	AllowClusterScopedCRDs bool   `yaml:"-"`
}

// Load applies defaults, an optional repository-local config file, and then
// environment variables. Environment variables have the highest precedence.
func Load() (*Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	return loadFrom(workingDir, os.LookupEnv)
}

func loadFrom(startDir string, lookupEnv func(string) (string, bool)) (*Config, error) {
	cfg := Default()

	configPath, pathWasExplicit, err := resolveConfigPath(startDir, lookupEnv)
	if err != nil {
		return nil, err
	}
	if configPath != "" {
		if err := decodeConfigFile(configPath, cfg); err != nil {
			return nil, err
		}
		cfg.ConfigPath = configPath
		cfg.ExplicitTarget = true
	}

	urlWasExplicit, err := applyEnv(cfg, lookupEnv)
	if err != nil {
		return nil, err
	}
	if pathWasExplicit || urlWasExplicit {
		cfg.ExplicitTarget = true
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func resolveConfigPath(startDir string, lookupEnv func(string) (string, bool)) (string, bool, error) {
	if raw, ok := lookupEnv(EnvConfigPath); ok {
		path := strings.TrimSpace(raw)
		if path == "" {
			return "", true, fmt.Errorf("%s is set but empty", EnvConfigPath)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(startDir, path)
		}
		return filepath.Clean(path), true, nil
	}

	path, err := findConfigPathFrom(startDir, "steve-config.yaml")
	return path, false, err
}

// findConfigPathFrom searches up to and including the nearest Go module root.
// It deliberately does not continue into parent repositories, where an
// unrelated steve-config.yaml could otherwise hijack this test run.
func findConfigPathFrom(startDir string, name string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolve config search directory: %w", err)
	}

	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat config file %s: %w", candidate, err)
		}

		moduleFile := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(moduleFile); err == nil {
			return "", nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat module file %s: %w", moduleFile, err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func decodeConfigFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("parse config file %s: multiple YAML documents are not supported", path)
		}
		return fmt.Errorf("parse config file %s: %w", path, err)
	}
	return nil
}

// Default returns safe connection defaults. The target is not considered
// explicit until a config file or STEVE_URL is supplied.
func Default() *Config {
	return &Config{
		URL:            "http://localhost:8080",
		RequestTimeout: "30s",
		WaitTimeout:    "30s",
		PollInterval:   "200ms",
	}
}

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("Steve config is nil")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("invalid Steve URL syntax")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid Steve URL: scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("invalid Steve URL: host is required")
	}
	if parsed.User != nil {
		return fmt.Errorf("invalid Steve URL: user info is not allowed")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(c.URL, "#") {
		return fmt.Errorf("invalid Steve URL: query strings and fragments are not allowed")
	}
	normalizedURL := strings.TrimRight(c.URL, "/")

	for _, field := range []struct {
		name string
		raw  string
	}{
		{name: "requestTimeout", raw: c.RequestTimeout},
		{name: "waitTimeout", raw: c.WaitTimeout},
		{name: "pollInterval", raw: c.PollInterval},
	} {
		duration, err := time.ParseDuration(field.raw)
		if err != nil {
			return fmt.Errorf("invalid %s %q: %w", field.name, field.raw, err)
		}
		if duration <= 0 {
			return fmt.Errorf("invalid %s %q: duration must be greater than zero", field.name, field.raw)
		}
	}

	if c.CAFile != "" && c.Insecure {
		return fmt.Errorf("caFile and insecure=true are mutually exclusive")
	}
	if c.AllowWorkloads && !c.AllowMutations {
		return fmt.Errorf("%s=true requires %s=true", EnvAllowWorkloads, EnvAllowMutations)
	}
	if c.AllowClusterScopedCRDs && !c.AllowMutations {
		return fmt.Errorf("%s=true requires %s=true", EnvAllowClusterScopedCRDs, EnvAllowMutations)
	}
	switch c.ExpectedCacheMode {
	case "", "sql", "non-sql":
	default:
		return fmt.Errorf("expectedCacheMode must be sql or non-sql, or empty for automatic detection")
	}
	c.URL = normalizedURL
	return nil
}

func (c *Config) RequestTimeoutDuration() time.Duration {
	if c == nil {
		return 30 * time.Second
	}
	return parseDurationOr(c.RequestTimeout, 30*time.Second)
}

func (c *Config) WaitTimeoutDuration() time.Duration {
	if c == nil {
		return 30 * time.Second
	}
	return parseDurationOr(c.WaitTimeout, 30*time.Second)
}

func (c *Config) PollIntervalDuration() time.Duration {
	if c == nil {
		return 200 * time.Millisecond
	}
	return parseDurationOr(c.PollInterval, 200*time.Millisecond)
}

func applyEnv(cfg *Config, lookupEnv func(string) (string, bool)) (bool, error) {
	urlWasExplicit := false
	if value, ok := lookupEnv(EnvURL); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			return false, fmt.Errorf("%s is set but empty", EnvURL)
		}
		cfg.URL = value
		urlWasExplicit = true
	}
	if value, ok := lookupEnv(EnvToken); ok {
		cfg.Token = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvCAFile); ok {
		cfg.CAFile = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvExpectedCacheMode); ok {
		cfg.ExpectedCacheMode = strings.TrimSpace(value)
	}

	if value, ok := lookupEnv(EnvInsecureSkipVerify); ok {
		parsed, err := parseBoolEnv(EnvInsecureSkipVerify, value)
		if err != nil {
			return false, err
		}
		cfg.Insecure = parsed
	} else if value, ok := lookupEnv(EnvInsecure); ok {
		parsed, err := parseBoolEnv(EnvInsecure, value)
		if err != nil {
			return false, err
		}
		cfg.Insecure = parsed
	}

	if value, ok := lookupEnv(EnvRequestTimeout); ok {
		cfg.RequestTimeout = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvWaitTimeout); ok {
		cfg.WaitTimeout = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvPollInterval); ok {
		cfg.PollInterval = strings.TrimSpace(value)
	}

	booleanValues := []struct {
		name   string
		target *bool
	}{
		{name: EnvVerbose, target: &cfg.Verbose},
		{name: EnvAllowMutations, target: &cfg.AllowMutations},
		{name: EnvAllowWorkloads, target: &cfg.AllowWorkloads},
		{name: EnvAllowClusterScopedCRDs, target: &cfg.AllowClusterScopedCRDs},
	}
	for _, item := range booleanValues {
		if value, ok := lookupEnv(item.name); ok {
			parsed, err := parseBoolEnv(item.name, value)
			if err != nil {
				return false, err
			}
			*item.target = parsed
		}
	}

	return urlWasExplicit, nil
}

func parseBoolEnv(name string, raw string) (bool, error) {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("invalid %s value %q: expected true or false", name, raw)
	}
	return value, nil
}

func parseDurationOr(raw string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
