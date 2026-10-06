package snaprecv

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

var errCrash = errors.New("snaprecv: injected crash")

// deliver calls HandleChunk and converts an injected crash panic into a
// flag, modelling an abrupt process death (the receiver is abandoned).
func deliver(r *Receiver, c Chunk) (err error, crashed bool) {
	defer func() {
		if rec := recover(); rec != nil {
			if rec == errCrash {
				crashed = true
				return
			}
			panic(rec)
		}
	}()
	err = r.HandleChunk(c)
	return
}

// crashHook panics once at the armed cut point.
type crashHook struct {
	point HookPoint
	armed *bool
	fired *bool
}

func (h crashHook) hook(p HookPoint) {
	if p == h.point && *h.armed && !*h.fired {
		*h.fired = true
		panic(errCrash)
	}
}

func makeChunks(version uint64, size, step int) []Chunk {
	data := content(version, size)
	var out []Chunk
	for off := 0; off < size; off += step {
		end := off + step
		if end > size {
			end = size
		}
		out = append(out, NewChunk(version, uint64(off), uint64(size), data[off:end]))
	}
	return out
}

// TestCrashAtEveryCutPoint crashes a fresh v1 install at every cut point,
// then verifies recovery either exposes nothing or the complete snapshot,
// and that delivery can always finish afterwards.
func TestCrashAtEveryCutPoint(t *testing.T) {
	points := []HookPoint{
		HookChunkDataWritten,
		HookChunkDataSynced,
		HookManifestSynced,
		HookInstallRenamed,
		HookInstallSymlinked,
		HookInstallCleaned,
	}
	for _, p := range points {
		t.Run(string(p), func(t *testing.T) {
			dir := tempDir(t)
			defer os.RemoveAll(dir)

			armed, fired := true, false
			hook := crashHook{point: p, armed: &armed, fired: &fired}
			r, err := Open(Options{Dir: dir, Hook: hook.hook})
			if err != nil {
				t.Fatal(err)
			}

			chunks := makeChunks(1, 97, 20) // 4 full chunks + 17-byte tail
			crashed := false
			for _, c := range chunks {
				err, did := deliver(r, c)
				if did {
					crashed = true
					break
				}
				if err != nil {
					t.Fatalf("chunk %+v: %v", c, err)
				}
			}
			if !crashed {
				t.Fatalf("cut point %s never fired", p)
			}

			// Recover and check the visible state is untorn.
			r2, err := Open(Options{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			b, v, err := r2.Read()
			switch {
			case err == ErrNoSnapshot:
				// Nothing installed yet: acceptable.
			case err != nil:
				t.Fatalf("unexpected read error: %v", err)
			default:
				if v != 1 || !bytes.Equal(b, content(1, 97)) {
					t.Fatalf("torn visible state: v=%d len=%d", v, len(b))
				}
			}

			// Redeliver everything; verified chunks are reused or the
			// snapshot is already installed (stale). It must complete.
			for _, c := range chunks {
				if err := r2.HandleChunk(c); err != nil && err != ErrStaleVersion {
					t.Fatalf("redeliver: %v", err)
				}
			}
			b, v, err = r2.Read()
			if err != nil || v != 1 || !bytes.Equal(b, content(1, 97)) {
				t.Fatalf("final state: v=%d err=%v", v, err)
			}
		})
	}
}

// TestInstallInterruptPreservesVisible crashes a v2 install while v1 is
// visible; recovery must expose either the intact old or the complete new
// snapshot, never anything torn.
func TestInstallInterruptPreservesVisible(t *testing.T) {
	points := []HookPoint{
		HookChunkDataSynced,  // final chunk's data sync, before manifest
		HookManifestSynced,   // final manifest sync, before install
		HookInstallRenamed,   // staging renamed, before visible swap
		HookInstallSymlinked, // swap done, cleanup pending
	}
	for _, p := range points {
		t.Run(string(p), func(t *testing.T) {
			dir := tempDir(t)
			defer os.RemoveAll(dir)

			armed, fired := false, false
			hook := crashHook{point: p, armed: &armed, fired: &fired}
			r, err := Open(Options{Dir: dir, Hook: hook.hook})
			if err != nil {
				t.Fatal(err)
			}

			// Install v1 with the hook disarmed.
			for _, c := range makeChunks(1, 50, 20) {
				if err := r.HandleChunk(c); err != nil {
					t.Fatal(err)
				}
			}
			if v := r.CurrentVersion(); v != 1 {
				t.Fatalf("v1 not installed: %d", v)
			}

			// Stage v2; arm the hook so the final chunk crashes at p.
			v2chunks := makeChunks(2, 73, 20)
			for _, c := range v2chunks[:len(v2chunks)-1] {
				if err := r.HandleChunk(c); err != nil {
					t.Fatal(err)
				}
			}
			armed = true
			if _, crashed := deliver(r, v2chunks[len(v2chunks)-1]); !crashed {
				t.Fatalf("cut point %s never fired", p)
			}

			// Recover: visible state must be intact v1 or complete v2.
			r2, err := Open(Options{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			b, v, err := r2.Read()
			if err != nil {
				t.Fatalf("read after recovery: %v", err)
			}
			switch v {
			case 1:
				if !bytes.Equal(b, content(1, 50)) {
					t.Fatal("v1 visible state torn")
				}
			case 2:
				if !bytes.Equal(b, content(2, 73)) {
					t.Fatal("v2 visible state torn")
				}
			default:
				t.Fatalf("unexpected visible version %d", v)
			}

			// Finish v2; redelivery tolerates already-installed state.
			for _, c := range v2chunks {
				if err := r2.HandleChunk(c); err != nil && err != ErrStaleVersion {
					t.Fatalf("redeliver: %v", err)
				}
			}
			b, v, err = r2.Read()
			if err != nil || v != 2 || !bytes.Equal(b, content(2, 73)) {
				t.Fatalf("final state: v=%d err=%v", v, err)
			}
			// v1 chunks can never overwrite the newer snapshot.
			for _, c := range makeChunks(1, 50, 20) {
				if err := r2.HandleChunk(c); err != ErrStaleVersion {
					t.Fatalf("stale v1 chunk: %v", err)
				}
			}
			if b, v, _ = r2.Read(); v != 2 || !bytes.Equal(b, content(2, 73)) {
				t.Fatal("stale chunks overwrote visible state")
			}
		})
	}
}
