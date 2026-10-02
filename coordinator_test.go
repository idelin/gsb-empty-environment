package twopc

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "twopc-test")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	return dir
}

func testConfig() Config {
	return Config{MaxRetries: 2, RetryDelay: 0}
}

func newCluster(votes ...Vote) (*LocalTransport, map[string]*Participant, []string) {
	ps := make(map[string]*Participant)
	names := make([]string, 0, len(votes))
	for i, v := range votes {
		name := fmt.Sprintf("P%d", i+1)
		ps[name] = NewParticipant(v)
		names = append(names, name)
	}
	return NewLocalTransport(ps), ps, names
}

func countRecords(records []Record, rt RecordType) int {
	n := 0
	for _, r := range records {
		if r.Type == rt {
			n++
		}
	}
	return n
}

func findAudit(records []Record, participant string) *Record {
	for i := range records {
		if records[i].Type == RecAudit && records[i].Participant == participant {
			return &records[i]
		}
	}
	return nil
}

func TestCommitHappyPath(t *testing.T) {
	tr, ps, names := newCluster(VoteYes, VoteYes, VoteYes)
	wal := &MemWAL{}
	c := NewCoordinator(testConfig(), wal, tr, nil)

	d, err := c.Run("tx1", names)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d != DecisionCommit {
		t.Fatalf("decision = %s, want COMMIT", d)
	}
	for _, p := range ps {
		if st := p.State("tx1"); st != StateCommitted {
			t.Fatalf("participant state = %s, want COMMITTED", st)
		}
	}
	if got := countRecords(wal.Records, RecDone); got != 1 {
		t.Fatalf("DONE records = %d, want 1", got)
	}
	if got := countRecords(wal.Records, RecAudit); got != 0 {
		t.Fatalf("unexpected AUDIT records: %d", got)
	}
	if pending := c.Pending(); len(pending) != 0 {
		t.Fatalf("pending transactions: %v", pending)
	}
}

func TestAbortOnVoteNo(t *testing.T) {
	tr, ps, names := newCluster(VoteYes, VoteNo, VoteYes)
	wal := &MemWAL{}
	c := NewCoordinator(testConfig(), wal, tr, nil)

	d, _ := c.Run("tx1", names)
	if d != DecisionAbort {
		t.Fatalf("decision = %s, want ABORT", d)
	}
	for _, p := range ps {
		if st := p.State("tx1"); st != StateAborted {
			t.Fatalf("participant state = %s, want ABORTED", st)
		}
	}
	if pending := c.Pending(); len(pending) != 0 {
		t.Fatalf("pending transactions: %v", pending)
	}
}

func TestVoteTimeoutAborts(t *testing.T) {
	tr, ps, names := newCluster(VoteYes, VoteYes, VoteYes)
	ps["P3"].Down = true // no response within the vote timeout
	wal := &MemWAL{}
	c := NewCoordinator(testConfig(), wal, tr, nil)

	d, _ := c.Run("tx1", names)
	if d != DecisionAbort {
		t.Fatalf("decision = %s, want ABORT on vote timeout", d)
	}
	if st := ps["P1"].State("tx1"); st != StateAborted {
		t.Fatalf("P1 state = %s, want ABORTED", st)
	}
	// P3 never acknowledged: pending audit must exist, transaction not DONE.
	if findAudit(wal.Records, "P3") == nil {
		t.Fatal("missing pending AUDIT record for unreachable P3")
	}
	if got := countRecords(wal.Records, RecDone); got != 0 {
		t.Fatalf("DONE records = %d, want 0 while P3 unreachable", got)
	}

	// P3 recovers: recovery must deliver the abort and close the transaction.
	ps["P3"].Down = false
	c.Recover()
	if st := ps["P3"].State("tx1"); st != StateAborted {
		t.Fatalf("P3 state after recovery = %s, want ABORTED", st)
	}
	if got := countRecords(wal.Records, RecDone); got != 1 {
		t.Fatalf("DONE records after recovery = %d, want 1", got)
	}
	if pending := c.Pending(); len(pending) != 0 {
		t.Fatalf("pending transactions after recovery: %v", pending)
	}
}

