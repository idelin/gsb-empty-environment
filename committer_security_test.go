package taskpkg_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"example.org/domainreissue/q029"
)

func TestTamperedAndTruncatedLogRejected(t *testing.T) {
	_, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	counter := taskpkg.NewMemoryCounter()
	committer, logPath := newRecoveredCommitter(t, witness, counter)
	ctx := context.Background()

	for _, summary := range []string{"op-1", "op-2"} {
		if err := committer.Commit(ctx, sampleOp("admin-1", summary), nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = committer.Close()

	// Modify the persisted summary on the first line. It changes the record
	// without touching the signature, so signature verification must fail.
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	pristine := string(raw)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	lines[0] = strings.Replace(lines[0], `"summary":"op-1"`, `"summary":"op-9"`, 1)
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskpkg.Open(ctx, taskpkg.Config{LogPath: logPath, Witness: witness, Counter: counter}); !errors.Is(err, taskpkg.ErrLogTampered) {
		t.Fatalf("tampered log: %v", err)
	}

	// Truncation: delete the second line; the counter still expects sequence 3
	// while the restored log ends at sequence 1.
	if err := os.WriteFile(logPath, []byte(lines[1]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskpkg.Open(ctx, taskpkg.Config{LogPath: logPath, Witness: witness, Counter: counter}); !errors.Is(err, taskpkg.ErrLogTampered) && !errors.Is(err, taskpkg.ErrLogCounterDesync) {
		t.Fatalf("truncated log must fail closed, got %v", err)
	}

	// A cleanly truncated prefix (sequence 1 only) has a valid chain on its own
	// but disagrees with the live counter, which already advanced past it.
	pristineLines := strings.Split(strings.TrimRight(pristine, "\n"), "\n")
	if err := os.WriteFile(logPath, []byte(pristineLines[0]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskpkg.Open(ctx, taskpkg.Config{LogPath: logPath, Witness: witness, Counter: counter}); !errors.Is(err, taskpkg.ErrLogCounterDesync) {
		t.Fatalf("clean truncation must report counter desync, got %v", err)
	}
}

func TestAdminDeletesAuditFile(t *testing.T) {
	_, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	counter := taskpkg.NewMemoryCounter()
	committer, logPath := newRecoveredCommitter(t, witness, counter)
	ctx := context.Background()

	if err := committer.Commit(ctx, sampleOp("admin-1", "op-1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := committer.Close(); err != nil {
		t.Fatal(err)
	}

	// Administrator removes the audit file. Reopening must refuse to proceed.
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if _, err := taskpkg.Open(ctx, taskpkg.Config{LogPath: logPath, Witness: witness, Counter: counter}); !errors.Is(err, taskpkg.ErrLogMissing) {
		t.Fatalf("deleted log: %v", err)
	}

	// Recovery requires a fresh counter; the live (advanced) counter is rejected.
	if _, err := taskpkg.Recover(ctx, taskpkg.Config{LogPath: logPath, Witness: witness, Counter: counter}); !errors.Is(err, taskpkg.ErrLogCounterDesync) {
		t.Fatalf("recover with reused counter: %v", err)
	}
	freshCounter := taskpkg.NewMemoryCounter()
	recovered, err := taskpkg.Recover(ctx, taskpkg.Config{LogPath: logPath, Witness: witness, Counter: freshCounter})
	if err != nil {
		t.Fatalf("explicit recovery: %v", err)
	}
	defer recovered.Close()
	if len(recovered.Entries()) != 0 {
		t.Fatal("recovered log must start empty")
	}
}

type forgedWitness struct {
	pub       ed25519.PublicKey
	otherPriv ed25519.PrivateKey
}

func (w *forgedWitness) RequestConfirmation(_ context.Context, req taskpkg.CommitmentRequest) (taskpkg.Confirmation, error) {
	confirmation := taskpkg.Confirmation{
		Sequence:        req.Sequence,
		Subject:         req.Operation.Subject,
		OperationDigest: req.OperationDigest,
		PriorCommit:     req.PriorCommit,
	}
	signer := taskpkg.NewLocalWitness(w.otherPriv)
	forged, err := signer.RequestConfirmation(context.Background(), req)
	if err != nil {
		return taskpkg.Confirmation{}, err
	}
	confirmation.Signature = forged.Signature
	return confirmation, nil
}

func (w *forgedWitness) PublicKey() ed25519.PublicKey { return w.pub }

func TestForgedSignatureRejected(t *testing.T) {
	pub, _ := mustKey(t)
	_, attackerPriv := mustKey(t)
	witness := &forgedWitness{pub: pub, otherPriv: attackerPriv}
	committer, _ := newRecoveredCommitter(t, witness, taskpkg.NewMemoryCounter())

	effect := &effectRecorder{}
	err := committer.Commit(context.Background(), sampleOp("admin-1", "op"), effect.effect)
	if !errors.Is(err, taskpkg.ErrBadSignature) {
		t.Fatalf("forged signature: %v", err)
	}
	if effect.count() != 0 || len(committer.Entries()) != 0 {
		t.Fatal("forged confirmation must produce zero side effects and no log entry")
	}
}

// handlerTransport dispatches HTTP requests to an in-process handler without
// binding a socket, so the HTTP callback path is exercised even in sandboxes
// that disallow listening on ports.
type handlerTransport struct {
	handler http.Handler
	fail    bool
}

func (t2 *handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t2.fail {
		return nil, errors.New("simulated network partition")
	}
	rec := newResponseRecorder()
	t2.handler.ServeHTTP(rec, req)
	return rec.result(req), nil
}

type responseRecorder struct {
	header http.Header
	body   strings.Builder
	status int
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), status: http.StatusOK}
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}
func (r *responseRecorder) WriteHeader(status int) { r.status = status }

func (r *responseRecorder) result(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: r.status,
		Header:     r.header,
		Body:       io.NopCloser(strings.NewReader(r.body.String())),
		Request:    req,
	}
}

func TestHTTPWitnessRoundTripAndPartition(t *testing.T) {
	_, priv := mustKey(t)
	local := taskpkg.NewLocalWitness(priv)
	transport := &handlerTransport{handler: taskpkg.WitnessHandler(local)}

	httpWitness := taskpkg.NewHTTPWitness("http://witness.example/audit", local.PublicKey())
	httpWitness.SetTransport(transport)
	counter := taskpkg.NewMemoryCounter()
	committer, _ := newRecoveredCommitter(t, httpWitness, counter)

	effect := &effectRecorder{}
	if err := committer.Commit(context.Background(), sampleOp("admin-1", "remote-op"), effect.effect); err != nil {
		t.Fatalf("http commit: %v", err)
	}
	if effect.count() != 1 || len(committer.Entries()) != 1 {
		t.Fatal("http witness should produce one committed entry")
	}

	// Partition: the transport now fails every request. The component fails
	// closed; availability under partition is explicitly not guaranteed.
	transport.fail = true
	err := committer.Commit(context.Background(), sampleOp("admin-1", "partitioned"), effect.effect)
	if !errors.Is(err, taskpkg.ErrWitnessUnavailable) {
		t.Fatalf("partition: %v", err)
	}
	if effect.count() != 1 || len(committer.Entries()) != 1 {
		t.Fatal("partition must not trigger the side effect or append an entry")
	}
}

func TestWitnessRejectsMalformedHTTP(t *testing.T) {
	_, priv := mustKey(t)
	local := taskpkg.NewLocalWitness(priv)
	transport := &handlerTransport{handler: taskpkg.WitnessHandler(local)}
	httpWitness := taskpkg.NewHTTPWitness("http://witness.example/audit", local.PublicKey())
	httpWitness.SetTransport(transport)

	req, _ := http.NewRequest(http.MethodPost, "http://witness.example/audit", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", resp.StatusCode)
	}
}

func TestMemoryCounterGuards(t *testing.T) {
	counter := taskpkg.NewMemoryCounter()
	if next, _ := counter.Next(); next != 1 {
		t.Fatalf("first sequence = %d", next)
	}
	if err := counter.Consume(1); err != nil {
		t.Fatal(err)
	}
	if err := counter.Consume(1); !errors.Is(err, taskpkg.ErrCounterRolledBack) {
		t.Fatalf("re-consume: %v", err)
	}
	if err := counter.Consume(3); !errors.Is(err, taskpkg.ErrCounterGap) {
		t.Fatalf("gap: %v", err)
	}
	if err := counter.Consume(2); err != nil {
		t.Fatal(err)
	}
	if next, _ := counter.Next(); next != 3 {
		t.Fatalf("next after consuming 2 = %d", next)
	}
}

func TestValidSignatureStillRejectedWhenSequenceReused(t *testing.T) {
	// A confirmation can carry a perfectly valid Ed25519 signature and still
	// be a replay: the non-rollbackable counter is what proves the sequence was
	// already consumed. Verify that guard directly at the counter boundary.
	_, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	ctx := context.Background()

	req := taskpkg.CommitmentRequest{
		Operation:       sampleOp("admin-1", "op-B"),
		Sequence:        1,
		PriorCommit:     make([]byte, 32),
		OperationDigest: taskpkg.OperationDigest(sampleOp("admin-1", "op-B")),
	}
	if _, err := witness.RequestConfirmation(ctx, req); err != nil {
		t.Fatal(err)
	}

	counter := taskpkg.NewMemoryCounter()
	if err := counter.Consume(1); err != nil { // legitimate first use
		t.Fatal(err)
	}
	err := counter.Consume(1) // replay of the same valid confirmation's sequence
	if !errors.Is(err, taskpkg.ErrCounterRolledBack) {
		t.Fatalf("reused sequence: %v", err)
	}
}
