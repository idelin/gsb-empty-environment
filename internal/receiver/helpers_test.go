package receiver

import (
	"crypto/ed25519"
	"sync"
	"testing"

	"example.org/offlinecli/internal/checkpoint"
	"example.org/offlinecli/internal/tlog"
)

const testOrigin checkpoint.Origin = "update.example/log"

// testLog is an in-memory append-only transparent log used to generate valid
// checkpoints and consistency proofs.
type testLog struct {
	mu     sync.Mutex
	leaves []tlog.Hash
	signer *checkpoint.Signer
}

func newTestLog(t *testing.T) (*testLog, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := checkpoint.NewSigner("update-log", testOrigin, priv)
	if err != nil {
		t.Fatal(err)
	}
	return &testLog{signer: signer}, pub
}

func (l *testLog) append(records ...[]byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, rec := range records {
		l.leaves = append(l.leaves, tlog.RecordHash(rec))
	}
}

func (l *testLog) size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.leaves))
}

// snapshot returns copies of the leaves up to size n.
func (l *testLog) snapshot(n int64) []tlog.Hash {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > int64(len(l.leaves)) {
		n = int64(len(l.leaves))
	}
	out := make([]tlog.Hash, n)
	copy(out, l.leaves[:n])
	return out
}

func (l *testLog) root(n int64) tlog.Hash {
	return tlog.TreeHash(l.snapshot(n))
}

// checkpointAt returns a valid signed checkpoint for the tree of size n.
func (l *testLog) checkpointAt(t *testing.T, n int64) []byte {
	t.Helper()
	env, err := l.signer.Sign(n, l.root(n))
	if err != nil {
		t.Fatal(err)
	}
	return env.Text
}

func (l *testLog) proof(oldSize, newSize int64) ([]tlog.Hash, error) {
	return tlog.ConsistencyProof(l.snapshot(newSize), oldSize, newSize)
}

// honestMirror always serves the current head and correct proofs.
type honestMirror struct {
	log *testLog
}

func (m *honestMirror) Latest() ([]byte, error) {
	n := m.log.size()
	env, err := m.log.signer.Sign(n, m.log.root(n))
	if err != nil {
		return nil, err
	}
	return env.Text, nil
}

func (m *honestMirror) Consistency(oldSize, newSize int64) ([]tlog.Hash, error) {
	return m.log.proof(oldSize, newSize)
}

// staleMirror serves a fixed, older checkpoint regardless of log growth.
type staleMirror struct {
	log       *testLog
	staleSize int64
}

func (m *staleMirror) Latest() ([]byte, error) {
	env, err := m.log.signer.Sign(m.staleSize, m.log.root(m.staleSize))
	if err != nil {
		return nil, err
	}
	return env.Text, nil
}

func (m *staleMirror) Consistency(oldSize, newSize int64) ([]tlog.Hash, error) {
	return m.log.proof(oldSize, newSize)
}

// badProofMirror serves the right checkpoint but garbage consistency proofs.
type badProofMirror struct {
	log *testLog
}

func (m *badProofMirror) Latest() ([]byte, error) {
	n := m.log.size()
	env, err := m.log.signer.Sign(n, m.log.root(n))
	if err != nil {
		return nil, err
	}
	return env.Text, nil
}

func (m *badProofMirror) Consistency(oldSize, newSize int64) ([]tlog.Hash, error) {
	good, err := m.log.proof(oldSize, newSize)
	if err != nil {
		return nil, err
	}
	bad := make([]tlog.Hash, len(good))
	copy(bad, good)
	for i := range bad {
		bad[i][0] ^= 0xff
	}
	return bad, nil
}

// openReceiver creates a receiver in a fresh per-test directory.
func openReceiver(t *testing.T, pub ed25519.PublicKey) *Receiver {
	t.Helper()
	dir := t.TempDir()
	r, err := Open(dir, testOrigin, pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if r.hasLock() {
			r.Close()
		}
	})
	return r
}

// reopen closes r and opens a fresh receiver over the same directory, modeling a
// client restart within the same process.
func reopen(t *testing.T, r *Receiver, pub ed25519.PublicKey) *Receiver {
	t.Helper()
	dir := r.Dir()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r2, err := Open(dir, testOrigin, pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if r2.hasLock() {
			r2.Close()
		}
	})
	return r2
}
