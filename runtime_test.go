package detrun_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"detrun"
)

// sink collects per-task observations so record and replay runs can be
// compared for identical results.
type sink struct {
	mu      sync.Mutex
	entries []string
}

func (s *sink) add(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, fmt.Sprintf(format, args...))
}

func (s *sink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	copy(out, s.entries)
	return out
}

// external is a test-injected external service: echoes the request, fails on
// "B2".
func external(req string) (string, error) {
	if req == "B2" {
		return "", errors.New("injected failure: B2 rejected")
	}
	return "resp:" + req, nil
}

// workload registers the standard two-task scenario on rt.
func workload(rt *detrun.Runtime, s *sink) {
	rt.Go(func(t *detrun.Task) error {
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		r1, err := t.Call("A1")
		s.add("A A1 -> %q err=%v", r1, err)
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		r2, err := t.Call("A2")
		s.add("A A2 -> %q err=%v", r2, err)
		return nil
	})
	rt.Go(func(t *detrun.Task) error {
		r1, err := t.Call("B1")
		s.add("B B1 -> %q err=%v", r1, err)
		if err := t.Sleep(20 * time.Millisecond); err != nil {
			return err
		}
		r2, err := t.Call("B2")
		s.add("B B2 -> %q err=%v", r2, err)
		return nil
	})
}

func recordWorkload(t *testing.T) ([]detrun.Event, *sink) {
	t.Helper()
	rt := detrun.NewRecord(external)
	s := &sink{}
	workload(rt, s)
	if err := rt.Run(); err != nil {
		t.Fatalf("record run: %v", err)
	}
	return rt.Log(), s
}

func TestRecordThenReplaySameResults(t *testing.T) {
	log, recordSink := recordWorkload(t)
	if len(log) == 0 {
		t.Fatal("empty recording")
	}

	rt := detrun.NewReplay(log)
	replaySink := &sink{}
	workload(rt, replaySink)
	if err := rt.Run(); err != nil {
		t.Fatalf("replay run: %v", err)
	}

	got, want := replaySink.snapshot(), recordSink.snapshot()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("replay results differ:\nrecord: %v\nreplay: %v", want, got)
	}
}

func TestRecordIsDeterministic(t *testing.T) {
	log1, sink1 := recordWorkload(t)
	log2, sink2 := recordWorkload(t)
	if fmt.Sprint(log1) != fmt.Sprint(log2) {
		t.Fatalf("two recordings differ:\n%v\n%v", log1, log2)
	}
	if fmt.Sprint(sink1.snapshot()) != fmt.Sprint(sink2.snapshot()) {
		t.Fatal("two record runs produced different results")
	}
}

func TestReplayDetectsExtraRequest(t *testing.T) {
	log, _ := recordWorkload(t)

	rt := detrun.NewReplay(log)
	rt.Go(func(t *detrun.Task) error {
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		if _, err := t.Call("A1"); err != nil {
			return err
		}
		if _, err := t.Call("A-EXTRA"); err != nil { // not in the recording
			return err
		}
		return nil
	})
	rt.Go(func(t *detrun.Task) error {
		if _, err := t.Call("B1"); err != nil {
			return err
		}
		return t.Sleep(20 * time.Millisecond)
	})

	err := rt.Run()
	if err == nil {
		t.Fatal("extra request was not reported")
	}
	var d *detrun.DivergenceError
	if !errors.As(err, &d) {
		t.Fatalf("expected DivergenceError, got %v", err)
	}
	// The extra call must be flagged at its own position, i.e. right after
	// the schedule event that resumed task 0 following the recorded A1 call.
	wantPos := indexOf(t, log, func(e detrun.Event) bool {
		return e.Kind == detrun.KindExternal && e.Req == "A1"
	}) + 2 // external(A1), then schedule(0); divergence is next
	if d.Pos != wantPos {
		t.Fatalf("divergence at pos %d, want %d (log: %v)", d.Pos, wantPos, log)
	}
	if !strings.Contains(err.Error(), `req="A-EXTRA"`) {
		t.Fatalf("error does not name the extra request: %v", err)
	}
}

func TestReplayDetectsRequestPastEndOfRecording(t *testing.T) {
	log, _ := recordWorkload(t)

	// Task A follows the recording exactly; task B issues one more request
	// after the recorded workload is over, i.e. past the end of the log.
	rt := detrun.NewReplay(log)
	rt.Go(func(t *detrun.Task) error {
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		if _, err := t.Call("A1"); err != nil {
			return err
		}
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		_, err := t.Call("A2")
		return err
	})
	rt.Go(func(t *detrun.Task) error {
		if _, err := t.Call("B1"); err != nil {
			return err
		}
		if err := t.Sleep(20 * time.Millisecond); err != nil {
			return err
		}
		if _, err := t.Call("B2"); err == nil {
			return errors.New("expected injected B2 failure")
		}
		_, err := t.Call("B3") // beyond the recorded tail
		return err
	})

	err := rt.Run()
	var d *detrun.DivergenceError
	if !errors.As(err, &d) {
		t.Fatalf("expected DivergenceError, got %v", err)
	}
	if !strings.Contains(d.Expected, "end of recording") {
		t.Fatalf("expected end-of-recording divergence, got %v", err)
	}
}

