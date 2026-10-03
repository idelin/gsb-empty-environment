package twopc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{RPCTimeout: 50 * time.Millisecond, MaxRetries: 3, RetryBackoff: 5 * time.Millisecond}
}

type testEnv struct {
	walPath string
	parts   map[string]*MemoryParticipant
	reg     map[string]Participant
}

func newTestEnv(t *testing.T, ids ...string) *testEnv {
	t.Helper()
	env := &testEnv{
		walPath: filepath.Join(t.TempDir(), "coordinator.wal"),
		parts:   map[string]*MemoryParticipant{},
		reg:     map[string]Participant{},
	}
	for _, id := range ids {
		p := NewMemoryParticipant(id)
		env.parts[id] = p
		env.reg[id] = p
	}
	return env
}

func (env *testEnv) openCoordinator(t *testing.T, hooks Hooks) (*Coordinator, *WAL) {
	t.Helper()
	wal, err := OpenWAL(env.walPath)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	return NewCoordinator(wal, testConfig(), env.reg, hooks, nil), wal
}

func (env *testEnv) recover(t *testing.T) *Coordinator {
	t.Helper()
	c, wal := env.openCoordinator(t, Hooks{})
	t.Cleanup(func() { wal.Close() })
	if err := c.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return c
}

func walRecords(t *testing.T, path string) []Record {
	t.Helper()
	wal, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	defer wal.Close()
	var out []Record
	if err := wal.Replay(func(r Record) error {
		out = append(out, r)
		return nil
	}); err != nil {
		t.Fatalf("replay WAL: %v", err)
	}
	return out
}

