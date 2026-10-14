package actions

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

func TestResourceOwnershipScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "ManagedLabelsCopiesInputAndOwnsHarnessLabels", run: testManagedLabelsCopiesInputAndOwnsHarnessLabels},
		{name: "RunIDIsStableHex", run: testRunIDIsStableHex},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func testManagedLabelsCopiesInputAndOwnsHarnessLabels(t *testing.T) {
	t.Parallel()

	extra := map[string]string{
		"team":       "qa",
		CaseLabel:    "case-a",
		ManagedLabel: "false",
		RunIDLabel:   "caller-value",
	}
	labels := ManagedLabels(extra)

	if extra[ManagedLabel] != "false" || extra[RunIDLabel] != "caller-value" {
		t.Fatalf("ManagedLabels mutated its input: %v", extra)
	}
	if labels["team"] != "qa" || labels[CaseLabel] != "case-a" {
		t.Fatalf("ManagedLabels dropped caller labels: %v", labels)
	}
	if labels[ManagedLabel] != "true" {
		t.Fatalf("expected %s=true, got %q", ManagedLabel, labels[ManagedLabel])
	}
	if labels[RunIDLabel] != RunID() || labels[RunIDLabel] == "caller-value" {
		t.Fatalf("expected harness-owned run ID, got %q", labels[RunIDLabel])
	}

	labels["team"] = "changed"
	labels["new"] = "value"
	if extra["team"] != "qa" {
		t.Fatalf("mutating returned labels changed input: %v", extra)
	}
	if _, exists := extra["new"]; exists {
		t.Fatalf("new returned label leaked into input: %v", extra)
	}
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "lowercase", raw: "Already-valid", want: "already-valid"},
		{name: "spaces and punctuation", raw: " ConfigMap / Scenario!! ", want: "configmap-scenario"},
		{name: "collapse separators", raw: "one___two---three", want: "one-two-three"},
		{name: "all invalid", raw: "___", want: "steve-test"},
		{name: "unicode separators", raw: "déjà vu", want: "d-j-vu"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeName(test.raw); got != test.want {
				t.Fatalf("sanitizeName(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestUniqueNameProducesDNSLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{name: "normal", prefix: "ConfigMap Scenario"},
		{name: "empty after sanitizing", prefix: "!!!"},
		{name: "long", prefix: strings.Repeat("Very-Long-Prefix-", 8)},
	}

	dnsLabel := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	hexSuffix := regexp.MustCompile(`-[0-9a-f]{10}$`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name := UniqueName(t, test.prefix)
			if len(name) > 63 {
				t.Fatalf("name exceeds DNS label limit: len=%d name=%q", len(name), name)
			}
			if !dnsLabel.MatchString(name) {
				t.Fatalf("name is not a DNS label: %q", name)
			}
			if !hexSuffix.MatchString(name) {
				t.Fatalf("name does not have a 10-character hexadecimal suffix: %q", name)
			}
		})
	}

	first := UniqueName(t, "same-prefix")
	second := UniqueName(t, "same-prefix")
	if first == second {
		t.Fatalf("expected independently generated names, got %q twice", first)
	}
}

func testRunIDIsStableHex(t *testing.T) {
	t.Parallel()

	first := RunID()
	second := RunID()
	if first != second {
		t.Fatalf("RunID changed within one process: %q then %q", first, second)
	}
	if matched, _ := regexp.MatchString(`^[0-9a-f]{10}$`, first); !matched {
		t.Fatalf("RunID is not a 10-character hexadecimal value: %q", first)
	}
}

func TestWaitTimeoutUsesSafeDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		nilResource   bool
		timeout, want time.Duration
	}{
		{name: "nil resource", nilResource: true, want: 30 * time.Second},
		{name: "negative timeout", timeout: -time.Second, want: 30 * time.Second},
		{name: "zero timeout", want: 30 * time.Second},
		{name: "configured timeout", timeout: 17 * time.Second, want: 17 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var resources *client.ResourceClient
			if !test.nilResource {
				cl := &client.Client{WaitTimeout: test.timeout, Schemas: map[string]client.SchemaInfo{
					"configmap": {ID: "configmap", CollectionPath: "v1/configmaps"},
				}}
				resources = cl.Resource("configmap")
			}
			if got := waitTimeout(resources); got != test.want {
				t.Fatalf("waitTimeout() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestValidateCreateBodyName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cleanupName string
		body        map[string]any
		wantErr     string
	}{
		{
			name:        "matching name",
			cleanupName: "example",
			body: map[string]any{
				"metadata": map[string]any{"name": "example"},
			},
		},
		{name: "empty cleanup name", cleanupName: " ", body: map[string]any{}, wantErr: "cleanup name must not be empty"},
		{name: "nil body", cleanupName: "example", wantErr: "resource body is nil"},
		{name: "missing metadata", cleanupName: "example", body: map[string]any{}, wantErr: "no non-empty metadata.name"},
		{
			name:        "mismatched name",
			cleanupName: "expected",
			body: map[string]any{
				"metadata": map[string]any{"name": "actual"},
			},
			wantErr: "does not match cleanup name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateCreateBodyName(test.cleanupName, test.body)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCreateBodyName returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

func TestCleanupAmbiguousCreateDeletesOnlyResourcesOwnedByCurrentRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		createErr   error
		labels      string
		wantMethods []string
	}{
		{
			name:        "owned resource after response failure",
			createErr:   errors.New("decode successful create response: unexpected EOF"),
			labels:      fmt.Sprintf(`%q:%q,%q:%q`, ManagedLabel, "true", RunIDLabel, RunID()),
			wantMethods: []string{"GET", "DELETE", "GET"},
		},
		{
			name:        "foreign resource is preserved",
			createErr:   errors.New("connection reset after POST"),
			labels:      fmt.Sprintf(`%q:%q,%q:%q`, ManagedLabel, "true", RunIDLabel, "another-run"),
			wantMethods: []string{"GET"},
		},
		{
			name:      "definitive conflict makes no cleanup request",
			createErr: &client.StatusError{StatusCode: http.StatusConflict},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var methods []string
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				switch request.Method {
				case http.MethodGet:
					if len(methods) > 1 {
						return response(http.StatusNotFound, `{}`), nil
					}
					return response(http.StatusOK, fmt.Sprintf(`{"metadata":{"name":"example","namespace":"qa","labels":{%s}}}`, test.labels)), nil
				case http.MethodDelete:
					return response(http.StatusNoContent, ""), nil
				default:
					return response(http.StatusMethodNotAllowed, `{}`), nil
				}
			})
			steveClient := &client.Client{
				BaseURL:      "https://steve.example.test",
				HTTPClient:   &http.Client{Transport: transport},
				PollInterval: time.Millisecond,
				WaitTimeout:  50 * time.Millisecond,
				Schemas: map[string]client.SchemaInfo{
					"configmap": {ID: "configmap", Namespaced: true, CollectionPath: "v1/configmaps"},
				},
			}
			cleanupAmbiguousCreate(t, steveClient.Resource("configmap").Namespace("qa"), "example", test.createErr)
			if !equalStrings(methods, test.wantMethods) {
				t.Fatalf("cleanup request methods = %v, want %v", methods, test.wantMethods)
			}
		})
	}
}
