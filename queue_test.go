package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0495"
)

var (
	errInfra = errors.New("sandbox network unreachable")
	errCrash = errors.New("segmentation fault in sample")
)

// scriptedClassifier returns a per-attempt class from the plan map. The same
// error can be classified differently on attempt 1 and attempt 2, which lets
// the tests prove that classification changes drive retries without ever
// re-executing an attempt.
type scriptedClassifier struct {
	mu   sync.Mutex
	plan map[string]netsec.FailureClass
}

func newScriptedClassifier() *scriptedClassifier {
	return &scriptedClassifier{plan: map[string]netsec.FailureClass{}}
}

func (c *scriptedClassifier) set(sampleID string, number int, class netsec.FailureClass) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plan[key(sampleID, number)] = class
}

func (c *scriptedClassifier) Classify(attempt netsec.Attempt, cause error) netsec.FailureClass {
	c.mu.Lock()
	defer c.mu.Unlock()
	if class, ok := c.plan[key(attempt.SampleID, attempt.Number)]; ok {
		return class
	}
	return netsec.ClassUnspecified
}

func key(sampleID string, number int) string {
	return fmt.Sprintf("%s#%d", sampleID, number)
}

func attemptKey(a netsec.Attempt) string { return key(a.SampleID, a.Number) }

// runWorker claims and completes samples with the supplied handler until the
// queue closes and drains.
func runWorker(ctx context.Context, t *testing.T, q *netsec.Queue, executed *sync.Map,
	handle func(*netsec.Lease) (bool, error)) {
	t.Helper()
	for {
		lease, err := q.Claim(ctx)
		if errors.Is(err, netsec.ErrQueueClosed) {
			return
		}
		if err != nil {
			t.Errorf("Claim: %v", err)
			return
		}
		if _, loaded := executed.LoadOrStore(attemptKey(lease.Attempt), struct{}{}); loaded {
			t.Errorf("attempt %s was executed more than once", attemptKey(lease.Attempt))
		}
		ok, cause := handle(lease)
		if err := q.Complete(lease, ok, cause); err != nil {
			t.Errorf("Complete %s: %v", attemptKey(lease.Attempt), err)
		}
	}
}
func TestEnqueueValidationAndDuplicates(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassInfrastructure
	}), 2)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: ""}); !errors.Is(err, netsec.ErrInvalidSample) {
		t.Fatalf("empty id: want ErrInvalidSample, got %v", err)
	}
	if err := q.Enqueue(netsec.Sample{ID: "s1"}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := q.Enqueue(netsec.Sample{ID: "s1"}); !errors.Is(err, netsec.ErrDuplicateSample) {
		t.Fatalf("duplicate id: want ErrDuplicateSample, got %v", err)
	}

	ctx := context.Background()
	lease, err := q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Complete(lease, true, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := q.Enqueue(netsec.Sample{ID: "s1"}); !errors.Is(err, netsec.ErrDuplicateSample) {
		t.Fatalf("reuse of finished id: want ErrDuplicateSample, got %v", err)
	}
}

func TestInfrastructureRetryThenSuccessKeepsHistory(t *testing.T) {
	classifier := newScriptedClassifier()
	classifier.set("s", 1, netsec.ClassInfrastructure)
	classifier.set("s", 2, netsec.ClassInfrastructure)
	q := netsec.NewQueue(classifier, 3)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: "s"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ctx := context.Background()
	seen := map[string]int{}
	for {
		lease, err := q.Claim(ctx)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		k := attemptKey(lease.Attempt)
		seen[k]++
		switch lease.Attempt.Number {
		case 1, 2:
			if err := q.Complete(lease, false, errInfra); err != nil {
				t.Fatalf("Complete %s: %v", k, err)
			}
		case 3:
			if err := q.Complete(lease, true, nil); err != nil {
				t.Fatalf("Complete %s: %v", k, err)
			}
			result, err := q.WaitResult(ctx, "s")
			if err != nil {
				t.Fatalf("WaitResult: %v", err)
			}
			if result.Status != netsec.StatusSucceeded {
				t.Fatalf("status = %v, want StatusSucceeded", result.Status)
			}
			if len(result.Attempts) != 3 {
				t.Fatalf("attempt history = %d, want 3", len(result.Attempts))
			}
			for i, record := range result.Attempts {
				if record.Attempt.Number != i+1 {
					t.Fatalf("history order broken at %d: %+v", i, record)
				}
			}
			if result.Attempts[0].Err != errInfra || result.Attempts[1].Err != errInfra {
				t.Fatalf("retry errors not preserved: %+v", result.Attempts)
			}
			if !result.Attempts[2].OK {
				t.Fatalf("last attempt not recorded as success")
			}
			for k, count := range seen {
				if count != 1 {
					t.Fatalf("attempt %s executed %d times, want 1", k, count)
				}
			}
			return
		default:
			t.Fatalf("unexpected attempt number %d", lease.Attempt.Number)
		}
	}
}
func TestCrashIsTerminalAndNotMaskedByRetry(t *testing.T) {
	classifier := newScriptedClassifier()
	// An infrastructure failure on attempt 1 followed by a crash on attempt 2:
	// the crash class must terminate the sample immediately, with the crash
	// retained and no further retry.
	classifier.set("s", 1, netsec.ClassInfrastructure)
	classifier.set("s", 2, netsec.ClassCrash)
	q := netsec.NewQueue(classifier, 5)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: "s"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ctx := context.Background()
	lease, err := q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim 1: %v", err)
	}
	if err := q.Complete(lease, false, errInfra); err != nil {
		t.Fatalf("Complete 1: %v", err)
	}
	lease, err = q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if lease.Attempt.Number != 2 {
		t.Fatalf("second attempt number = %d, want 2", lease.Attempt.Number)
	}
	if err := q.Complete(lease, false, errCrash); err != nil {
		t.Fatalf("Complete 2: %v", err)
	}

	result := q.Result("s")
	if result == nil {
		t.Fatal("missing terminal result")
	}
	if result.Status != netsec.StatusCrashed {
		t.Fatalf("status = %v, want StatusCrashed", result.Status)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("history = %d, want 2 attempts retained", len(result.Attempts))
	}
	if result.Attempts[1].Err != errCrash {
		t.Fatalf("crash error not retained: %+v", result.Attempts[1])
	}
	if q.Len() != 0 {
		t.Fatalf("crash sample must not be re-enqueued, Len = %d", q.Len())
	}
}

