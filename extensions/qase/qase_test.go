package qase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
)

func testService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv(APIURLEnvVar, server.URL)
	t.Setenv(QaseTokenEnvVar, "test-token")
	return SetupQaseClient()
}

func writeEntities(w http.ResponseWriter, entities []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "result": map[string]any{"entities": entities}})
}

func TestUploadTestsOnlyMutatesCasesInTargetSuite(t *testing.T) {
	for _, tc := range []struct {
		name         string
		localIDs     []int
		wantMutation string
		wantError    string
	}{
		{name: "foreign case does not prevent creation", wantMutation: "POST /case/RM"},
		{name: "update only the local case", localIDs: []int{2}, wantMutation: "PATCH /case/RM/2"},
		{name: "duplicate local cases fail", localIDs: []int{2, 3}, wantError: "2 Qase cases match in suite 30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations []string
			service := testService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/case/RM" {
					entities := []map[string]any{{"id": 1, "title": "TestPods", "suite_id": 99}}
					for _, id := range tc.localIDs {
						entities = append(entities, map[string]any{"id": id, "title": "TestPods", "suite_id": 30})
					}
					writeEntities(w, entities)
					return
				}
				mutations = append(mutations, r.Method+" "+r.URL.Path)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode mutation: %v", err)
				}
				if body["suite_id"] != float64(30) {
					t.Errorf("wrong mutation suite: %v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":true,"result":{"id":2}}`))
			})
			suiteID := int64(30)
			err := service.UploadTests(context.Background(), "RM", []upstream.TestCaseCreate{{Title: "TestPods", SuiteId: &suiteID}})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("wanted error containing %q, got %v", tc.wantError, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(mutations, ", "); got != tc.wantMutation {
				t.Fatalf("mutations = %q, want %q", got, tc.wantMutation)
			}
		})
	}
}

func TestFindTestCasesPaginatesAndMatchesAutomationNameWithinSuite(t *testing.T) {
	var offsets []string
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		offsets = append(offsets, r.URL.Query().Get("offset"))
		if r.Method != http.MethodGet || r.URL.Path != "/case/RM" || r.URL.Query().Get("search") != "TestPods" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.URL.Query().Get("offset") == "0" {
			entities := make([]map[string]any, requestLimit)
			for i := range entities {
				entities[i] = map[string]any{"id": i + 1, "title": "TestPods", "suite_id": 99}
			}
			writeEntities(w, entities)
			return
		}
		writeEntities(w, []map[string]any{
			{"id": 201, "title": "Pod lifecycle", "suite_id": 30, "custom_fields": []map[string]any{{"id": 15, "value": "TestPods"}, {"id": 42, "value": "selected"}}},
			{"id": 202, "title": "TestPods", "suite_id": 30},
			{"id": 203, "title": "TestPods", "suite_id": nil},
		})
	})
	cases, err := service.FindTestCases(context.Background(), "RM", "TestPods", "selected", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].GetId() != 201 {
		t.Fatalf("unexpected matches: %+v", cases)
	}
	if got := strings.Join(offsets, ","); got != "0,100" {
		t.Fatalf("offsets = %q, want 0,100", got)
	}
}

func TestGetTestSuitePathUsesExactHierarchyWithoutCreating(t *testing.T) {
	for _, tc := range []struct {
		name      string
		leaf      []map[string]any
		wantError string
	}{
		{name: "same name under foreign parent is ignored", leaf: []map[string]any{{"id": 40, "title": "pods", "parent_id": 99}, {"id": 30, "title": "pods", "parent_id": 20}}},
		{name: "missing suite fails", wantError: "upload the schemas first"},
		{name: "duplicate suites fail", leaf: []map[string]any{{"id": 30, "title": "pods", "parent_id": 20}, {"id": 31, "title": "pods", "parent_id": 20}}, wantError: "multiple Qase suites"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := testService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/suite/RM" {
					t.Errorf("suite resolution must only read: %s %s", r.Method, r.URL)
				}
				switch r.URL.Query().Get("search") {
				case "Go Automation":
					writeEntities(w, []map[string]any{{"id": 10, "title": "Go Automation", "parent_id": nil}})
				case "Steve":
					writeEntities(w, []map[string]any{{"id": 20, "title": "Steve", "parent_id": 10}})
				case "pods":
					writeEntities(w, tc.leaf)
				default:
					t.Errorf("unexpected suite query: %s", r.URL)
				}
			})
			id, err := service.GetTestSuitePath(context.Background(), "RM", "Go Automation/Steve/pods")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("wanted error containing %q, got %v", tc.wantError, err)
				}
			} else if err != nil || id != 30 {
				t.Fatalf("suite ID = %d, error = %v", id, err)
			}
		})
	}
}
