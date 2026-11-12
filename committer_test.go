package taskpkg_test

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"sync"
	"testing"

	"example.org/domainreissue/q029"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

type recordingWitness struct {
	local     *taskpkg.LocalWitness
	mu        sync.Mutex
	requests  []taskpkg.CommitmentRequest
	responses []taskpkg.Confirmation
	fail      error
}

func (w *recordingWitness) RequestConfirmation(ctx context.Context, req taskpkg.CommitmentRequest) (taskpkg.Confirmation, error) {
	w.mu.Lock()
	fail := w.fail
	w.mu.Unlock()
	if fail != nil {
		w.mu.Lock()
		w.requests = append(w.requests, req)
		w.mu.Unlock()
		return taskpkg.Confirmation{}, fail
	}
	confirmation, err := w.local.RequestConfirmation(ctx, req)
	if err != nil {
		return taskpkg.Confirmation{}, err
	}
	w.mu.Lock()
	w.requests = append(w.requests, req)
	w.responses = append(w.responses, confirmation)
	w.mu.Unlock()
	return confirmation, nil
}

func (w *recordingWitness) PublicKey() ed25519.PublicKey { return w.local.PublicKey() }

func (w *recordingWitness) snapshot() []taskpkg.Confirmation {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]taskpkg.Confirmation, len(w.responses))
	copy(out, w.responses)
	return out
}

type replayWitness struct {
	pub ed25519.PublicKey
	msg taskpkg.Confirmation
}

func (w *replayWitness) RequestConfirmation(_ context.Context, _ taskpkg.CommitmentRequest) (taskpkg.Confirmation, error) {
	return w.msg, nil
}

func (w *replayWitness) PublicKey() ed25519.PublicKey { return w.pub }

type effectRecorder struct {
	mu    sync.Mutex
	calls int
}

func (e *effectRecorder) effect(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return nil
}

func (e *effectRecorder) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func newRecoveredCommitter(t *testing.T, witness taskpkg.Witness, counter taskpkg.MonotonicCounter) (*taskpkg.Committer, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "audit.log")
	committer, err := taskpkg.Recover(context.Background(), taskpkg.Config{
		LogPath: logPath,
		Witness: witness,
		Counter: counter,
	})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	t.Cleanup(func() { _ = committer.Close() })
	return committer, logPath
}

func sampleOp(subject, summary string) taskpkg.Operation {
	return taskpkg.Operation{Subject: subject, Summary: summary, Payload: []byte("body-" + summary)}
}