// Acceptance boundary: the coordinator crashes after logging the COMMIT
// decision but before publishing it. On restart it replays the WAL, queries
// the online participants and finishes the commit.
func TestCrashBeforeCommitThenRecover(t *testing.T) {
	tr, ps, names := newCluster(VoteYes, VoteYes, VoteYes)
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "coordinator.wal")

	// --- first incarnation: prepare + decide, then "crash" ---
	wal1, _, err := OpenFileWAL(walPath)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	c1 := NewCoordinator(testConfig(), wal1, tr, nil)
	if err := c1.Begin("tx1", names); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	c1.prepare("tx1")
	c1.decide("tx1")
	if d := c1.DecisionOf("tx1"); d != DecisionCommit {
		t.Fatalf("pre-crash decision = %s, want COMMIT", d)
	}
	for _, p := range ps {
		if p.CommitCalls != 0 {
			t.Fatal("commit delivered before simulated crash; test setup broken")
		}
		if st := p.State("tx1"); st != StatePrepared {
			t.Fatalf("participant state = %s, want PREPARED", st)
		}
	}
	wal1.Close() // crash: c1 is abandoned without resolving

	// --- restart: replay WAL, query participants, finish the commit ---
	wal2, records, err := OpenFileWAL(walPath)
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	defer wal2.Close()
	if got := countRecords(records, RecDecision); got != 1 {
		t.Fatalf("replayed DECISION records = %d, want 1", got)
	}
	c2 := NewCoordinator(testConfig(), wal2, tr, records)
	c2.Recover()

	for _, p := range ps {
		if st := p.State("tx1"); st != StateCommitted {
			t.Fatalf("participant state after recovery = %s, want COMMITTED", st)
		}
		if p.QueryCalls == 0 {
			t.Fatal("recovery did not query participant status")
		}
	}
	final, err := LoadRecords(walPath)
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	if got := countRecords(final, RecDone); got != 1 {
		t.Fatalf("DONE records = %d, want 1", got)
	}
	if pending := c2.Pending(); len(pending) != 0 {
		t.Fatalf("pending transactions after recovery: %v", pending)
	}
}

// Crash after votes are logged but before the decision record: recovery must
// rebuild the decision from replayed votes without re-preparing anyone.
func TestCrashAfterVotesBeforeDecision(t *testing.T) {
	tr, ps, names := newCluster(VoteYes, VoteYes)
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "coordinator.wal")

	wal1, _, _ := OpenFileWAL(walPath)
	c1 := NewCoordinator(testConfig(), wal1, tr, nil)
	c1.Begin("tx1", names)
	c1.prepare("tx1")
	wal1.Close() // crash before decide()

	wal2, records, _ := OpenFileWAL(walPath)
	defer wal2.Close()
	c2 := NewCoordinator(testConfig(), wal2, tr, records)
	c2.Recover()

	if d := c2.DecisionOf("tx1"); d != DecisionCommit {
		t.Fatalf("decision after recovery = %s, want COMMIT", d)
	}
	for _, p := range ps {
		if st := p.State("tx1"); st != StateCommitted {
			t.Fatalf("participant state = %s, want COMMITTED", st)
		}
		if p.PrepareCalls != 1 {
			t.Fatalf("PrepareCalls = %d, want 1 (no re-prepare after recovery)", p.PrepareCalls)
		}
	}
}

// Acceptance boundary: an unresponsive participant causes a pending audit
// event after the retry budget is exhausted; the transaction is not
// forgotten and completes once the participant returns.
func TestPendingAuditAfterRetryExhaustion(t *testing.T) {
	tr, ps, names := newCluster(VoteYes, VoteYes)
	ps["P2"].Down = true
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "coordinator.wal")

	wal1, _, _ := OpenFileWAL(walPath)
	c1 := NewCoordinator(Config{MaxRetries: 3}, wal1, tr, nil)
	d, _ := c1.Run("tx1", names)
	if d != DecisionAbort {
		t.Fatalf("decision = %s, want ABORT (P2 vote timed out)", d)
	}
	recs1, err := LoadRecords(walPath)
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	audit := findAudit(recs1, "P2")
	if audit == nil {
		t.Fatal("missing pending AUDIT record after retries exhausted")
	}
	if !strings.Contains(audit.Reason, "pending") {
		t.Fatalf("audit reason %q does not describe the pending event", audit.Reason)
	}
	if pending := c1.Pending(); len(pending) != 1 || pending[0] != "tx1" {
		t.Fatalf("pending = %v, want [tx1]", pending)
	}
	wal1.Close()

	// Restart while P2 is still down: audit must not be duplicated.
	wal2, records, _ := OpenFileWAL(walPath)
	c2 := NewCoordinator(Config{MaxRetries: 3}, wal2, tr, records)
	c2.Recover()
	recs2, err := LoadRecords(walPath)
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	if got := countRecords(recs2, RecAudit); got != 1 {
		t.Fatalf("AUDIT records = %d, want 1 (idempotent across restarts)", got)
	}

	// P2 comes back: the pending transaction must be resolved, not orphaned.
	ps["P2"].Down = false
	c2.Recover()
	if st := ps["P2"].State("tx1"); st != StateAborted {
		t.Fatalf("P2 state = %s, want ABORTED", st)
	}
	recs3, err := LoadRecords(walPath)
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	if got := countRecords(recs3, RecDone); got != 1 {
		t.Fatalf("DONE records = %d, want 1", got)
	}
	if pending := c2.Pending(); len(pending) != 0 {
		t.Fatalf("pending transactions after P2 returned: %v", pending)
	}
	wal2.Close()
}