func TestReplayFailsOnMissingTail(t *testing.T) {
	log, _ := recordWorkload(t)

	// Task B never issues its final recorded B2 call, so the run ends with
	// the recording's tail unconsumed. That must not be reported as success.
	rt := detrun.NewReplay(log)
	rt.Go(func(t *detrun.Task) error {
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		if _, err := t.Call("A1"); err != nil {
			return err
		}
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		_, err := t.Call("A2")
		return err
	})
	rt.Go(func(t *detrun.Task) error {
		if _, err := t.Call("B1"); err != nil {
			return err
		}
		return t.Sleep(20 * time.Millisecond)
	})

	err := rt.Run()
	if err == nil {
		t.Fatal("replay succeeded despite missing recording tail")
	}
	var d *detrun.DivergenceError
	if !errors.As(err, &d) {
		t.Fatalf("expected DivergenceError, got %v", err)
	}
	if !strings.Contains(err.Error(), "unconsumed") {
		t.Fatalf("error does not mention unconsumed tail: %v", err)
	}
}

func TestReplayDetectsClockDivergence(t *testing.T) {
	log, _ := recordWorkload(t)

	rt := detrun.NewReplay(log)
	rt.Go(func(t *detrun.Task) error {
		if err := t.Sleep(6 * time.Millisecond); err != nil { // recorded: 5ms
			return err
		}
		_, err := t.Call("A1")
		return err
	})
	rt.Go(func(t *detrun.Task) error {
		if _, err := t.Call("B1"); err != nil {
			return err
		}
		return t.Sleep(20 * time.Millisecond)
	})

	err := rt.Run()
	var d *detrun.DivergenceError
	if !errors.As(err, &d) {
		t.Fatalf("expected DivergenceError, got %v", err)
	}
	if !strings.Contains(d.Expected, "clock") {
		t.Fatalf("expected clock divergence, got %v", err)
	}
	if !strings.Contains(err.Error(), "5ms") || !strings.Contains(err.Error(), "6ms") {
		t.Fatalf("error should show both virtual times: %v", err)
	}
}

func TestReplayDetectsScheduleDivergence(t *testing.T) {
	log, _ := recordWorkload(t)

	// Replay only task B's body. The recording expects the scheduler's
	// second decision (resume task 1) at position 1; this run instead
	// issues an external request there.
	rt := detrun.NewReplay(log)
	rt.Go(func(t *detrun.Task) error {
		_, err := t.Call("B1")
		return err
	})

	err := rt.Run()
	var d *detrun.DivergenceError
	if !errors.As(err, &d) {
		t.Fatalf("expected DivergenceError, got %v", err)
	}
	if d.Pos != 1 {
		t.Fatalf("schedule divergence should be at pos 1, got %d", d.Pos)
	}
	if !strings.Contains(d.Expected, "schedule") {
		t.Fatalf("expected schedule divergence, got %v", err)
	}
}

func TestReplayInjectsRecordedFailure(t *testing.T) {
	log, recordSink := recordWorkload(t)

	// Replay must not need any external func at all; the recorded failure
	// for B2 is injected from the log.
	rt := detrun.NewReplay(log)
	replaySink := &sink{}
	workload(rt, replaySink)
	if err := rt.Run(); err != nil {
		t.Fatalf("replay run: %v", err)
	}

	entries := replaySink.snapshot()
	var b2 string
	for _, e := range entries {
		if strings.Contains(e, "B2") {
			b2 = e
		}
	}
	if !strings.Contains(b2, "injected failure: B2 rejected") {
		t.Fatalf("recorded failure not injected in replay: %q (all: %v)", b2, entries)
	}
	if fmt.Sprint(entries) != fmt.Sprint(recordSink.snapshot()) {
		t.Fatal("replay observations differ from record observations")
	}
}

func TestReplayNeverCallsExternal(t *testing.T) {
	log, _ := recordWorkload(t)
	rt := detrun.NewReplay(log)
	s := &sink{}
	workload(rt, s)
	if err := rt.Run(); err != nil {
		t.Fatalf("replay run: %v", err)
	}
	// NewReplay has no ExternalFunc parameter at all; reaching here proves
	// the run was served purely from the recording.
}

func TestVirtualClockDoesNotUseWallTime(t *testing.T) {
	rt := detrun.NewRecord(nil)
	rt.Go(func(t *detrun.Task) error {
		return t.Sleep(10 * time.Second) // virtual: must not delay the test
	})
	start := time.Now()
	if err := rt.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("virtual sleep leaked into wall time: %v", elapsed)
	}
	if rt.Now() != 10*time.Second {
		t.Fatalf("virtual clock = %v, want 10s", rt.Now())
	}
}

func TestConcurrentRecordAndReplay(t *testing.T) {
	log, want := recordWorkload(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt := detrun.NewReplay(log)
			s := &sink{}
			workload(rt, s)
			if err := rt.Run(); err != nil {
				t.Errorf("replay: %v", err)
			}
			if fmt.Sprint(s.snapshot()) != fmt.Sprint(want.snapshot()) {
				t.Errorf("concurrent replay results differ")
			}
		}()
	}
	wg.Wait()
}

func indexOf(t *testing.T, log []detrun.Event, pred func(detrun.Event) bool) int {
	t.Helper()
	for i, e := range log {
		if pred(e) {
			return i
		}
	}
	t.Fatalf("event not found in log: %v", log)
	return -1
}
