package api

import (
	"context"
	"testing"
	"time"
)

func TestOnlyPostUpdateReceivesLongRequestBudget(t *testing.T) {
	for _, request := range []struct {
		method, path string
		want         time.Duration
	}{
		{"POST", "/v1/update", 30 * time.Minute},
		{"GET", "/v1/status", 5 * time.Minute},
		{"GET", "/v1/update", 5 * time.Minute},
		{"POST", "/v1/update-other", 5 * time.Minute},
	} {
		ctx, cancel := boundedRequestContext(context.Background(), request.method, request.path)
		deadline, ok := ctx.Deadline()
		cancel()
		if !ok || time.Until(deadline) < request.want-time.Second || time.Until(deadline) > request.want {
			t.Fatalf("wrong request budget for %s %s", request.method, request.path)
		}
	}
}
func TestUpdateBudgetPreservesEarlierCallerDeadline(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	ctx, cancel := boundedRequestContext(parent, "POST", "/v1/update")
	defer cancel()
	expected, _ := parent.Deadline()
	actual, _ := ctx.Deadline()
	if !actual.Equal(expected) {
		t.Fatal("caller deadline was extended")
	}
	stop()
	<-ctx.Done()
	if ctx.Err() != context.Canceled {
		t.Fatalf("caller cancellation lost: %v", ctx.Err())
	}
}
