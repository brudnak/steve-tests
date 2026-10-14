package actions

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rancher/steve-tests/pkg/client"
)

func TestWaitForCollectionConvergesAcrossTransientResults(t *testing.T) {
	t.Parallel()

	lister := &scriptedCollectionLister{
		pollInterval: time.Nanosecond,
		responses: []collectionListResponse{
			{collection: namedCollection("alpha")},
			{err: probeStatusError(http.StatusServiceUnavailable, "cache warming")},
			{collection: namedCollection("alpha", "bravo")},
		},
	}

	collection, err := waitForCollection(context.Background(), lister, "pagesize=2", CollectionExpectation{
		Length: intPointer(2),
		Order:  []string{"alpha", "bravo"},
	})
	if err != nil {
		t.Fatalf("waitForCollection returned error: %v", err)
	}
	if lister.calls != 3 {
		t.Fatalf("expected three list attempts, got %d", lister.calls)
	}
	if got := collectionNames(collection); !equalStrings(got, []string{"alpha", "bravo"}) {
		t.Fatalf("unexpected converged collection names: %v", got)
	}
}

func TestWaitForCollectionFailsFastOnPermanentListError(t *testing.T) {
	t.Parallel()

	statusErr := probeStatusError(http.StatusUnprocessableEntity, "invalid filter")
	lister := &scriptedCollectionLister{
		pollInterval: time.Hour,
		responses:    []collectionListResponse{{err: statusErr}},
	}

	_, err := waitForCollection(context.Background(), lister, "filter=bad", CollectionExpectation{Length: intPointer(0)})
	if err == nil {
		t.Fatal("expected permanent list error")
	}
	if !errors.Is(err, statusErr) {
		t.Fatalf("expected original status error in error chain, got %v", err)
	}
	if lister.calls != 1 {
		t.Fatalf("permanent error should stop after one attempt, got %d", lister.calls)
	}
}

func TestIsRetryableCollectionListError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "nil"},
		{name: "transport", err: errors.New("connection reset"), retryable: true},
		{name: "context canceled", err: context.Canceled},
		{name: "context deadline", err: context.DeadlineExceeded},
		{name: "bad request", err: probeStatusError(http.StatusBadRequest, "bad query")},
		{name: "forbidden", err: probeStatusError(http.StatusForbidden, "forbidden")},
		{name: "unprocessable", err: probeStatusError(http.StatusUnprocessableEntity, "bad filter")},
		{name: "request timeout", err: probeStatusError(http.StatusRequestTimeout, "timeout"), retryable: true},
		{name: "too early", err: probeStatusError(http.StatusTooEarly, "warming"), retryable: true},
		{name: "rate limited", err: probeStatusError(http.StatusTooManyRequests, "slow down"), retryable: true},
		{name: "server error", err: probeStatusError(http.StatusInternalServerError, "cache warming"), retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryableCollectionListError(test.err); got != test.retryable {
				t.Fatalf("isRetryableCollectionListError(%v) = %t, want %t", test.err, got, test.retryable)
			}
		})
	}
}

func TestWaitForCollectionCancellationIncludesLastResult(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	lister := &scriptedCollectionLister{
		pollInterval: time.Hour,
		responses:    []collectionListResponse{{collection: namedCollection("alpha")}},
		afterList:    cancel,
	}

	_, err := waitForCollection(ctx, lister, "", CollectionExpectation{Contains: []string{"bravo"}})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	for _, want := range []string{"after 1 attempt(s)", "names=[alpha]", `contain "bravo"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected cancellation error to contain %q, got %v", want, err)
		}
	}
}

func TestWaitForCollectionRejectsInvalidInputsBeforeListing(t *testing.T) {
	t.Parallel()

	if _, err := waitForCollection(context.Background(), nil, "", CollectionExpectation{Length: intPointer(0)}); err == nil || !strings.Contains(err.Error(), "lister is nil") {
		t.Fatalf("expected nil lister error, got %v", err)
	}

	lister := &scriptedCollectionLister{}
	if _, err := waitForCollection(context.Background(), lister, "", CollectionExpectation{}); err == nil || !strings.Contains(err.Error(), "at least one expectation") {
		t.Fatalf("expected invalid expectation error, got %v", err)
	}
	if lister.calls != 0 {
		t.Fatalf("invalid expectation should not issue a list request, got %d", lister.calls)
	}
}

type collectionListResponse struct {
	collection *client.SteveCollection
	err        error
}

type scriptedCollectionLister struct {
	responses    []collectionListResponse
	pollInterval time.Duration
	afterList    func()
	calls        int
}

func (lister *scriptedCollectionLister) ListContext(_ context.Context, _ string) (*client.SteveCollection, error) {
	index := lister.calls
	lister.calls++
	if lister.afterList != nil {
		lister.afterList()
	}
	if len(lister.responses) == 0 {
		return &client.SteveCollection{}, nil
	}
	if index >= len(lister.responses) {
		index = len(lister.responses) - 1
	}
	response := lister.responses[index]
	return response.collection, response.err
}

func (lister *scriptedCollectionLister) PollInterval() time.Duration {
	return lister.pollInterval
}

func intPointer(value int) *int {
	return &value
}