func TestInfrastructureFailuresAreBounded(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassInfrastructure
	}), 3)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: "s"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ctx := context.Background()
	executed := &sync.Map{}
	for i := 0; i < 3; i++ {
		lease, err := q.Claim(ctx)
		if err != nil {
			t.Fatalf("Claim %d: %v", i+1, err)
		}
		if lease.Attempt.Number != i+1 {
			t.Fatalf("attempt number = %d, want %d", lease.Attempt.Number, i+1)
		}
		if _, loaded := executed.LoadOrStore(attemptKey(lease.Attempt), struct{}{}); loaded {
			t.Fatalf("attempt %s repeated", attemptKey(lease.Attempt))
		}
		if err := q.Complete(lease, false, errInfra); err != nil {
			t.Fatalf("Complete %d: %v", i+1, err)
		}
	}

	result := q.Result("s")
	if result == nil {
		t.Fatal("missing terminal result")
	}
	if result.Status != netsec.StatusInfrastructureExhausted {
		t.Fatalf("status = %v, want StatusInfrastructureExhausted", result.Status)
	}
	if len(result.Attempts) != 3 {
		t.Fatalf("history = %d, want 3", len(result.Attempts))
	}
	if q.Len() != 0 {
		t.Fatalf("budget exhausted sample must not remain queued, Len = %d", q.Len())
	}
}

