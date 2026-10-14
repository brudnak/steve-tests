package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/query"
)

// Secret collection requests must finish whether or not the target exposes
// Rancher Projects. Failure messages must not include Secret response bodies.
func TestSecretCollectionReturnsBeforeDeadline(t *testing.T) {
	cl := getTestClient(t)
	if _, present := cl.Schema("management.cattle.io.project"); present {
		t.Log("management.cattle.io.project is present")
	} else {
		t.Log("management.cattle.io.project is absent")
	}
	schema, ok := cl.Schema("secret")
	if !ok {
		t.Fatal("Secret schema was not discovered")
	}

	tests := []struct {
		name   string
		values url.Values
	}{
		{
			name: "bounded collection",
			values: url.Values{
				query.PageSizeParam: {"1"},
			},
		},
		{
			name: "bounded sorted collection",
			values: url.Values{
				query.PageSizeParam: {"1"},
				query.SortParam:     {"metadata.name"},
			},
		},
		{
			name: "bounded no-match collection",
			values: url.Values{
				query.PageSizeParam: {"1"},
				query.FilterParam:   {query.Equal("metadata.name", "steve-tests-no-such-secret")},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const hardDeadline = 10 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), hardDeadline)
			defer cancel()

			path := schema.CollectionPath + "?" + tc.values.Encode()
			started := time.Now()
			resp, body, err := cl.GetContext(ctx, path)
			elapsed := time.Since(started)
			if err != nil {
				if ctx.Err() != nil {
					t.Fatalf("Secret collection did not return within %s", hardDeadline)
				}
				t.Fatalf("Secret collection request failed after %s: %v", elapsed, err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("Secret collection returned status %d after %s (response body withheld; %d bytes)", resp.StatusCode, elapsed, len(body))
			}
			if !json.Valid(body) {
				t.Fatalf("Secret collection returned invalid JSON after %s (response body withheld; %d bytes)", elapsed, len(body))
			}
			t.Logf("Secret collection returned status 200 in %s (%d bytes; body withheld)", elapsed, len(body))
		})
	}
}
