package msgproc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func okHandler(payload []byte) Handler {
	return func(ctx context.Context, in []byte) ([]byte, error) {
		if string(in) != string(payload) {
			return nil, errors.New("unexpected payload")
		}
		return []byte("done:" + string(in)), nil
	}
}

// 重复签收返回同一处理票据，且不产生新的业务记录。
func TestAcknowledgeIdempotent(t *testing.T) {
	p := NewProcessor(NewStore())
	msg := Message{ID: "m-1", Payload: []byte("a")}

	t1, err := p.Acknowledge(msg)
	if err != nil {
		t.Fatalf("first ack: %v", err)
	}
	t2, err := p.Acknowledge(msg)
	if err != nil {
		t.Fatalf("dup ack: %v", err)
	}
	if t1 != t2 {
		t.Fatalf("duplicate ack returned different ticket: %v vs %v", t1, t2)
	}
	view, err := p.Query(t1.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if view.Receipt != ReceiptAcked || view.Business != BizPending {
		t.Fatalf("unexpected states: %v / %v", view.Receipt, view.Business)
	}
	if len(view.Attempts) != 0 {
		t.Fatalf("no attempt should exist before execute, got %d", len(view.Attempts))
	}
}

// 签收后崩溃：签收已持久化，恢复后重复签收拿到同一票据，业务仍是 PENDING。
func TestCrashAfterAck(t *testing.T) {
	store := NewStore()
	faults := Faults{CrashAfterAck: func(string) bool { return true }}
	p1 := NewProcessor(store, WithFaults(faults))

	_, err := p1.Acknowledge(Message{ID: "m-crash", Payload: []byte("x")})
	if err != ErrSimulatedCrash {
		t.Fatalf("want ErrSimulatedCrash, got %v", err)
	}

	// 模拟进程重启：同一 Store，新的 Processor，不再注入故障。
	p2 := NewProcessor(store)
	ticket, err := p2.Acknowledge(Message{ID: "m-crash", Payload: []byte("x")})
	if err != nil {
		t.Fatalf("re-ack after recovery: %v", err)
	}
	view, err := p2.Query(ticket.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if view.Receipt != ReceiptAcked || view.Business != BizPending {
		t.Fatalf("after crash want ACKED/PENDING, got %v/%v", view.Receipt, view.Business)
	}
	if view.Phase() != PhaseAcknowledged {
		t.Fatalf("after crash want phase ACKNOWLEDGED, got %v", view.Phase())
	}
}

// 业务超时：尝试记录为 TIMEOUT，重试成功后历史完整保留。
func TestBusinessTimeoutAndRetry(t *testing.T) {
	store := NewStore()
	hang := true
	faults := Faults{HangInExecute: func(string) bool { return hang }}
	p := NewProcessor(store, WithFaults(faults), WithExecTimeout(50*time.Millisecond))

	ticket, err := p.Acknowledge(Message{ID: "m-timeout", Payload: []byte("t")})
	if err != nil {
		t.Fatalf("ack: %v", err)
	}

	attempt, err := p.Execute(context.Background(), ticket.ID, okHandler([]byte("t")))
	if err != ErrExecutionTimeout {
		t.Fatalf("want timeout, got %v", err)
	}
	if attempt.Outcome != BizTimeout || attempt.Seq != 1 {
		t.Fatalf("unexpected attempt: %+v", attempt)
	}

	hang = false // 撤掉故障后重试
	attempt, err = p.Execute(context.Background(), ticket.ID, okHandler([]byte("t")))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempt.Outcome != BizSucceeded || attempt.Seq != 2 {
		t.Fatalf("unexpected retry attempt: %+v", attempt)
	}

	view, _ := p.Query(ticket.ID)
	if len(view.Attempts) != 2 {
		t.Fatalf("want 2 attempts in history, got %d", len(view.Attempts))
	}
	if view.Attempts[0].Outcome != BizTimeout || view.Attempts[1].Outcome != BizSucceeded {
		t.Fatalf("attempt history not preserved: %+v", view.Attempts)
	}
	if view.Business != BizSucceeded || string(view.Result) != "done:t" {
		t.Fatalf("final state: %v result=%q", view.Business, view.Result)
	}
}

// 结果丢失：业务执行成功但结果被丢弃，状态为 RESULT_LOST，可重试恢复。
func TestResultLost(t *testing.T) {
	store := NewStore()
	lose := true
	faults := Faults{LoseResult: func(string) bool { return lose }}
	p := NewProcessor(store, WithFaults(faults))

	ticket, err := p.Acknowledge(Message{ID: "m-lost", Payload: []byte("z")})
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	_, err = p.Execute(context.Background(), ticket.ID, okHandler([]byte("z")))
	if err != ErrResultLost {
		t.Fatalf("want ErrResultLost, got %v", err)
	}

	view, _ := p.Query(ticket.ID)
	if view.Business != BizResultLost || view.ResultKnown || view.Result != nil {
		t.Fatalf("want RESULT_LOST with unknown result, got %+v", view)
	}
	if view.Phase() != PhaseResultUnknown {
		t.Fatalf("want phase RESULT_UNKNOWN, got %v", view.Phase())
	}

	lose = false
	if _, err := p.Execute(context.Background(), ticket.ID, okHandler([]byte("z"))); err != nil {
		t.Fatalf("retry after result lost: %v", err)
	}
	view, _ = p.Query(ticket.ID)
	if view.Business != BizSucceeded || !view.ResultKnown {
		t.Fatalf("retry should recover to SUCCEEDED, got %v", view.Business)
	}
	if len(view.Attempts) != 2 || view.Attempts[0].Outcome != BizResultLost {
		t.Fatalf("history should record RESULT_LOST then SUCCEEDED: %+v", view.Attempts)
	}
}

// 业务失败重试：失败原因记录在尝试历史中。
func TestFailureRetryHistory(t *testing.T) {
	p := NewProcessor(NewStore())
	ticket, _ := p.Acknowledge(Message{ID: "m-fail", Payload: []byte("f")})

	calls := 0
	handler := func(ctx context.Context, in []byte) ([]byte, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("boom")
		}
		return []byte("ok"), nil
	}
	for i := 0; i < 3; i++ {
		p.Execute(context.Background(), ticket.ID, handler)
	}
	view, _ := p.Query(ticket.ID)
	if len(view.Attempts) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(view.Attempts))
	}
	for i, want := range []BusinessState{BizFailed, BizFailed, BizSucceeded} {
		if view.Attempts[i].Outcome != want || view.Attempts[i].Seq != i+1 {
			t.Fatalf("attempt %d: %+v", i, view.Attempts[i])
		}
	}
	if view.Attempts[0].Err == "" {
		t.Fatal("failed attempt should record error")
	}
}

