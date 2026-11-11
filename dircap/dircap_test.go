package dircap

import (
	"errors"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"example.org/offlinecli/dircap/memfs"
)

func slotOf(h Handle) int { return int(uint32(uint64(h))) }

func assertReleased(t *testing.T, d *memfs.Dir) {
	t.Helper()
	select {
	case <-d.Released():
	case <-time.After(2 * time.Second):
		t.Fatal("directory was not released")
	}
}

func assertNotReleased(t *testing.T, d *memfs.Dir) {
	t.Helper()
	select {
	case <-d.Released():
		t.Fatal("directory released too early")
	case <-time.After(50 * time.Millisecond):
	}
}

func waitBlocked(t *testing.T, d *memfs.Dir, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for d.Blocked() != n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d blocked readers, got %d", n, d.Blocked())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDeriveCanOnlyNarrow(t *testing.T) {
	reg := NewRegistry()
	d := memfs.NewDir()
	d.Write("a", "hello")

	root := reg.Register(d, OpRead|OpList)
	if _, err := reg.Derive(root, OpRead|OpWrite); !errors.Is(err, ErrOpsEscalation) {
		t.Fatalf("widening derive: expected ErrOpsEscalation, got %v", err)
	}
	child, err := reg.Derive(root, OpRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Derive(child, OpRead|OpList); !errors.Is(err, ErrOpsEscalation) {
		t.Fatalf("grandchild widening: expected ErrOpsEscalation, got %v", err)
	}
	grand, err := reg.Derive(child, OpRead)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := reg.Open(grand, "a")
	if err != nil {
		t.Fatal(err)
	}
	rf.Release()

	wo := reg.Register(memfs.NewDir(), OpWrite)
	if _, err := reg.Open(wo, "a"); !errors.Is(err, ErrOpNotGranted) {
		t.Fatalf("read without OpRead: expected ErrOpNotGranted, got %v", err)
	}
}

func TestCloseRevokesSubtree(t *testing.T) {
	reg := NewRegistry()
	d := memfs.NewDir()
	d.Write("a", "hello")

	root := reg.Register(d, OpRead|OpWrite)
	child, err := reg.Derive(root, OpRead)
	if err != nil {
		t.Fatal(err)
	}
	grand, err := reg.Derive(child, OpRead)
	if err != nil {
		t.Fatal(err)
	}

	if err := reg.Close(root); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]Handle{"root": root, "child": child, "grand": grand} {
		if _, err := reg.Open(h, "a"); !errors.Is(err, ErrRevoked) {
			t.Fatalf("%s: open: expected ErrRevoked, got %v", name, err)
		}
		if _, err := reg.Derive(h, OpRead); !errors.Is(err, ErrRevoked) {
			t.Fatalf("%s: derive: expected ErrRevoked, got %v", name, err)
		}
	}
	// No read was in flight, so the directory is released immediately.
	assertReleased(t, d)
}

func TestHandleSlotReuse(t *testing.T) {
	reg := NewRegistry()
	d1, d2 := memfs.NewDir(), memfs.NewDir()

	h1 := reg.Register(d1, OpRead)
	if err := reg.Close(h1); err != nil {
		t.Fatal(err)
	}
	// Closed but not yet recycled: reports revoked.
	if _, err := reg.Open(h1, "x"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked, got %v", err)
	}

	h2 := reg.Register(d2, OpRead)
	if h2 == h1 {
		t.Fatal("recycled handle must differ from the stale one")
	}
	if slotOf(h1) != slotOf(h2) {
		t.Fatalf("expected slot reuse: h1 slot %d, h2 slot %d", slotOf(h1), slotOf(h2))
	}
	// The stale handle now aliases a recycled slot: invalid, not revoked.
	if _, err := reg.Open(h1, "x"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("expected ErrInvalidHandle, got %v", err)
	}
	if err := reg.Close(h1); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("stale close: expected ErrInvalidHandle, got %v", err)
	}
}

