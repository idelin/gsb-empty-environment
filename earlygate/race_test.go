package earlygate_test

import (
	"errors"
	"io"
	"sync"
	"testing"

	"example.org/netsec/q0165/earlygate"
)

func TestEventOrdering_AckTransitions(t *testing.T) {
	t.Run("ack before confirm", func(t *testing.T) {
		gate, _ := newTestGate(t)
		gate.SubmitBatch(1, []testOp{{name: "a", kind: "read"}, {name: "b", kind: "write"}})
		if err := gate.AckEarlyExecution(1); err != nil {
			t.Fatalf("ack: %v", err)
		}
		if _, err := gate.Confirm(); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		info, _ := gate.Request(1)
		if info.Status != earlygate.StatusEarlyConfirmed {
			t.Fatalf("status = %s, want EarlyConfirmed", info.Status)
		}
	})

	t.Run("ack after confirm", func(t *testing.T) {
		gate, _ := newTestGate(t)
		gate.SubmitBatch(1, []testOp{{name: "a", kind: "read"}})
		if _, err := gate.Confirm(); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		if err := gate.AckEarlyExecution(1); err != nil {
			t.Fatalf("ack: %v", err)
		}
		info, _ := gate.Request(1)
		if info.Status != earlygate.StatusEarlyConfirmed {
			t.Fatalf("status = %s, want EarlyConfirmed", info.Status)
		}
	})

	t.Run("ack after reject stays unconfirmed", func(t *testing.T) {
		gate, _ := newTestGate(t)
		gate.SubmitBatch(1, []testOp{{name: "a", kind: "read"}})
		if _, err := gate.Reject(nil); err != nil {
			t.Fatalf("reject: %v", err)
		}
		if err := gate.AckEarlyExecution(1); err != nil {
			t.Fatalf("ack: %v", err)
		}
		info, _ := gate.Request(1)
		if info.Status != earlygate.StatusEarlyUnconfirmed || !info.EarlyFinished {
			t.Fatalf("status = %s finished=%v, want EarlyUnconfirmed+finished", info.Status, info.EarlyFinished)
		}
	})

	t.Run("ack unknown or non-early request", func(t *testing.T) {
		gate, _ := newTestGate(t)
		gate.SubmitBatch(1, []testOp{{name: "a", kind: "read"}, {name: "b", kind: "write"}})
		if err := gate.AckEarlyExecution(99); !errors.Is(err, earlygate.ErrUnknownRequest) {
			t.Fatalf("unknown ack err = %v, want ErrUnknownRequest", err)
		}
		if err := gate.AckEarlyExecution(2); !errors.Is(err, earlygate.ErrInvalidTransition) {
			t.Fatalf("held ack err = %v, want ErrInvalidTransition", err)
		}
	})
}

func TestCancel_OnlyQueuedRequests(t *testing.T) {
	gate, rec := newTestGate(t)
	gate.SubmitBatch(1, []testOp{
		{name: "a", kind: "write"},
		{name: "b", kind: "read"},
	})

	if err := gate.Cancel(1); err != nil {
		t.Fatalf("Cancel(1): %v", err)
	}
	info, _ := gate.Request(1)
	if info.Status != earlygate.StatusCanceled {
		t.Fatalf("request 1 status = %s, want Canceled", info.Status)
	}

	// 已取消的请求不能再次取消，也不会在确认时被派发。
	if err := gate.Cancel(1); !errors.Is(err, earlygate.ErrInvalidTransition) {
		t.Fatalf("second cancel err = %v, want ErrInvalidTransition", err)
	}
	confirmed, err := gate.Confirm()
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(confirmed) != 0 {
		t.Fatalf("confirm decisions = %v, want none (only held request was canceled)", confirmed)
	}

	// 早期执行中的请求不可取消，避免宿主误以为副作用未发生。
	if err := gate.Cancel(2); !errors.Is(err, earlygate.ErrInvalidTransition) {
		t.Fatalf("cancel early request err = %v, want ErrInvalidTransition", err)
	}
	if err := gate.Cancel(42); !errors.Is(err, earlygate.ErrUnknownRequest) {
		t.Fatalf("cancel unknown err = %v, want ErrUnknownRequest", err)
	}

	all := rec.snapshot()
	var sawCancel bool
	for _, d := range all {
		if v, ok := d.(earlygate.CancelAck[testOp]); ok && v.RequestID == 1 {
			sawCancel = true
		}
	}
	if !sawCancel {
		t.Fatalf("decision stream missing CancelAck for request 1: %#v", all)
	}
}

