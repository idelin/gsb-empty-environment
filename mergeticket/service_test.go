package mergeticket

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const mainRef = "main"

func newTestService(t *testing.T) (*Service, *Graph, Hash) {
	t.Helper()
	g := NewGraph()
	root := commitOn(t, g, "", map[string]string{"README": "init"})
	if err := g.SetRef(mainRef, root); err != nil {
		t.Fatalf("seed main ref: %v", err)
	}
	svc := NewService(g)
	if err := svc.SetPolicy(mainRef, Policy{Version: 1, Name: "default", RequireCI: true}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	return svc, g, root
}

func commitOn(t *testing.T, g *Graph, parent Hash, files map[string]string) Hash {
	t.Helper()
	tree := g.AddTree(files)
	id, err := g.AddCommit(parent, tree)
	if err != nil {
		t.Fatalf("add commit: %v", err)
	}
	return id
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}

func countEvents(events []Event, ticketID, typ string) int {
	n := 0
	for _, e := range events {
		if e.TicketID == ticketID && e.Type == typ {
			n++
		}
	}
	return n
}

func assertJournalTotalOrder(t *testing.T, events []Event) {
	t.Helper()
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("journal not a contiguous total order: index %d has seq %d", i, e.Seq)
		}
	}
}

func TestApproveAndConsume_HappyPath(t *testing.T) {
	svc, g, root := newTestService(t)
	cand := commitOn(t, g, root, map[string]string{"README": "init", "a.go": "package a"})

	ticket, err := svc.Approve("T1", mainRef, cand)
	mustNoErr(t, err)
	if ticket.State != StateOpen || ticket.Baseline != root || ticket.PolicyVersion != 1 {
		t.Fatalf("approval snapshot wrong: %+v", ticket)
	}
	if ticket.CandidateTree == "" || ticket.CandidateCommit != cand {
		t.Fatalf("ticket must pin exact candidate commit and tree: %+v", ticket)
	}

	merged, err := svc.Consume("T1")
	mustNoErr(t, err)
	if merged.State != StateMerged {
		t.Fatalf("expected merged, got %s", merged.State)
	}
	if g.Ref(mainRef) != cand {
		t.Fatal("main must fast-forward to approved candidate")
	}
	if got := svc.Head(mainRef); got != "" {
		t.Fatalf("merged ticket must leave the queue, head=%q", got)
	}

	// 对已合入票据的重试必须是幂等终态错误，且不产生新事件。
	eventsBefore := len(svc.Journal())
	_, err = svc.Consume("T1")
	wantErrIs(t, err, ErrAlreadyMerged)
	if len(svc.Journal()) != eventsBefore {
		t.Fatal("retry on a merged ticket must not append events")
	}
}

func TestReorderThenBaselineAdvance_StaleTicketRejected(t *testing.T) {
	svc, g, root := newTestService(t)
	commitA := commitOn(t, g, root, map[string]string{"a.go": "A"})
	commitB := commitOn(t, g, root, map[string]string{"b.go": "B"})

	_, err := svc.Approve("TA", mainRef, commitA)
	mustNoErr(t, err)
	_, err = svc.Approve("TB", mainRef, commitB)
	mustNoErr(t, err)
	if got := svc.Queue(mainRef); len(got) != 2 || got[0] != "TA" || got[1] != "TB" {
		t.Fatalf("initial queue wrong: %v", got)
	}

	// 队列重排：把 TB 提到队首。重排只产生事件，不改任何绑定。
	mustNoErr(t, svc.Reorder(mainRef, 1, 0))
	if got := svc.Head(mainRef); got != "TB" {
		t.Fatalf("after reorder head should be TB, got %q", got)
	}

	// TB 先合入，目标基线从 root 推进到 commitB。
	_, err = svc.Consume("TB")
	mustNoErr(t, err)
	if g.Ref(mainRef) != commitB {
		t.Fatal("baseline should advance to commitB")
	}

	// TA 仍绑定旧基线 root：首次消费按基线漂移被拒并永久失效；
	// 之后的重试只拿到终态错误，永远不会带进旧候选。
	_, err = svc.Consume("TA")
	wantErrIs(t, err, ErrBaselineAdvanced)
	for attempt := 0; attempt < 5; attempt++ {
		_, err = svc.Consume("TA")
		wantErrIs(t, err, ErrTerminal)
	}
	ticket, err := svc.Ticket("TA")
	mustNoErr(t, err)
	if ticket.State != StateInvalid {
		t.Fatalf("stale ticket must be invalid, got %s", ticket.State)
	}
	if g.Ref(mainRef) != commitB {
		t.Fatal("rejected retry must never move the ref")
	}

	journal := svc.Journal()
	assertJournalTotalOrder(t, journal)
	if countEvents(journal, "TA", EventInvalid) != 1 {
		t.Fatal("stale ticket must record exactly one invalid event across retries")
	}
	var sawReorder bool
	for _, e := range journal {
		if e.Type == EventReordered && e.TicketID == "TB" && e.FromPosition == 1 && e.ToPosition == 0 {
			sawReorder = true
		}
	}
	if !sawReorder {
		t.Fatal("reordered event with exact positions not observed")
	}
}