func TestUnspecifiedFailureIsTerminal(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassUnspecified
	}), 3)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: "s"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	lease, err := q.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Complete(lease, false, errors.New("mystery")); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	result := q.Result("s")
	if result == nil || result.Status != netsec.StatusFailed {
		t.Fatalf("got %+v, want StatusFailed", result)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("history = %d, want 1", len(result.Attempts))
	}
}
func TestPriorityAndEnqueueOrder(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassCrash
	}), 1)
	defer q.Close()

	order := []string{"low-1", "low-2", "high-1", "low-3", "high-2"}
	priorities := map[string]int{
		"low-1": 0, "low-2": 0, "low-3": 0,
		"high-1": 1, "high-2": 1,
	}
	for _, id := range order {
		if err := q.Enqueue(netsec.Sample{ID: id, Priority: priorities[id]}); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}

	want := []string{"high-1", "high-2", "low-1", "low-2", "low-3"}
	ctx := context.Background()
	for i, wantID := range want {
		lease, err := q.Claim(ctx)
		if err != nil {
			t.Fatalf("Claim %d: %v", i, err)
		}
		if lease.Sample.ID != wantID {
			t.Fatalf("claim %d = %s, want %s", i, lease.Sample.ID, wantID)
		}
		if err := q.Complete(lease, false, errCrash); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
}

func TestRetryDoesNotStarveNewSamples(t *testing.T) {
	classifier := newScriptedClassifier()
	classifier.set("old", 1, netsec.ClassInfrastructure)
	classifier.set("old", 2, netsec.ClassCrash)
	q := netsec.NewQueue(classifier, 3)
	defer q.Close()

	ctx := context.Background()
	// old is admitted first, then two fresh samples at the same priority. All
	// three are waiting before old's first attempt runs.
	for _, id := range []string{"old", "new-1", "new-2"} {
		if err := q.Enqueue(netsec.Sample{ID: id, Priority: 10}); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}

	// old runs first (earliest enqueue tick) and hits an infrastructure fault.
	lease, err := q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim old: %v", err)
	}
	if lease.Sample.ID != "old" || lease.Attempt.Number != 1 {
		t.Fatalf("first claim = %s#%d, want old#1", lease.Sample.ID, lease.Attempt.Number)
	}
	if err := q.Complete(lease, false, errInfra); err != nil {
		t.Fatalf("Complete old: %v", err)
	}

	// The retry must re-enter behind new-1 and new-2 instead of keeping old's
	// original head position (which would starve every later admission).

	got := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		lease, err := q.Claim(ctx)
		if err != nil {
			t.Fatalf("Claim %d: %v", i, err)
		}
		got = append(got, lease.Sample.ID)
		if lease.Sample.ID == "old" && lease.Attempt.Number != 2 {
			t.Fatalf("old sample retry attempt = %d, want 2", lease.Attempt.Number)
		}
		if lease.Sample.ID == "old" {
			if err := q.Complete(lease, false, errCrash); err != nil {
				t.Fatalf("Complete old: %v", err)
			}
			continue
		}
		if err := q.Complete(lease, true, nil); err != nil {
			t.Fatalf("Complete %s: %v", lease.Sample.ID, err)
		}
	}

	want := []string{"new-1", "new-2", "old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim order = %v, want %v (retry must tail behind new samples)", got, want)
		}
	}
	result := q.Result("old")
	if result == nil || result.Status != netsec.StatusCrashed {
		t.Fatalf("old result = %+v, want StatusCrashed with full history", result)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("old history = %d, want 2", len(result.Attempts))
	}
}
func TestConcurrentClaimsNeverDuplicateAnAttempt(t *testing.T) {
	const sampleCount = 200
	const workerCount = 16

	// Classification flips between attempts: every odd attempt looks like an
	// infrastructure fault and is retried, every even attempt succeeds. This
	// exercises the re-enqueue path under heavy concurrent claim/complete
	// traffic while proving no (sample, attempt) pair is ever executed twice.
	classifier := netsec.ClassifierFunc(func(attempt netsec.Attempt, cause error) netsec.FailureClass {
		if attempt.Number%2 == 1 {
			return netsec.ClassInfrastructure
		}
		return netsec.ClassUnspecified
	})
	q := netsec.NewQueue(classifier, 2)

	for i := 0; i < sampleCount; i++ {
		if err := q.Enqueue(netsec.Sample{ID: fmt.Sprintf("sample-%d", i)}); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	ctx := context.Background()
	executed := &sync.Map{}
	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runWorker(ctx, t, q, executed, func(lease *netsec.Lease) (bool, error) {
				if lease.Attempt.Number == 2 {
					return true, nil
				}
				return false, errInfra
			})
		}()
	}

	deadline := time.Now().Add(10 * time.Second)
	for len(q.Results()) < sampleCount {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d samples finished before deadline", len(q.Results()), sampleCount)
		}
		time.Sleep(time.Millisecond)
	}
	q.Close()
	wg.Wait()

	results := q.Results()
	if len(results) != sampleCount {
		t.Fatalf("results = %d, want %d", len(results), sampleCount)
	}
	for _, result := range results {
		if result.Status != netsec.StatusSucceeded {
			t.Fatalf("sample %s status = %v, want StatusSucceeded", result.Sample.ID, result.Status)
		}
		if len(result.Attempts) != 2 {
			t.Fatalf("sample %s attempts = %d, want 2", result.Sample.ID, len(result.Attempts))
		}
		for _, record := range result.Attempts {
			k := attemptKey(record.Attempt)
			count := 0
			executed.Range(func(key, value any) bool {
				if key == k {
					count++
				}
				return true
			})
			if count != 1 {
				t.Fatalf("attempt %s observed %d times, want exactly 1", k, count)
			}
		}
	}

	totalExecutions := 0
	executed.Range(func(any, any) bool { totalExecutions++; return true })
	if totalExecutions != sampleCount*2 {
		t.Fatalf("total executions = %d, want %d", totalExecutions, sampleCount*2)
	}
}

