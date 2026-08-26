package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"m365-copilot2api/internal/chathub"
)

func TestAccountConcurrencyLimitsAndReleasesSlots(t *testing.T) {
	t.Setenv("M365_ACCOUNT_DEFAULT_CONCURRENCY", "2")
	limiter := newAccountConcurrency()
	release1, err := limiter.Acquire(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	release2, err := limiter.Acquire(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if limiter.Available("account-a") {
		t.Fatal("account remained available at its configured limit")
	}
	if !limiter.Available("account-b") {
		t.Fatal("one full account must not block another account")
	}
	release1()
	if !limiter.Available("account-a") {
		t.Fatal("released slot was not returned")
	}
	release1()
	release2()
}

func TestAccountConcurrencyWaitHonorsCancellation(t *testing.T) {
	t.Setenv("M365_ACCOUNT_DEFAULT_CONCURRENCY", "1")
	limiter := newAccountConcurrency()
	release, err := limiter.Acquire(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.Acquire(ctx, "account-a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire() error = %v, want deadline exceeded", err)
	}
}

func TestAccountConcurrencyUsesDocumentedDefault(t *testing.T) {
	t.Setenv("M365_ACCOUNT_DEFAULT_CONCURRENCY", "")
	limiter := newAccountConcurrency()
	if limiter.limit != defaultAccountConcurrency {
		t.Fatalf("limit = %d, want %d", limiter.limit, defaultAccountConcurrency)
	}
}

func TestCallWithTransientRetryEventuallySucceeds(t *testing.T) {
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "2")
	t.Setenv("M365_TRANSIENT_RETRY_DELAY_MS", "1")
	calls := 0
	result, err := callWithTransientRetry(context.Background(), "account-a", nil, func() (chathub.Result, error) {
		calls++
		if calls == 1 {
			return chathub.Result{}, errors.New("ws dial: websocket: bad handshake")
		}
		return chathub.Result{Text: "ok"}, nil
	})
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d want 2", calls)
	}
}

func TestCallWithTransientRetryStopsAtBudget(t *testing.T) {
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "2")
	t.Setenv("M365_TRANSIENT_RETRY_DELAY_MS", "1")
	calls := 0
	_, err := callWithTransientRetry(context.Background(), "account-a", nil, func() (chathub.Result, error) {
		calls++
		return chathub.Result{}, errors.New("ws dial: websocket: bad handshake")
	})
	if err == nil {
		t.Fatal("expected final transient error")
	}
	if calls != 3 {
		t.Fatalf("calls=%d want initial call plus 2 retries", calls)
	}
}

func TestCallWithTransientRetryDoesNotReplayObservedStream(t *testing.T) {
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "2")
	t.Setenv("M365_TRANSIENT_RETRY_DELAY_MS", "1")
	calls := 0
	observed := true
	_, err := callWithTransientRetry(context.Background(), "account-a", func() bool { return observed }, func() (chathub.Result, error) {
		calls++
		return chathub.Result{}, errors.New("ws read before completion: connection reset")
	})
	if err == nil {
		t.Fatal("expected stream error")
	}
	if calls != 1 {
		t.Fatalf("observed stream was replayed %d times", calls)
	}
}

func TestCallWithTransientRetryRetriesUpstreamCancellationWhileRequestIsAlive(t *testing.T) {
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "2")
	t.Setenv("M365_TRANSIENT_RETRY_DELAY_MS", "1")
	calls := 0
	result, err := callWithTransientRetry(context.Background(), "account-a", nil, func() (chathub.Result, error) {
		calls++
		if calls == 1 {
			return chathub.Result{}, context.Canceled
		}
		return chathub.Result{Text: "ok"}, nil
	})
	if err != nil || result.Text != "ok" || calls != 2 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestCallWithTransientRetryStopsWhenRequestContextIsCanceled(t *testing.T) {
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "2")
	t.Setenv("M365_TRANSIENT_RETRY_DELAY_MS", "1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := callWithTransientRetry(ctx, "account-a", nil, func() (chathub.Result, error) {
		calls++
		return chathub.Result{}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestTransientRetryAttemptsCanBeDisabledAndIsCapped(t *testing.T) {
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "0")
	if got := transientRetryAttempts(); got != 0 {
		t.Fatalf("disabled retries=%d", got)
	}
	t.Setenv("M365_TRANSIENT_RETRY_ATTEMPTS", "99")
	if got := transientRetryAttempts(); got != maxTransientRetryAttempts {
		t.Fatalf("capped retries=%d want %d", got, maxTransientRetryAttempts)
	}
}
