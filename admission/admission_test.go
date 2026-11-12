package admission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
)

// ---- test helpers ---------------------------------------------------------

func shaDigest(t *testing.T, data []byte) Digest {
	t.Helper()
	sum := sha256.Sum256(data)
	return Digest{Hash: hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

func fakeDigest(hash string, size int64) Digest { return Digest{Hash: hash, Size: size} }

var (
	trunkIdentity = Identity{Name: "ci-trunk", Source: "trunk", Trusted: true}
	prIdentity    = Identity{Name: "pr-1234", Source: "pr/1234", Trusted: false}
)

func trustedCtx() context.Context {
	return WithIdentity(context.Background(), trunkIdentity)
}

func prCtx() context.Context {
	return WithIdentity(context.Background(), prIdentity)
}

// recordingLogger captures every event for assertions.
type recordingLogger struct {
	events []Event
}

func (l *recordingLogger) Log(e Event) { l.events = append(l.events, e) }

func (l *recordingLogger) last() Event {
	if len(l.events) == 0 {
		return Event{}
	}
	return l.events[len(l.events)-1]
}

func newTestGuard(t *testing.T) (*Guard, *MemoryStore, *recordingLogger) {
	t.Helper()
	store := NewMemoryStore()
	log := &recordingLogger{}
	return NewGuard(store, log), store, log
}

func assertErrorCode(t *testing.T, err error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("error code = %s, want %s (err=%v)", got, want, err)
	}
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error matching %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("error %v does not match %v", err, target)
	}
}

func seedBlob(t *testing.T, store Store, data []byte) Digest {
	t.Helper()
	d := shaDigest(t, data)
	if err := store.PutBlob(context.Background(), d, data); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	return d
}

func blobResource(d Digest) string {
	return "builds/test/blobs/" + d.Hash + "/" + strconv.FormatInt(d.Size, 10)
}

func uploadResource(d Digest) string {
	return "builds/test/blobs/uploads/uuid-1?hash=" + d.Hash + "&size_bytes=" + strconv.FormatInt(d.Size, 10)
}
