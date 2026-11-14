package netsec

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestKeyDestructionRequiresNoActiveOrQuarantinedCID(t *testing.T) {
	clock := NewManualClock(time.Unix(600, 0).UTC())
	gen := SequentialCIDGenerator([]byte{0x10}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))

	g0, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfirmPeerUse(g0.CID); err != nil {
		t.Fatal(err)
	}
	rot, err := m.RotateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if rot.Witness.Kind != AuditKeyRotated || rot.NewGeneration != 1 {
		t.Fatalf("rotation = %+v", rot)
	}
	if tok, ok := m.TokenForCID(g0.CID); !ok || !tok.Equal(g0.Token) {
		t.Fatal("rotation changed an existing CID's token binding")
	}
	if _, err := m.IssueCID(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DestroyKeyGeneration(0); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("destroy active generation error = %v", err)
	}
	if _, err := m.RetireFromPeer(g0.Sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DestroyKeyGeneration(0); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("destroy quarantined generation error = %v", err)
	}
	if st := m.KeyStatus(0); st.Active != 0 || st.Quarantined != 1 || st.Destroyable {
		t.Fatalf("status during quarantine = %+v", st)
	}
	if _, err := m.DestroyKeyGeneration(1); !errors.Is(err, ErrCurrentKeyGeneration) {
		t.Fatalf("destroy current generation error = %v", err)
	}

	clock.Advance(13 * time.Second)
	if _, err := m.PurgeExpired(); err != nil {
		t.Fatal(err)
	}
	if st := m.KeyStatus(0); st.Active != 0 || st.Quarantined != 0 || st.KeyReferences != 0 || !st.Destroyable {
		t.Fatalf("status after expiry = %+v", st)
	}
	tr, err := m.DestroyKeyGeneration(0)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Witness == nil || tr.Witness.Kind != AuditKeyDestroyed {
		t.Fatalf("destroy witness = %+v", tr.Witness)
	}
	if m.keys.HasGeneration(0) {
		t.Fatal("destroyed key generation is still present")
	}
}

func TestCrashRecoveryRestoresStateAndRejectsRollback(t *testing.T) {
	clock := NewManualClock(time.Unix(700, 0).UTC())
	journal := NewMemoryJournal()
	gen := SequentialCIDGenerator([]byte{0x21}, 8)
	cfg := testConfig(clock, gen, journal, "edge-a", "c1")
	m1 := mustManager(t, cfg)

	g0, err := m1.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.ConfirmPeerUse(g0.CID); err != nil {
		t.Fatal(err)
	}
	if _, err := m1.RequestRetire(g0.CID, "rotate"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := m1.RetireFromPeer(g0.Sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := m1.RotateKeys(); err != nil {
		t.Fatal(err)
	}
	g1, err := m1.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.ConfirmPeerUse(g1.CID); err != nil {
		t.Fatal(err)
	}
	if _, err := m1.RebindPath("203.0.113.9:443"); err != nil {
		t.Fatal(err)
	}
	cp, err := m1.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := m1.VerifyCheckpoint(cp); err != nil {
		t.Fatal(err)
	}

	m2 := mustManager(t, cfg)
	if err := m2.Recover(journal, cp); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if m2.CurrentKeyGeneration() != 1 {
		t.Fatalf("generation after recovery = %d", m2.CurrentKeyGeneration())
	}
	st := m2.State()
	if len(st.CIDs) != 2 || st.AuditHead < cp.Sequence {
		t.Fatalf("recovered state = %+v", st)
	}
	if st.CIDs[0].State != CIDRetired || st.CIDs[1].State != CIDActive {
		t.Fatalf("recovered CID states = %v, %v", st.CIDs[0].State, st.CIDs[1].State)
	}
	if tok, ok := m2.TokenForCID(g0.CID); !ok || !tok.Equal(g0.Token) {
		t.Fatal("recovered retired token mismatch")
	}
	if tok, ok := m2.TokenForCID(g1.CID); !ok || !tok.Equal(g1.Token) {
		t.Fatal("recovered active token mismatch")
	}

	records, err := journal.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("empty journal")
	}
	if err := journal.Append(records[len(records)-1]); !errors.Is(err, ErrJournalRollback) {
		t.Fatalf("duplicate append error = %v", err)
	}

	// A journal prefix behind the signed checkpoint is a rollback attack.
	prefix, err := NewMemoryJournalFrom(records[:len(records)-1])
	if err != nil {
		t.Fatal(err)
	}
	m3 := mustManager(t, cfg)
	if err := m3.Recover(prefix, cp); !errors.Is(err, ErrRollbackDetected) {
		t.Fatalf("truncated log recovery error = %v, want rollback", err)
	}
	if err := m2.Recover(journal, cp); !errors.Is(err, ErrAlreadyRecovered) {
		t.Fatalf("second recovery error = %v", err)
	}

	// CID tombstones survive recovery: the recovered process cannot reissue
	// the old value even though it is quarantined.
	if !m2.ledger.Used(g0.CID) {
		t.Fatal("recovered ledger lost CID tombstone")
	}
	if !m2.tokenLedger.Used(g0.Token) || !m2.tokenLedger.Used(g1.Token) {
		t.Fatal("recovered token ledger lost issued token tombstones")
	}
}

func TestCrashRecoveryExpiresCIDsWhoseDeadlinePassedWhileDown(t *testing.T) {
	clock := NewManualClock(time.Unix(800, 0).UTC())
	journal := NewMemoryJournal()
	m1 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x31}, 8), journal, "edge-a", "c1"))
	g, err := m1.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.RetireFromPeer(g.Sequence); err != nil {
		t.Fatal(err)
	}
	cp, err := m1.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(20 * time.Second)
	m2 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x31}, 8), journal, "edge-a", "c1"))
	if err := m2.Recover(journal, cp); err != nil {
		t.Fatal(err)
	}
	if info, _ := m2.CIDInfo(g.CID); info.State != CIDExpired {
		t.Fatalf("state after downtime = %v", info.State)
	}
	if _, err := m2.DestroyKeyGeneration(0); !errors.Is(err, ErrCurrentKeyGeneration) {
		t.Fatalf("current key destroy error = %v", err)
	}
}

func TestFileJournalSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	fj, err := OpenFileJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := NewManualClock(time.Unix(900, 0).UTC())
	cfg := testConfig(clock, SequentialCIDGenerator([]byte{0x41}, 8), fj, "edge-a", "c1")
	m1 := mustManager(t, cfg)
	g, err := m1.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.RetireFromPeer(g.Sequence); err != nil {
		t.Fatal(err)
	}
	cp, err := m1.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := fj.Close(); err != nil {
		t.Fatal(err)
	}
	fj2, err := OpenFileJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fj2.Close()
	m2 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x41}, 8), fj2, "edge-a", "c1"))
	if err := m2.Recover(fj2, cp); err != nil {
		t.Fatalf("file recovery: %v", err)
	}
	if info, _ := m2.CIDInfo(g.CID); info.State != CIDRetired {
		t.Fatalf("state after file recovery = %v", info.State)
	}
}

type limitedJournal struct {
	base  *MemoryJournal
	limit int
}

func (l *limitedJournal) Append(w AuditWitness) error {
	records, _ := l.base.Records()
	if len(records) >= l.limit {
		return errors.New("injected journal failure")
	}
	return l.base.Append(w)
}

func (l *limitedJournal) Records() ([]AuditWitness, error) { return l.base.Records() }

func TestJournalFailureDoesNotRotateKeyOrSplitClose(t *testing.T) {
	clock := NewManualClock(time.Unix(1700, 0).UTC())
	base := NewMemoryJournal()
	limited := &limitedJournal{base: base, limit: 1_000_000}
	m := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0xa1}, 8), limited, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfirmPeerUse(g.CID); err != nil {
		t.Fatal(err)
	}
	cp, err := m.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}

	// Rotation records intent before mutating key material. A journal failure
	// must leave the generation unchanged.
	limited.limit = 2
	if _, err := m.RotateKeys(); err == nil {
		t.Fatal("rotation succeeded despite journal failure")
	}
	if m.CurrentKeyGeneration() != 0 {
		t.Fatalf("failed rotation changed generation to %d", m.CurrentKeyGeneration())
	}

	// Allow connection.closed, then fail the first cid.closed. Recovery must
	// finish the deterministic close from the durable prefix.
	limited.limit = 3
	if _, err := m.Close("local close"); err == nil {
		t.Fatal("close succeeded despite journal failure")
	}
	m2 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0xa2}, 8), base, "edge-a", "c1"))
	if err := m2.Recover(base, cp); err != nil {
		t.Fatalf("partial-close recovery: %v", err)
	}
	if !m2.IsClosed() {
		t.Fatal("recovery did not complete durable connection close")
	}
	if info, _ := m2.CIDInfo(g.CID); info.State != CIDClosed {
		t.Fatalf("recovered CID state = %v", info.State)
	}
}
