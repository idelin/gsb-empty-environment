package msgproc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func okHandler(ctx context.Context, payload []byte) (interface{}, error) {
	return "done:" + string(payload), nil
}

func TestDuplicateAckReturnsSameTicket(t *testing.T) {
	p := NewProcessor(NewStore(), okHandler, nil, time.Second)
	id1, err := p.Ack("m-1", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	id2, err := p.Ack("m-1", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("duplicate ack returned different tickets: %s vs %s", id1, id2)
	}
	st, _ := p.Query(id1)
	if st.Phase != PhaseAcked {
		t.Fatalf("phase = %v, want ACKED (ack != business success)", st.Phase)
	}
}

func TestRetryPreservesAttemptHistory(t *testing.T) {
	calls := 0
	handler := func(ctx context.Context, payload []byte) (interface{}, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("transient error")
		}
		return "ok", nil
	}
	p := NewProcessor(NewStore(), handler, nil, time.Second)
	id, _ := p.Ack("m-2", []byte("x"))

	for i := 0; i < 2; i++ {
		if err := p.Execute(context.Background(), id); err == nil {
			t.Fatal("expected failure")
		}
	}
	if err := p.Execute(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	st, _ := p.Query(id)
	if st.Phase != PhaseSucceeded {
		t.Fatalf("phase = %v, want SUCCEEDED", st.Phase)
	}
	if len(st.Attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(st.Attempts))
	}
	for i, a := range st.Attempts {
		if a.Seq != i+1 {
			t.Fatalf("attempt %d seq = %d", i, a.Seq)
		}
	}
	if st.Attempts[0].Outcome != OutcomeFailure || st.Attempts[1].Outcome != OutcomeFailure {
		t.Fatal("early attempts should be recorded as FAILURE")
	}
	if st.Attempts[2].Outcome != OutcomeSuccess {
		t.Fatal("last attempt should be SUCCESS")
	}
	if st.Attempts[0].Err != "transient error" {
		t.Fatalf("attempt error not preserved: %q", st.Attempts[0].Err)
	}
}

func TestCrashAfterAckThenReAckSameTicket(t *testing.T) {
	store := NewStore()
	faults := &Faults{CrashAfterAck: true}
	p := NewProcessor(store, okHandler, faults, time.Second)

	if _, err := p.Ack("m-3", []byte("x")); err != ErrCrashAfterAck {
		t.Fatalf("err = %v, want ErrCrashAfterAck", err)
	}

	// 模拟进程重启：同一 Store，新的 Processor。
	faults.CrashAfterAck = false
	p2 := NewProcessor(store, okHandler, faults, time.Second)
	id, err := p2.Ack("m-3", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := p2.Query(id)
	if st.Phase != PhaseAcked {
		t.Fatalf("phase = %v, want ACKED after crash+re-ack", st.Phase)
	}
	if len(st.Attempts) != 0 {
		t.Fatal("no business attempt should exist before Execute")
	}
}

func TestRecoverMarksInterruptedAttempt(t *testing.T) {
	store := NewStore()
	started := make(chan struct{})
	block := make(chan struct{})
	handler := func(ctx context.Context, payload []byte) (interface{}, error) {
		close(started)
		<-block
		return nil, nil
	}
	p := NewProcessor(store, handler, nil, 10*time.Second)
	id, _ := p.Ack("m-4", []byte("x"))
	go p.Execute(context.Background(), id)
	<-started

	// 执行中“崩溃”，重启并恢复。
	p2 := NewProcessor(store, okHandler, nil, time.Second)
	p2.Recover()
	close(block)

	st, _ := p2.Query(id)
	if st.Phase != PhaseUnknown {
		t.Fatalf("phase = %v, want UNKNOWN after crash during execution", st.Phase)
	}
	if len(st.Attempts) != 1 || st.Attempts[0].Outcome != OutcomeInterrupted {
		t.Fatalf("attempt should be INTERRUPTED, got %+v", st.Attempts)
	}
}

func TestBusinessTimeoutYieldsUnknownPhase(t *testing.T) {
	faults := &Faults{BusinessTimeout: true}
	p := NewProcessor(NewStore(), okHandler, faults, 50*time.Millisecond)
	id, _ := p.Ack("m-5", []byte("x"))

	if err := p.Execute(context.Background(), id); err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	st, _ := p.Query(id)
	if st.Phase != PhaseUnknown {
		t.Fatalf("phase = %v, want UNKNOWN (timeout != failure)", st.Phase)
	}
	if st.Attempts[0].Outcome != OutcomeTimeout {
		t.Fatalf("outcome = %v, want TIMEOUT", st.Attempts[0].Outcome)
	}
}

func TestResultLossDistinguishable(t *testing.T) {
	faults := &Faults{ResultLoss: true}
	p := NewProcessor(NewStore(), okHandler, faults, time.Second)
	id, _ := p.Ack("m-6", []byte("x"))

	if err := p.Execute(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	st, _ := p.Query(id)
	if st.Phase != PhaseSucceeded {
		t.Fatalf("phase = %v, want SUCCEEDED", st.Phase)
	}
	if !st.ResultLost {
		t.Fatal("ResultLost should be true")
	}
	if _, err := p.Result(id); err != ErrResultLost {
		t.Fatalf("err = %v, want ErrResultLost", err)
	}
}

func TestCallerDistinguishesThreePhases(t *testing.T) {
	store := NewStore()
	p := NewProcessor(store, okHandler, nil, time.Second)
	id, _ := p.Ack("m-7", []byte("x"))

	st, _ := p.Query(id)
	if st.Phase != PhaseAcked {
		t.Fatalf("stage 1: phase = %v, want ACKED", st.Phase)
	}

	if err := p.Execute(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	st, _ = p.Query(id)
	if st.Phase != PhaseSucceeded {
		t.Fatalf("stage 2: phase = %v, want SUCCEEDED", st.Phase)
	}

	res, err := p.Result(id)
	if err != nil {
		t.Fatal(err)
	}
	if res != "done:x" {
		t.Fatalf("stage 3: result = %v", res)
	}
}
