package actions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

// CollectionExpectation describes the result of a collection query.
// A non-nil Order checks the exact sequence, including an empty slice.
type CollectionExpectation struct {
	Length      *int
	Contains    []string
	NotContains []string
	Order       []string
}

type collectionLister interface {
	ListContext(context.Context, string) (*client.SteveCollection, error)
	PollInterval() time.Duration
}

func hasCollectionExpectation(expect CollectionExpectation) bool {
	return expect.Length != nil || len(expect.Contains) > 0 || len(expect.NotContains) > 0 || expect.Order != nil
}

func waitForCollection(ctx context.Context, resources collectionLister, query string, expect CollectionExpectation) (*client.SteveCollection, error) {
	if resources == nil {
		return nil, fmt.Errorf("collection lister is nil")
	}
	if err := validateCollectionExpectation(expect); err != nil {
		return nil, fmt.Errorf("invalid collection expectation: %w", err)
	}

	started := time.Now()
	attempts := 0
	var lastCollection *client.SteveCollection
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			return nil, collectionWaitError(started, attempts, lastCollection, lastErr, err)
		}

		attempts++
		collection, err := resources.ListContext(ctx, query)
		if err != nil {
			lastErr = fmt.Errorf("list request failed: %w", err)
			if !isRetryableCollectionListError(err) {
				return nil, fmt.Errorf("collection list request failed on attempt %d: %w", attempts, err)
			}
		} else {
			lastCollection = collection
			lastErr = ValidateCollection(collection, expect)
			if lastErr == nil {
				return collection, nil
			}
		}

		pollInterval := resources.PollInterval()
		if pollInterval <= 0 {
			pollInterval = 200 * time.Millisecond
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, collectionWaitError(started, attempts, lastCollection, lastErr, ctx.Err())
		case <-timer.C:
		}
	}
}

func isRetryableCollectionListError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr *client.StatusError
	if !errors.As(err, &statusErr) {
		return true
	}
	return statusErr.StatusCode == http.StatusRequestTimeout ||
		statusErr.StatusCode == http.StatusTooEarly ||
		statusErr.StatusCode == http.StatusTooManyRequests ||
		statusErr.StatusCode >= http.StatusInternalServerError
}

func collectionWaitError(started time.Time, attempts int, coll *client.SteveCollection, lastErr error, cause error) error {
	if lastErr == nil {
		lastErr = fmt.Errorf("no collection result was received")
	}
	return fmt.Errorf("collection expectation was not satisfied after %d attempt(s) in %s; last result: %s; last error: %v: %w",
		attempts,
		time.Since(started).Round(time.Millisecond),
		describeCollection(coll),
		lastErr,
		cause,
	)
}

func describeCollection(coll *client.SteveCollection) string {
	if coll == nil {
		return "<nil>"
	}
	return fmt.Sprintf("dataLength=%d count=%d revision=%q names=%v",
		len(coll.Data),
		coll.Count,
		coll.Revision,
		collectionNames(coll),
	)
}

// WaitForCollection waits for the complete query result, including filters and
// sort order, to match. Object visibility alone does not mean indexes are ready.
func WaitForCollection(t *testing.T, resources *client.ResourceClient, query string, expect CollectionExpectation) *client.SteveCollection {
	t.Helper()
	if resources == nil {
		t.Fatal("collection resource client is nil")
	}
	ctx, cancel := context.WithTimeout(t.Context(), waitTimeout(resources))
	defer cancel()
	collection, err := waitForCollection(ctx, resources, query, expect)
	if err != nil {
		t.Fatalf("collection did not converge: %v", err)
	}
	return collection
}