// Duplicate votes (at-least-once delivery) must be recorded once and must
// not change the outcome.
func TestDuplicateVotes(t *testing.T) {
	tr, _, names := newCluster(VoteYes, VoteYes)
	wal := &MemWAL{}
	c := NewCoordinator(testConfig(), wal, tr, nil)

	c.Begin("tx1", names)
	if !c.RecordVote("tx1", "P1", VoteYes) {
		t.Fatal("first vote not accepted")
	}
	if c.RecordVote("tx1", "P1", VoteYes) {
		t.Fatal("duplicate vote accepted")
	}
	if c.RecordVote("tx1", "P1", VoteNo) {
		t.Fatal("conflicting duplicate vote accepted")
	}
	if c.RecordVote("tx1", "GHOST", VoteYes) {
		t.Fatal("vote from non-participant accepted")
	}
	c.RecordVote("tx1", "P2", VoteYes)
	c.decide("tx1")
	c.resolve("tx1")

	if d := c.DecisionOf("tx1"); d != DecisionCommit {
		t.Fatalf("decision = %s, want COMMIT", d)
	}
	if got := countRecords(wal.Records, RecVote); got != 2 {
		t.Fatalf("VOTE records = %d, want 2 (duplicates deduplicated)", got)
	}
	// Votes arriving after the decision are ignored.
	if c.RecordVote("tx1", "P1", VoteNo) {
		t.Fatal("late vote after decision accepted")
	}
	if got := countRecords(wal.Records, RecVote); got != 2 {
		t.Fatalf("VOTE records after late vote = %d, want 2", got)
	}
}

// A decision must never be reversed, even across restarts.
func TestDecisionIsDurableAndFinal(t *testing.T) {
	tr, _, names := newCluster(VoteYes, VoteYes)
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "coordinator.wal")

	wal1, _, _ := OpenFileWAL(walPath)
	c1 := NewCoordinator(testConfig(), wal1, tr, nil)
	c1.Begin("tx1", names)
	c1.prepare("tx1")
	c1.decide("tx1")
	wal1.Close()

	wal2, records, _ := OpenFileWAL(walPath)
	defer wal2.Close()
	c2 := NewCoordinator(testConfig(), wal2, tr, records)
	c2.decide("tx1") // must be a no-op, not a second decision
	recs, err := LoadRecords(walPath)
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	if got := countRecords(recs, RecDecision); got != 1 {
		t.Fatalf("DECISION records = %d, want 1", got)
	}
	if d := c2.DecisionOf("tx1"); d != DecisionCommit {
		t.Fatalf("decision = %s, want COMMIT", d)
	}
}

// Torn tail: a crash in the middle of a WAL append must not corrupt replay.
func TestWALTornTailIgnored(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "coordinator.wal")
	wal, _, err := OpenFileWAL(walPath)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	wal.Append(Record{Type: RecBegin, TxID: "tx1", Participants: []string{"P1"}})
	wal.Close()

	// Simulate a half-written record at the end of the file.
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	f.WriteString(`{"type":"VOTE","txid":"tx1","part`)
	f.Close()

	records, err := LoadRecords(walPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 1 || records[0].Type != RecBegin {
		t.Fatalf("records = %v, want only the complete BEGIN", records)
	}
}