func TestDoubleCompleteAndForeignLeaseRejected(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassInfrastructure
	}), 3)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: "a"}); err != nil {
		t.Fatalf("Enqueue a: %v", err)
	}
	if err := q.Enqueue(netsec.Sample{ID: "b"}); err != nil {
		t.Fatalf("Enqueue b: %v", err)
	}
	ctx := context.Background()
	leaseA, err := q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim a: %v", err)
	}
	leaseB, err := q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim b: %v", err)
	}

	// A fabricated lease for an attempt that was never issued.
	forged := &netsec.Lease{Attempt: netsec.Attempt{SampleID: "a", Number: 99}}
	if err := q.Complete(forged, true, nil); !errors.Is(err, netsec.ErrLeaseNotHeld) {
		t.Fatalf("forged lease: want ErrLeaseNotHeld, got %v", err)
	}
	// A lease copy carries the same token: completing it once must consume the
	// attempt, and any second presentation (including the original) is rejected
	// so the attempt is never executed/recorded twice.
	tampered := &netsec.Lease{Attempt: leaseA.Attempt, Sample: leaseA.Sample}
	if err := q.Complete(tampered, false, errInfra); err != nil {
		t.Fatalf("first completion via lease copy: %v", err)
	}
	if err := q.Complete(leaseA, true, nil); !errors.Is(err, netsec.ErrLeaseNotHeld) {
		t.Fatalf("replayed lease: want ErrLeaseNotHeld, got %v", err)
	}
	if err := q.Complete(tampered, true, nil); !errors.Is(err, netsec.ErrLeaseNotHeld) {
		t.Fatalf("second lease copy: want ErrLeaseNotHeld, got %v", err)
	}

	if err := q.Complete(nil, true, nil); !errors.Is(err, netsec.ErrLeaseNotHeld) {
		t.Fatalf("nil lease: want ErrLeaseNotHeld, got %v", err)
	}

	// The retry from the first failure is a fresh attempt, not a replay.
	retry, err := q.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim retry: %v", err)
	}
	if retry.Sample.ID != "a" || retry.Attempt.Number != 2 {
		t.Fatalf("retry lease = %s#%d, want a#2", retry.Sample.ID, retry.Attempt.Number)
	}
	if err := q.Complete(retry, true, nil); err != nil {
		t.Fatalf("Complete retry: %v", err)
	}
	if err := q.Complete(leaseB, true, nil); err != nil {
		t.Fatalf("lease B should still be completable once: %v", err)
	}
}
func TestClaimBlocksAndRespectsContextCancellation(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassCrash
	}), 1)
	defer q.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := q.Claim(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("empty queue Claim: want DeadlineExceeded, got %v", err)
	}

	// A later enqueue must still unblock a fresh claim on the same queue.
	ready, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	errCh := make(chan error, 1)
	go func() {
		_, err := q.Claim(ready)
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := q.Enqueue(netsec.Sample{ID: "late"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("blocked Claim after Enqueue: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Claim was not woken by Enqueue")
	}
}

func TestCloseDrainsQueuedSamplesAndStopsWaiters(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassCrash
	}), 1)
	if err := q.Enqueue(netsec.Sample{ID: "s"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Before Close, Wait with an empty result buffer can be canceled cleanly.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := q.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait: want DeadlineExceeded, got %v", err)
	}

	lease, err := q.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim queued sample after Close setup: %v", err)
	}
	if err := q.Complete(lease, true, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Close()
	if _, err := q.Claim(context.Background()); !errors.Is(err, netsec.ErrQueueClosed) {
		t.Fatalf("Claim on drained closed queue: want ErrQueueClosed, got %v", err)
	}
	// Wait still serves the buffered terminal result after Close.
	count, err := q.Wait(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("Wait after Close: got count=%d err=%v, want 1 nil", count, err)
	}
	if err := q.Enqueue(netsec.Sample{ID: "x"}); !errors.Is(err, netsec.ErrQueueClosed) {
		t.Fatalf("Enqueue after Close: want ErrQueueClosed, got %v", err)
	}
	if r := q.Result("s"); r == nil || r.Status != netsec.StatusSucceeded {
		t.Fatalf("finished result lost after Close: %+v", r)
	}
	// Close is idempotent.
	q.Close()

	// With no buffered results, Close must unblock a waiting Claim/Wait with
	// ErrQueueClosed.
	empty := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassCrash
	}), 1)
	empty.Close()
	if _, err := empty.Claim(context.Background()); !errors.Is(err, netsec.ErrQueueClosed) {
		t.Fatalf("Claim on empty closed queue: want ErrQueueClosed, got %v", err)
	}
	if _, err := empty.Wait(context.Background()); !errors.Is(err, netsec.ErrQueueClosed) {
		t.Fatalf("Wait on empty closed queue: want ErrQueueClosed, got %v", err)
	}
}

func TestMaxAttemptsOneMeansNoRetries(t *testing.T) {
	q := netsec.NewQueue(netsec.ClassifierFunc(func(netsec.Attempt, error) netsec.FailureClass {
		return netsec.ClassInfrastructure
	}), 1)
	defer q.Close()

	if err := q.Enqueue(netsec.Sample{ID: "s"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	lease, err := q.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Complete(lease, false, errInfra); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	result := q.Result("s")
	if result == nil || result.Status != netsec.StatusInfrastructureExhausted {
		t.Fatalf("got %+v, want immediate exhaustion", result)
	}
	if q.Len() != 0 {
		t.Fatalf("no retry allowed with budget 1, Len = %d", q.Len())
	}
}