// 调用者能区分三个阶段：已签收未执行 / 结果未知 / 结果已知。
func TestCallerDistinguishesThreePhases(t *testing.T) {
	store := NewStore()
	faults := Faults{
		CrashAfterAck: func(id string) bool { return id == "m-crash" },
		LoseResult:    func(string) bool { return true },
	}
	p := NewProcessor(store, WithFaults(faults))

	// 阶段一：签收后崩溃，业务未执行。
	p.Acknowledge(Message{ID: "m-crash", Payload: []byte("1")})
	recovered := NewProcessor(store) // 恢复后不再注入崩溃
	tkCrash, _ := recovered.Acknowledge(Message{ID: "m-crash", Payload: []byte("1")})

	// 阶段二：结果丢失。
	tkLost, _ := p.Acknowledge(Message{ID: "m-lost", Payload: []byte("2")})
	p.Execute(context.Background(), tkLost.ID, okHandler([]byte("2")))

	// 阶段三：正常完成。
	tkOK, _ := p.Acknowledge(Message{ID: "m-ok", Payload: []byte("3")})
	okP := NewProcessor(store) // 无故障处理器
	okP.Execute(context.Background(), tkOK.ID, okHandler([]byte("3")))

	cases := []struct {
		ticket Ticket
		phase  Phase
		biz    BusinessState
	}{
		{tkCrash, PhaseAcknowledged, BizPending},
		{tkLost, PhaseResultUnknown, BizResultLost},
		{tkOK, PhaseResultKnown, BizSucceeded},
	}
	for _, c := range cases {
		view, err := p.Query(c.ticket.ID)
		if err != nil {
			t.Fatalf("query %s: %v", c.ticket.ID, err)
		}
		if view.Phase() != c.phase || view.Business != c.biz {
			t.Fatalf("ticket %s: want %v/%v, got %v/%v",
				c.ticket.ID, c.phase, c.biz, view.Phase(), view.Business)
		}
	}
}

func TestQueryUnknownTicket(t *testing.T) {
	p := NewProcessor(NewStore())
	if _, err := p.Query("nope"); err != ErrTicketNotFound {
		t.Fatalf("want ErrTicketNotFound, got %v", err)
	}
	if _, err := p.Acknowledge(Message{}); err != ErrMessageInvalid {
		t.Fatalf("want ErrMessageInvalid, got %v", err)
	}
}