func TestConfirmCancelRace_ExactlyOneOutcomePerRequest(t *testing.T) {
	const runs = 200
	for run := 0; run < runs; run++ {
		gate, rec := newTestGate(t)
		gate.SubmitBatch(1, []testOp{
			{name: "a", kind: "write"},
			{name: "b", kind: "authget"},
		})

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var confirmErr, cancelErr error
		go func() {
			defer wg.Done()
			<-start
			_, confirmErr = gate.Confirm()
		}()
		go func() {
			defer wg.Done()
			<-start
			cancelErr = gate.Cancel(1)
		}()
		close(start)
		wg.Wait()

		state := gate.HandshakeState()
		if state != earlygate.HandshakeConfirmed {
			t.Fatalf("run %d state = %s, want Confirmed", run, state)
		}
		if confirmErr != nil {
			t.Fatalf("run %d confirm err = %v", run, confirmErr)
		}

		info, _ := gate.Request(1)
		switch info.Status {
		case earlygate.StatusDispatched:
			if cancelErr == nil || !errors.Is(cancelErr, earlygate.ErrInvalidTransition) {
				t.Fatalf("run %d dispatched but cancel err = %v", run, cancelErr)
			}
		case earlygate.StatusCanceled:
			if cancelErr != nil {
				t.Fatalf("run %d canceled but cancel err = %v", run, cancelErr)
			}
		default:
			t.Fatalf("run %d request 1 status = %s, want Dispatched or Canceled", run, info.Status)
		}

		// 任何请求在整个决策流中至多出现一次终态决策，且早期/普通集合不相交。
		all := rec.snapshot()
		terminal := map[uint64]int{}
		early := map[uint64]bool{}
		normal := map[uint64]bool{}
		for _, d := range all {
			switch v := d.(type) {
			case earlygate.DispatchNormal[testOp]:
				terminal[v.RequestID]++
				normal[v.RequestID] = true
			case earlygate.CancelAck[testOp]:
				terminal[v.RequestID]++
			case earlygate.RejectRequest[testOp]:
				terminal[v.RequestID]++
			case earlygate.ExecuteEarly[testOp]:
				early[v.RequestID] = true
			}
		}
		for id, count := range terminal {
			if count > 1 {
				t.Fatalf("run %d request %d has %d terminal decisions", run, id, count)
			}
		}
		for id := range early {
			if normal[id] {
				t.Fatalf("run %d request %d both early and normal dispatched", run, id)
			}
		}
	}
}

func TestConfirmRejectRace_OnlyFirstSettlementWins(t *testing.T) {
	const runs = 200
	for run := 0; run < runs; run++ {
		gate, _ := newTestGate(t)
		gate.SubmitBatch(1, []testOp{{name: "a", kind: "write"}, {name: "b", kind: "read"}})

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var confirmErr, rejectErr error
		go func() {
			defer wg.Done()
			<-start
			_, confirmErr = gate.Confirm()
		}()
		go func() {
			defer wg.Done()
			<-start
			_, rejectErr = gate.Reject(errors.New("boom"))
		}()
		close(start)
		wg.Wait()

		state := gate.HandshakeState()
		switch state {
		case earlygate.HandshakeConfirmed:
			if confirmErr != nil || !errors.Is(rejectErr, earlygate.ErrHandshakeSettled) {
				t.Fatalf("run %d confirmed: confirmErr=%v rejectErr=%v", run, confirmErr, rejectErr)
			}
		case earlygate.HandshakeRejected:
			if rejectErr != nil || !errors.Is(confirmErr, earlygate.ErrHandshakeSettled) {
				t.Fatalf("run %d rejected: confirmErr=%v rejectErr=%v", run, confirmErr, rejectErr)
			}
		default:
			t.Fatalf("run %d unexpected state %s", run, state)
		}

		writeInfo, _ := gate.Request(1)
		readInfo, _ := gate.Request(2)
		switch state {
		case earlygate.HandshakeConfirmed:
			if writeInfo.Status != earlygate.StatusDispatched {
				t.Fatalf("run %d write status = %s, want Dispatched", run, writeInfo.Status)
			}
			if readInfo.Status != earlygate.StatusEarlyExecuting {
				t.Fatalf("run %d read status = %s, want EarlyExecuting", run, readInfo.Status)
			}
		case earlygate.HandshakeRejected:
			if writeInfo.Status != earlygate.StatusRejected {
				t.Fatalf("run %d write status = %s, want Rejected", run, writeInfo.Status)
			}
			if readInfo.Status != earlygate.StatusEarlyUnconfirmed {
				t.Fatalf("run %d read status = %s, want EarlyUnconfirmed", run, readInfo.Status)
			}
		}
	}
}

