package web

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"m365-copilot2api/internal/chathub"
)

func TestAccountConcurrencyLimitsAndReleasesSlots(t *testing.T) {
	t.Setenv("M365_ACCOUNT_DEFAULT_CONCURRENCY", "2")
	t.Setenv("M365_ACCOUNT_CONCURRENCY_LIMIT", "")
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
	t.Setenv("M365_ACCOUNT_CONCURRENCY_LIMIT", "")
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
	t.Setenv("M365_ACCOUNT_CONCURRENCY_LIMIT", "")
	limiter := newAccountConcurrency()
	if limiter.limit != defaultAccountConcurrency {
		t.Fatalf("limit = %d, want %d", limiter.limit, defaultAccountConcurrency)
	}
}

func TestAdaptiveAccountConcurrencyReducesOn429AndRecovers(t *testing.T) {
	limiter := &accountConcurrency{
		limit:      20,
		inflight:   map[string]int{},
		changed:    make(chan struct{}),
		adaptive:   true,
		perAccount: map[string]int{},
		successes:  map[string]int{},
	}
	if got := limiter.effectiveLimit("account-a"); got != 5 {
		t.Fatalf("initial adaptive limit=%d, want 5", got)
	}
	limiter.Observe("account-a", &UpstreamHTTPError{Status: 429})
	if got := limiter.effectiveLimit("account-a"); got != 3 {
		t.Fatalf("429 adaptive limit=%d, want 3", got)
	}
	limiter.Observe("account-a", nil)
	if got := limiter.effectiveLimit("account-a"); got != 3 {
		t.Fatalf("limit grew after one success=%d, want 3", got)
	}
	limiter.Observe("account-a", nil)
	if got := limiter.effectiveLimit("account-a"); got != 4 {
		t.Fatalf("limit after recovery=%d, want 4", got)
	}
}

func TestAccountConcurrencySupports128ConcurrentCalls(t *testing.T) {
	const concurrency = 128
	t.Setenv("M365_ACCOUNT_CONCURRENCY_LIMIT", "128")
	limiter := newAccountConcurrency()
	if limiter.Limit() != concurrency {
		t.Fatalf("limit = %d, want %d", limiter.Limit(), concurrency)
	}

	var active, maximum int64
	acquired := make(chan func(), concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := limiter.Acquire(context.Background(), "account-a")
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			current := atomic.AddInt64(&active, 1)
			for {
				old := atomic.LoadInt64(&maximum)
				if current <= old || atomic.CompareAndSwapInt64(&maximum, old, current) {
					break
				}
			}
			acquired <- release
		}()
	}
	deadlines := time.After(2 * time.Second)
	leases := make([]func(), 0, concurrency)
	for i := 0; i < concurrency; i++ {
		select {
		case release := <-acquired:
			leases = append(leases, release)
		case <-deadlines:
			t.Fatalf("only %d of %d calls acquired a slot", i, concurrency)
		}
	}
	if got := limiter.Inflight("account-a"); got != concurrency {
		t.Fatalf("inflight = %d, want %d", got, concurrency)
	}
	if limiter.Available("account-a") {
		t.Fatalf("account should be full at %d concurrent calls", concurrency)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := limiter.Acquire(ctx, "account-a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%dst acquire error = %v, want deadline exceeded", concurrency+1, err)
	}
	for i := 0; i < concurrency; i++ {
		leases[i]()
		atomic.AddInt64(&active, -1)
	}
	wg.Wait()
	if got := limiter.Inflight("account-a"); got != 0 || maximum != concurrency {
		t.Fatalf("final inflight=%d maximum=%d, want 0 and %d", got, maximum, concurrency)
	}
}

func TestAccountConcurrencyRejectsInvalidConfiguredValues(t *testing.T) {
	for _, raw := range []string{"0", "-1", "1025", "9999", "not-a-number"} {
		t.Setenv("M365_ACCOUNT_CONCURRENCY_LIMIT", raw)
		t.Setenv("M365_ACCOUNT_DEFAULT_CONCURRENCY", "")
		if got := configuredAccountConcurrencyLimit(); got != defaultAccountConcurrency {
			t.Fatalf("configured limit for %q = %d, want default %d", raw, got, defaultAccountConcurrency)
		}
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