func TestSameTreeDifferentParent_NotInterchangeable(t *testing.T) {
	svc, g, root := newTestService(t)

	treeX := g.AddTree(map[string]string{"x.go": "X"})
	c1, err := g.AddCommit(root, treeX) // 挂在 root 下
	mustNoErr(t, err)
	intermediate := commitOn(t, g, root, map[string]string{"mid.go": "M"})
	c2, err := g.AddCommit(intermediate, treeX) // 同一棵树，挂在另一个父提交下
	mustNoErr(t, err)

	if c1 == c2 {
		t.Fatal("same tree under different parents must produce distinct commit hashes")
	}
	if got, _ := g.Lookup(c1); got.Tree != treeX {
		t.Fatal("c1 tree lookup mismatch")
	}

	// T1 批准的是 c1 这个精确提交。
	t1, err := svc.Approve("T1", mainRef, c1)
	mustNoErr(t, err)

	// 基线推进到 intermediate；c2 携带 intermediate 这段没人审过的历史。
	mustNoErr(t, svc.AdvanceBaseline(mainRef, intermediate))

	// T1 的批准不能被复用来合入"同树不同父"的 c2，也不能合入旧的 c1。
	_, err = svc.Consume("T1")
	wantErrIs(t, err, ErrBaselineAdvanced)
	if g.Ref(mainRef) == c1 || g.Ref(mainRef) == c2 {
		t.Fatal("unreviewed-parent candidate must never reach main via the old ticket")
	}

	// c2 需要自己的批准；新票据绑定新基线后才能合入。
	t2, err := svc.Approve("T2", mainRef, c2)
	mustNoErr(t, err)
	if t2.CandidateCommit == t1.CandidateCommit {
		t.Fatal("tickets for same-tree/different-parent commits must pin different commits")
	}
	_, err = svc.Consume("T2")
	mustNoErr(t, err)
	if g.Ref(mainRef) != c2 {
		t.Fatal("freshly approved c2 should merge to main")
	}

	stale, _ := svc.Ticket("T1")
	if stale.State != StateInvalid {
		t.Fatalf("T1 must stay invalid, got %s", stale.State)
	}
}

func TestPolicyVersionBump_InvalidatesOldApprovals(t *testing.T) {
	svc, g, root := newTestService(t)
	cand := commitOn(t, g, root, map[string]string{"a.go": "A"})
	_, err := svc.Approve("TP", mainRef, cand)
	mustNoErr(t, err)

	mustNoErr(t, svc.SetPolicy(mainRef, Policy{Version: 2, Name: "stricter", RequireCI: true}))

	_, err = svc.Consume("TP")
	wantErrIs(t, err, ErrPolicyChanged)

	// 失效是终态：重试不会有第二次迁移。
	_, err = svc.Consume("TP")
	wantErrIs(t, err, ErrTerminal)
	journal := svc.Journal()
	if countEvents(journal, "TP", EventInvalid) != 1 {
		t.Fatal("policy bump must produce exactly one invalid event")
	}
	if g.Ref(mainRef) != root {
		t.Fatal("policy-stale ticket must not move the baseline")
	}

	// 新策略下重新批准的同候选可以合入（基线未变）。
	_, err = svc.Approve("TP2", mainRef, cand)
	mustNoErr(t, err)
	_, err = svc.Consume("TP2")
	mustNoErr(t, err)
	if g.Ref(mainRef) != cand {
		t.Fatal("approval under the new policy should merge")
	}
}