func TestIngest_StreamedParsingBecomesOneBatch(t *testing.T) {
	gate, _ := newTestGate(t)

	ops := []testOp{
		{name: "a", kind: "read"},
		{name: "b", kind: "write"},
		{name: "c", kind: "read"},
	}
	idx := 0
	decisions, err := gate.Ingest(7, func() (testOp, error) {
		if idx >= len(ops) {
			var zero testOp
			return zero, io.EOF
		}
		op := ops[idx]
		idx++
		return op, nil
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(decisions) != 3 {
		t.Fatalf("Ingest decisions = %d, want 3", len(decisions))
	}
	for _, d := range decisions {
		if ref := decisionRef(t, d); ref.BatchID != 7 {
			t.Fatalf("decision batch = %d, want 7", ref.BatchID)
		}
	}

	parseErr := errors.New("frame truncated")
	_, err = gate.Ingest(8, func() (testOp, error) {
		return testOp{}, parseErr
	})
	if !errors.Is(err, parseErr) {
		t.Fatalf("Ingest parse err = %v, want wrapped parse error", err)
	}
}

func TestClassifierError_RejectsRequest(t *testing.T) {
	gate, _ := newTestGate(t)
	decisions := gate.SubmitBatch(1, []testOp{
		{name: "a", kind: "read"},
		{name: "b", kind: "mystery"},
		{name: "c", kind: "write"},
	})

	got := refs(decisions)
	if _, ok := got[1].(earlygate.ExecuteEarly[testOp]); !ok {
		t.Fatalf("request 1 = %T, want ExecuteEarly", got[1])
	}
	reject, ok := got[2].(earlygate.RejectRequest[testOp])
	if !ok || reject.Reason == nil {
		t.Fatalf("request 2 = %#v, want RejectRequest with reason", got[2])
	}
	if _, ok := got[3].(earlygate.HoldEarly[testOp]); !ok {
		t.Fatalf("request 3 = %T, want HoldEarly", got[3])
	}

	info, _ := gate.Request(2)
	if info.Status != earlygate.StatusClassifyRejected {
		t.Fatalf("request 2 status = %s, want ClassifyRejected", info.Status)
	}

	// 分类失败的请求不占用派发路径，确认后也不会被普通派发。
	confirmed, err := gate.Confirm()
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	for _, d := range confirmed {
		if v, ok := d.(earlygate.DispatchNormal[testOp]); ok && v.RequestID == 2 {
			t.Fatalf("classify-rejected request 2 must not be dispatched")
		}
	}
}

func TestNew_RejectsNilClassifier(t *testing.T) {
	if _, err := earlygate.New[testOp](nil); !errors.Is(err, earlygate.ErrNilClassifier) {
		t.Fatalf("New(nil) err = %v, want ErrNilClassifier", err)
	}
}

func decisionRef(t *testing.T, d earlygate.Decision[testOp]) earlygate.RequestRef[testOp] {
	t.Helper()
	switch v := d.(type) {
	case earlygate.ExecuteEarly[testOp]:
		return v.RequestRef
	case earlygate.HoldEarly[testOp]:
		return v.RequestRef
	case earlygate.DispatchNormal[testOp]:
		return v.RequestRef
	case earlygate.RejectRequest[testOp]:
		return v.RequestRef
	case earlygate.CancelAck[testOp]:
		return v.RequestRef
	default:
		t.Fatalf("unknown decision %T", d)
		return earlygate.RequestRef[testOp]{}
	}
}
