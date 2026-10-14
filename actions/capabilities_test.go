package actions

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

func TestSQLProbeScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "ProbeSQLModeStopsAtContextCancellation", run: testProbeSQLModeStopsAtContextCancellation},
		{name: "ProbeSQLModeRejectsMissingDependencies", run: testProbeSQLModeRejectsMissingDependencies},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestProbeSQLModeClassifiesTerminalResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		wantSQL bool
		wantErr string
	}{
		{
			name:    "non-SQL accepts arbitrary sort",
			wantSQL: false,
		},
		{
			name:    "SQL rejects non-indexed sort",
			err:     probeStatusError(http.StatusUnprocessableEntity, "column is invalid ["+sqlModeProbeSort+"]: supplied column is invalid"),
			wantSQL: true,
		},
		{name: "unrelated 422 is not SQL", err: probeStatusError(http.StatusUnprocessableEntity, "invalid request"), wantErr: "status 422"},
		{name: "other invalid column is not our probe", err: probeStatusError(http.StatusUnprocessableEntity, "column is invalid [metadata.other]"), wantErr: "status 422"},
		{
			name:    "authorization failure is not a mode",
			err:     probeStatusError(http.StatusForbidden, "forbidden"),
			wantErr: "status 403",
		},
		{
			name:    "unrelated internal error is not cache warmup",
			err:     probeStatusError(http.StatusInternalServerError, "database is unavailable"),
			wantErr: "status 500",
		},
		{
			name:    "transport failure is surfaced",
			err:     errors.New("connection reset"),
			wantErr: "connection reset",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			lister := &scriptedSQLModeLister{responses: []error{test.err}}
			gotSQL, err := probeSQLMode(context.Background(), lister, time.Millisecond)
			if gotSQL != test.wantSQL {
				t.Fatalf("probeSQLMode SQL mode = %t, want %t", gotSQL, test.wantSQL)
			}
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("probeSQLMode returned error: %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
				}
			}
			if lister.calls != 1 {
				t.Fatalf("expected one terminal probe call, got %d", lister.calls)
			}
			assertSQLModeProbeQuery(t, lister.queries[0])
		})
	}
}

func TestProbeSQLModeRetriesOnlyCacheWarmup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal error
		wantSQL  bool
	}{
		{
			name:     "cache warms into SQL mode",
			terminal: probeStatusError(http.StatusUnprocessableEntity, "column is invalid ["+sqlModeProbeSort+"]: supplied column is invalid"),
			wantSQL:  true,
		},
		{
			name:     "cache warms into non-SQL mode",
			terminal: nil,
			wantSQL:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			lister := &scriptedSQLModeLister{responses: []error{
				probeStatusError(http.StatusInternalServerError, "waiting for SQL cache sync"),
				probeStatusError(http.StatusInternalServerError, "CACHE CONTEXT CANCELED during startup"),
				test.terminal,
			}}
			gotSQL, err := probeSQLMode(context.Background(), lister, time.Nanosecond)
			if err != nil {
				t.Fatalf("probeSQLMode returned error: %v", err)
			}
			if gotSQL != test.wantSQL {
				t.Fatalf("probeSQLMode SQL mode = %t, want %t", gotSQL, test.wantSQL)
			}
			if lister.calls != 3 {
				t.Fatalf("expected two warmup retries and one terminal call, got %d", lister.calls)
			}
		})
	}
}

func testProbeSQLModeStopsAtContextCancellation(t *testing.T) {
	t.Parallel()

	warmup := probeStatusError(http.StatusInternalServerError, "SQL cache sync is incomplete")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lister := &scriptedSQLModeLister{
		responses:  []error{warmup},
		repeatLast: true,
		afterCall: func(calls int) {
			if calls == 3 {
				cancel()
			}
		},
	}

	gotSQL, err := probeSQLMode(ctx, lister, time.Nanosecond)
	if gotSQL {
		t.Fatal("probeSQLMode reported SQL mode while cache never became ready")
	}
	if err == nil {
		t.Fatal("expected cache warmup timeout error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation in error chain, got %v", err)
	}
	if !strings.Contains(err.Error(), "last error") || !strings.Contains(err.Error(), "SQL cache sync") {
		t.Fatalf("timeout error did not retain the last probe response: %v", err)
	}
	if lister.calls != 3 {
		t.Fatalf("expected three deterministic cache probes, got %d call(s)", lister.calls)
	}
}

func testProbeSQLModeRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	if _, err := probeSQLMode(nil, &scriptedSQLModeLister{}, time.Millisecond); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("expected nil context error, got %v", err)
	}
	if _, err := probeSQLMode(context.Background(), nil, time.Millisecond); err == nil || !strings.Contains(err.Error(), "lister is nil") {
		t.Fatalf("expected nil lister error, got %v", err)
	}
	if _, err := ProbeSQLMode(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("expected nil client error, got %v", err)
	}
	cl := &client.Client{Schemas: map[string]client.SchemaInfo{}}
	if _, err := ProbeSQLMode(context.Background(), cl); err == nil || !strings.Contains(err.Error(), `schema "configmap" is required`) {
		t.Fatalf("expected missing schema error, got %v", err)
	}
}

type scriptedSQLModeLister struct {
	responses  []error
	repeatLast bool
	afterCall  func(int)
	calls      int
	queries    []string
}

func (lister *scriptedSQLModeLister) ListContext(_ context.Context, query string) (*client.SteveCollection, error) {
	lister.queries = append(lister.queries, query)
	responseIndex := lister.calls
	lister.calls++
	if lister.afterCall != nil {
		lister.afterCall(lister.calls)
	}
	if len(lister.responses) == 0 {
		return &client.SteveCollection{}, nil
	}
	if responseIndex >= len(lister.responses) {
		if lister.repeatLast {
			responseIndex = len(lister.responses) - 1
		} else {
			return &client.SteveCollection{}, nil
		}
	}
	if err := lister.responses[responseIndex]; err != nil {
		return nil, err
	}
	return &client.SteveCollection{}, nil
}

func probeStatusError(status int, body string) error {
	return &client.StatusError{
		Method:     http.MethodGet,
		Path:       "v1/configmaps",
		StatusCode: status,
		Body:       body,
	}
}

func assertSQLModeProbeQuery(t *testing.T, rawQuery string) {
	t.Helper()
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("parse probe query: %v", err)
	}
	if _, present := values["limit"]; present {
		t.Fatal("probe must not mix the internal limit parameter with public Steve pagination")
	}
	if got := values.Get("pagesize"); got != "1" {
		t.Fatalf("probe pagesize = %q, want 1", got)
	}
	if got := values.Get("sort"); got != sqlModeProbeSort {
		t.Fatalf("probe sort = %q, want %q", got, sqlModeProbeSort)
	}
}

func TestDetectCacheMode(t *testing.T) {
	tests := []struct {
		name, expected      string
		status              int
		body, want, wantErr string
	}{
		{name: "detect SQL", status: 422, body: `{"message":"column is invalid [spec.steveTestsSQLModeProbe]: supplied column is invalid"}`, want: "sql"},
		{name: "detect non-SQL", status: 200, body: `{"data":[]}`, want: "non-sql"},
		{name: "expected SQL", expected: "sql", status: 422, body: `{"message":"column is invalid [spec.steveTestsSQLModeProbe]: supplied column is invalid"}`, want: "sql"},
		{name: "SQL mismatch", expected: "sql", status: 200, body: `{"data":[]}`, wantErr: "is non-sql, expected sql"},
		{name: "non-SQL mismatch", expected: "non-sql", status: 422, body: `{"message":"column is invalid [spec.steveTestsSQLModeProbe]: supplied column is invalid"}`, wantErr: "is sql, expected non-sql"},
		{name: "invalid expectation", expected: "both", wantErr: "expected cache mode"},
		{name: "unknown mode fails", status: 403, body: `{"message":"forbidden"}`, wantErr: "status 403"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			cl := &client.Client{BaseURL: "https://steve.example.test", Schemas: map[string]client.SchemaInfo{"configmap": {ID: "configmap", CollectionPath: "v1/configmaps"}}, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(test.status, test.body), nil
			})}}
			mode, err := DetectCacheMode(t.Context(), cl, test.expected)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
			} else if err != nil || mode != test.want {
				t.Fatalf("mode=%q error=%v, want %q", mode, err, test.want)
			}
			if test.expected == "both" && calls != 0 {
				t.Fatal("invalid expectation made a request")
			}
		})
	}
}