func TestHappyPathCommit(t *testing.T) {
	env := newTestEnv(t, "p1", "p2", "p3")
	c, wal := env.openCoordinator(t, Hooks{})
	defer wal.Close()

	state, err := c.Submit("txn-1", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if state != StateCommitted {
		t.Fatalf("state = %s, want COMMITTED", state)
	}
	for id, p := range env.parts {
		if got := p.State("txn-1"); got != PStateCommitted {
			t.Errorf("participant %s = %s, want COMMITTED", id, got)
		}
	}
	if len(c.Pending()) != 0 {
		t.Errorf("unexpected pending txns: %v", c.Pending())
	}
}

func TestAbortOnNoVote(t *testing.T) {
	env := newTestEnv(t, "p1", "p2", "p3")
	env.parts["p2"].SetVoteFunc(func(string) Vote { return VoteNo })
	c, wal := env.openCoordinator(t, Hooks{})
	defer wal.Close()

	state, err := c.Submit("txn-1", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if state != StateAborted {
		t.Fatalf("state = %s, want ABORTED", state)
	}
	for id, p := range env.parts {
		if got := p.State("txn-1"); got != PStateAborted {
			t.Errorf("participant %s = %s, want ABORTED", id, got)
		}
	}
}

func TestAbortOnParticipantDown(t *testing.T) {
	env := newTestEnv(t, "p1", "p2", "p3")
	env.parts["p2"].SetDown(true) // crashed before prepare
	c, wal := env.openCoordinator(t, Hooks{})

	state, err := c.Submit("txn-1", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	wal.Close()

	// p1/p3 get the abort; p2 is unreachable, so the decision stays pending
	// with an audit event until p2 comes back.
	if state != StateAborting {
		t.Fatalf("state = %s, want ABORTING (p2 pending)", state)
	}
	for _, id := range []string{"p1", "p3"} {
		if got := env.parts[id].State("txn-1"); got != PStateAborted {
			t.Errorf("participant %s = %s, want ABORTED (no orphan)", id, got)
		}
	}
	if pending := c.Pending()["txn-1"]; len(pending) != 1 || pending[0] != "p2" {
		t.Fatalf("pending = %v, want [p2]", pending)
	}

	// p2 restarts; recovery delivers the abort and releases its state.
	env.parts["p2"].SetDown(false)
	c2 := env.recover(t)
	if got, _ := c2.State("txn-1"); got != StateAborted {
		t.Fatalf("recovered state = %s, want ABORTED", got)
	}
	// p2 never prepared, so it holds no state for the txn; the abort is a
	// no-op there. What matters is that nothing is left PREPARED.
	if got := env.parts["p2"].State("txn-1"); got == PStatePrepared {
		t.Fatalf("p2 = %s, orphan prepared txn leaked", got)
	}
}

func TestAbortOnSlowParticipantTimeout(t *testing.T) {
	env := newTestEnv(t, "p1", "p2")
	env.parts["p2"].SetDelay(500 * time.Millisecond) // way past RPCTimeout
	c, wal := env.openCoordinator(t, Hooks{})
	defer wal.Close()

	state, err := c.Submit("txn-1", []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if state != StateAborted {
		t.Fatalf("state = %s, want ABORTED", state)
	}
	if got := env.parts["p1"].State("txn-1"); got != PStateAborted {
		t.Errorf("participant p1 = %s, want ABORTED", got)
	}
}

func TestDuplicateVoteIdempotent(t *testing.T) {
	p := NewMemoryParticipant("p1")
	v1, err := p.Prepare(context.Background(), "txn-x")
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	// Coordinator retry re-sends Prepare for the same txn.
	v2, err := p.Prepare(context.Background(), "txn-x")
	if err != nil {
		t.Fatalf("duplicate Prepare: %v", err)
	}
	if v1 != v2 {
		t.Fatalf("duplicate vote changed: %s then %s", v1, v2)
	}

	// Coordinator-side: a full run must log exactly one VOTE record per
	// participant even though Prepare may be retried.
	env := newTestEnv(t, "p1", "p2")
	c, wal := env.openCoordinator(t, Hooks{})
	defer wal.Close()
	if _, err := c.Submit("txn-1", []string{"p1", "p2"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	votes := map[string]int{}
	for _, r := range walRecords(t, env.walPath) {
		if r.Type == RecordVote {
			votes[r.Participant]++
		}
	}
	for _, pid := range []string{"p1", "p2"} {
		if votes[pid] != 1 {
			t.Errorf("VOTE records for %s = %d, want 1", pid, votes[pid])
		}
	}
}

// Acceptance boundary 1: the coordinator crashes after the COMMITTING record
// is durable but before any Commit is sent. A restarted coordinator replays
// the WAL, queries the (still prepared) participants, and finishes the commit.
func TestRecoverCompletesCommitAfterCrash(t *testing.T) {
	env := newTestEnv(t, "p1", "p2", "p3")

	var crashed int32
	hooks := Hooks{BeforeCommitSend: func(txnID string) bool {
		return atomic.CompareAndSwapInt32(&crashed, 0, 1) // crash once
	}}
	c1, wal1 := env.openCoordinator(t, hooks)
	_, err := c1.Submit("txn-1", []string{"p1", "p2", "p3"})
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("Submit err = %v, want ErrCrashed", err)
	}
	wal1.Close()

	// Participants are stuck in PREPARED, holding locks.
	for id, p := range env.parts {
		if got := p.State("txn-1"); got != PStatePrepared {
			t.Fatalf("participant %s = %s, want PREPARED before recovery", id, got)
		}
	}

	// Restart with the same WAL: replay + query + finish the commit.
	c2 := env.recover(t)
	if got, _ := c2.State("txn-1"); got != StateCommitted {
		t.Fatalf("recovered state = %s, want COMMITTED", got)
	}
	for id, p := range env.parts {
		if got := p.State("txn-1"); got != PStateCommitted {
			t.Errorf("participant %s = %s, want COMMITTED after recovery", id, got)
		}
	}

	// The WAL must show the full lifecycle across the crash.
	var sawCommitting, sawCommitted bool
	for _, r := range walRecords(t, env.walPath) {
		if r.Type == RecordState && r.State == StateCommitting {
			sawCommitting = true
		}
		if r.Type == RecordState && r.State == StateCommitted {
			sawCommitted = true
		}
	}
	if !sawCommitting || !sawCommitted {
		t.Errorf("WAL missing transition: committing=%v committed=%v", sawCommitting, sawCommitted)
	}
}

// Acceptance boundary 2: a participant stays unreachable past the retry
// limit, so the coordinator records a pending audit event; once the
// participant is back, recovery completes the delivery.
func TestPendingAuditAfterRetryLimit(t *testing.T) {
	env := newTestEnv(t, "p1", "p2", "p3")

	hooks := Hooks{BeforeCommitSend: func(txnID string) bool {
		env.parts["p3"].SetDown(true) // p3 dies just before the commit phase
		return false
	}}
	c1, wal1 := env.openCoordinator(t, hooks)
	state, err := c1.Submit("txn-1", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	wal1.Close()

	// Decision made but not fully delivered: txn stays COMMITTING with p3 pending.
	if state != StateCommitting {
		t.Fatalf("state = %s, want COMMITTING (pending)", state)
	}
	pending := c1.Pending()["txn-1"]
	if len(pending) != 1 || pending[0] != "p3" {
		t.Fatalf("pending = %v, want [p3]", pending)
	}

	// The pending audit event must be durable in the WAL.
	var audits int
	for _, r := range walRecords(t, env.walPath) {
		if r.Type == RecordAudit && r.Participant == "p3" {
			audits++
		}
	}
	if audits == 0 {
		t.Fatal("no AUDIT record for unreachable participant p3")
	}

	// p3 comes back; a recovery pass finishes the delivery.
	env.parts["p3"].SetDown(false)
	c2 := env.recover(t)
	if got, _ := c2.State("txn-1"); got != StateCommitted {
		t.Fatalf("recovered state = %s, want COMMITTED", got)
	}
	if got := env.parts["p3"].State("txn-1"); got != PStateCommitted {
		t.Fatalf("p3 = %s, want COMMITTED after recovery", got)
	}
	if len(c2.Pending()) != 0 {
		t.Errorf("still pending after recovery: %v", c2.Pending())
	}
}

// A transaction interrupted in PREPARING can never have committed anywhere,
// so recovery must abort it everywhere to avoid orphaned locks.
func TestRecoverAbortsIndecisionTxn(t *testing.T) {
	env := newTestEnv(t, "p1", "p2")

	// Simulate a crash mid-prepare: p1 is prepared, p2 never voted, and the
	// WAL only got as far as PREPARING + p1's vote.
	if _, err := env.parts["p1"].Prepare(context.Background(), "txn-9"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	wal, err := OpenWAL(env.walPath)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	wal.Append(Record{TxnID: "txn-9", Type: RecordTxnBegin, Detail: "p1,p2"})
	wal.Append(Record{TxnID: "txn-9", Type: RecordState, State: StatePreparing})
	wal.Append(Record{TxnID: "txn-9", Type: RecordVote, Participant: "p1", Vote: VoteYes})
	wal.Close()

	c := env.recover(t)
	if got, _ := c.State("txn-9"); got != StateAborted {
		t.Fatalf("recovered state = %s, want ABORTED", got)
	}
	if got := env.parts["p1"].State("txn-9"); got != PStateAborted {
		t.Fatalf("p1 = %s, want ABORTED (orphan released)", got)
	}
}

func TestWALToleratesTornTail(t *testing.T) {
	env := newTestEnv(t)
	wal, err := OpenWAL(env.walPath)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	wal.Append(Record{TxnID: "txn-1", Type: RecordTxnBegin, Detail: "p1"})
	wal.Append(Record{TxnID: "txn-1", Type: RecordState, State: StatePreparing})
	wal.Close()

	// Crash mid-write: append half a JSON record.
	f, err := os.OpenFile(env.walPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(`{"txn_id":"txn-1","type":"STATE","sta`); err != nil {
		t.Fatalf("write torn record: %v", err)
	}
	f.Close()

	records := walRecords(t, env.walPath)
	if len(records) != 2 {
		t.Fatalf("replayed %d records, want 2 (torn tail ignored)", len(records))
	}
}