func TestInFlightReadSurvivesRevocation(t *testing.T) {
	reg := NewRegistry()
	d1 := memfs.NewDir()
	payload := strings.Repeat("x", 1<<20)
	d1.Write("big", payload)
	h1 := reg.Register(d1, OpRead)

	rf, err := reg.Open(h1, "big")
	if err != nil {
		t.Fatal(err)
	}

	d1.Block()
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, len(payload))
		_, err := io.ReadFull(rf, buf)
		if err == nil && string(buf) != payload {
			err = errors.New("payload mismatch")
		}
		readErr <- err
	}()
	waitBlocked(t, d1, 1)

	// Revoke while the read is blocked: the object must stay alive.
	if err := reg.Close(h1); err != nil {
		t.Fatal(err)
	}
	assertNotReleased(t, d1)
	if _, err := reg.Open(h1, "big"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked, got %v", err)
	}

	// The freed slot is recycled even though the old node is still pinned.
	d2 := memfs.NewDir()
	h2 := reg.Register(d2, OpRead)
	if slotOf(h1) != slotOf(h2) {
		t.Fatal("expected slot recycling while the old read is pinned")
	}
	if _, err := reg.Open(h1, "big"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("expected ErrInvalidHandle, got %v", err)
	}

	// The blocked read still completes; only the last release frees d1.
	d1.Unblock()
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
	assertNotReleased(t, d1)
	rf.Release()
	assertReleased(t, d1)
	assertNotReleased(t, d2)
}

func TestCapabilitySurvivesHotReload(t *testing.T) {
	// The registry is host-scoped: a plugin restart does not invalidate
	// handles the old instance registered.
	reg := NewRegistry()
	d := memfs.NewDir()
	d.Write("state", "v1")
	h := reg.Register(d, OpRead) // registered by the old instance

	// ... plugin process restarts; the host keeps the registry ...

	rf, err := reg.Open(h, "state") // used by the new instance
	if err != nil {
		t.Fatal(err)
	}
	buf, err := io.ReadAll(rf)
	if err != nil {
		t.Fatal(err)
	}
	rf.Release()
	if string(buf) != "v1" {
		t.Fatalf("got %q, want %q", buf, "v1")
	}
}

func TestDerivationRace(t *testing.T) {
	reg := NewRegistry()
	d := memfs.NewDir()
	d.Write("f", "data")

	var poolMu sync.Mutex
	pool := []Handle{reg.Register(d, OpRead|OpWrite|OpList)}

	const workers = 8
	var wg sync.WaitGroup
	deadline := time.Now().Add(300 * time.Millisecond)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			buf := make([]byte, 4)
			for time.Now().Before(deadline) {
				poolMu.Lock()
				h := pool[rng.Intn(len(pool))]
				poolMu.Unlock()

				switch rng.Intn(4) {
				case 0: // derive, sometimes attempting to widen
					ops := OpRead
					if rng.Intn(2) == 0 {
						ops = OpRead | OpWrite | OpList | OpExec
					}
					nh, err := reg.Derive(h, ops)
					switch {
					case err == nil:
						poolMu.Lock()
						pool = append(pool, nh)
						poolMu.Unlock()
					case errors.Is(err, ErrOpsEscalation),
						errors.Is(err, ErrRevoked),
						errors.Is(err, ErrInvalidHandle):
					default:
						t.Errorf("unexpected derive error: %v", err)
					}
				case 1: // close, possibly stale or already closed
					if err := reg.Close(h); err != nil &&
						!errors.Is(err, ErrInvalidHandle) &&
						!errors.Is(err, ErrRevoked) {
						t.Errorf("unexpected close error: %v", err)
					}
				case 2: // open, read, release
					rf, err := reg.Open(h, "f")
					if err != nil {
						if !errors.Is(err, ErrRevoked) &&
							!errors.Is(err, ErrInvalidHandle) &&
							!errors.Is(err, ErrOpNotGranted) {
							t.Errorf("unexpected open error: %v", err)
						}
						continue
					}
					if _, err := rf.Read(buf); err != nil {
						t.Errorf("read: %v", err)
					}
					rf.Release()
				case 3: // register a fresh root capability
					poolMu.Lock()
					pool = append(pool, reg.Register(d, OpRead))
					poolMu.Unlock()
				}
			}
		}(int64(w))
	}
	wg.Wait()

	poolMu.Lock()
	for _, h := range pool {
		reg.Close(h)
	}
	poolMu.Unlock()

	select {
	case <-d.Released():
	case <-time.After(2 * time.Second):
		t.Fatal("directory still pinned after revoking every capability")
	}
}