func TestConcurrentConsume_OnlyOneWins(t *testing.T) {
	svc, g, root := newTestService(t)
	cand := commitOn(t, g, root, map[string]string{"a.go": "A"})
	_, err := svc.Approve("TW", mainRef, cand)
	mustNoErr(t, err)

	const workers = 16
	var wg sync.WaitGroup
	var wins, terminalRejects int
	var mu sync.Mutex
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, consumeErr := svc.Consume("TW")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case consumeErr == nil:
				wins++
			case errors.Is(consumeErr, ErrAlreadyMerged):
				terminalRejects++
			default:
				t.Errorf("unexpected consume error: %v", consumeErr)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 || terminalRejects != workers-1 {
		t.Fatalf("want exactly 1 merge and %d terminal rejects, got %d/%d", workers-1, wins, terminalRejects)
	}
	journal := svc.Journal()
	if countEvents(journal, "TW", EventMerged) != 1 {
		t.Fatal("exactly one merged event allowed")
	}
	assertJournalTotalOrder(t, journal)
}

func TestConsumeCancelRace_SingleObservableOutcome(t *testing.T) {
	svc, g, root := newTestService(t)
	const n = 40

	type spec struct {
		target string
		ticket string
		cand   Hash
	}
	specs := make([]spec, 0, n)
	for i := range n {
		target := fmt.Sprintf("branch-%d", i)
		mustNoErr(t, g.SetRef(target, root))
		mustNoErr(t, svc.SetPolicy(target, Policy{Version: 1}))
		cand := commitOn(t, g, root, map[string]string{fmt.Sprintf("f%d.go", i): "x"})
		id := fmt.Sprintf("T%d", i)
		_, err := svc.Approve(id, target, cand)
		mustNoErr(t, err)
		specs = append(specs, spec{target: target, ticket: id, cand: cand})
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, sp := range specs {
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			<-start
			_, _ = svc.Consume(id)
		}(sp.ticket)
		go func(id string) {
			defer wg.Done()
			<-start
			_, _ = svc.Cancel(id)
		}(sp.ticket)
	}
	close(start)
	wg.Wait()

	journal := svc.Journal()
	assertJournalTotalOrder(t, journal)

	seenSeq := map[int64]bool{}
	for _, e := range journal {
		if seenSeq[e.Seq] {
			t.Fatalf("duplicate seq %d", e.Seq)
		}
		seenSeq[e.Seq] = true
	}

	for _, sp := range specs {
		ticket, err := svc.Ticket(sp.ticket)
		mustNoErr(t, err)
		merged := countEvents(journal, sp.ticket, EventMerged)
		cancelled := countEvents(journal, sp.ticket, EventCancelled)
		if merged+cancelled != 1 {
			t.Fatalf("%s: want exactly one terminal event, got merged=%d cancelled=%d", sp.ticket, merged, cancelled)
		}
		switch ticket.State {
		case StateMerged:
			if g.Ref(sp.target) != sp.cand {
				t.Fatalf("%s: merged state but ref not at candidate", sp.ticket)
			}
		case StateCancelled:
			if g.Ref(sp.target) != root {
				t.Fatalf("%s: cancelled ticket must leave ref at root", sp.ticket)
			}
		default:
			t.Fatalf("%s: unexpected state %s", sp.ticket, ticket.State)
		}
	}
}

func TestWaitForSequence_SharedOrder(t *testing.T) {
	svc, g, root := newTestService(t)
	after := int64(len(svc.Journal()))

	got := make(chan []Event, 1)
	go func() {
		events, err := svc.WaitForSequence(after, time.Second)
		if err != nil {
			got <- nil
			return
		}
		got <- events
	}()

	cand := commitOn(t, g, root, map[string]string{"a.go": "A"})
	_, err := svc.Approve("TWQ", mainRef, cand)
	mustNoErr(t, err)

	select {
	case events := <-got:
		if len(events) != 1 || events[0].Type != EventApproved || events[0].Seq != after+1 {
			t.Fatalf("waiter did not observe the exact next ordered event: %+v", events)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForSequence did not wake on journal append")
	}

	if _, err := svc.WaitForSequence(int64(len(svc.Journal())), 10*time.Millisecond); err == nil {
		t.Fatal("waiting beyond the current sequence without new events must time out")
	}
}
