package snaprecv

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// modelSnapshot is one version the model knows about.
type modelSnapshot struct {
	data []byte
	meta Meta
}

// TestModelChunkByChunk runs randomized operation sequences against the
// store and checks, chunk by chunk, that:
//
//   - the visible state is always exactly some full snapshot (never torn)
//   - installed versions never go backwards
//   - every chunk the store reports validated matches the model's bytes
//   - overlapping/conflicting and stale-version writes never disturb it
//   - crashes at any cut point preserve all of the above after recovery
func TestModelChunkByChunk(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runModel(t, seed)
		})
	}
}

func runModel(t *testing.T, seed int64) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(seed))

	// Three versions; v2 has a tail chunk (size not a multiple of the
	// chunk size), v3 shares its chunk size with v1 to exercise overlap.
	snaps := map[uint64]modelSnapshot{}
	for _, spec := range []struct {
		v, size, chunk uint64
	}{{1, 300, 64}, {2, 200, 64}, {3, 320, 64}} {
		data, meta := buildSnapshot(seed*100+int64(spec.v), int(spec.size), int64(spec.chunk), spec.v)
		snaps[spec.v] = modelSnapshot{data: data, meta: meta}
	}

	s := mustOpen(t, dir, nil)

	var (
		installedVer   uint64 // 0 = nothing installed
		crashed        bool
		armedCut       CutPoint
		armed          bool
		pendingInstall uint64 // install in flight when a crash hit
	)
	s.hook = func(cp CutPoint) error {
		if armed && cp == armedCut {
			armed, crashed = false, true
			return errors.New("injected crash")
		}
		return nil
	}

	// checkVisible verifies the visible state against the model.
	checkVisible := func() {
		t.Helper()
		v, ok := s.InstalledVersion()
		if installedVer == 0 {
			if ok && pendingInstall == 0 {
				t.Fatalf("store installed v%d from nothing", v)
			}
		}
		if !ok {
			return
		}
		// After a crash mid-install, either side of the swap is legal.
		if v != installedVer && v != pendingInstall {
			t.Fatalf("installed v%d, model allows v%d or v%d", v, installedVer, pendingInstall)
		}
		if v < installedVer {
			t.Fatalf("installed version went backwards: %d < %d", v, installedVer)
		}
		got := readInstalled(t, s)
		if !bytes.Equal(got, snaps[v].data) {
			t.Fatalf("visible state of v%d is torn", v)
		}
		installedVer = v
		pendingInstall = 0
	}

	// checkStagedChunks verifies every validated chunk byte-for-byte
	// against the model, straight from the on-disk pool.
	checkStagedChunks := func(ver uint64) {
		t.Helper()
		snap := snaps[ver]
		_, flags, ok := s.Staged(ver)
		if !ok {
			return
		}
		for i, validated := range flags {
			if !validated {
				continue
			}
			onDisk, err := os.ReadFile(filepath.Join(dir, "pool", snap.meta.Chunks[i]))
			if err != nil {
				t.Fatalf("v%d chunk %d flagged validated but missing: %v", ver, i, err)
			}
			if !bytes.Equal(onDisk, chunkOf(snap.data, snap.meta, i)) {
				t.Fatalf("v%d chunk %d content diverged from model", ver, i)
			}
		}
	}

	crashAndReopen := func() {
		t.Helper()
		var err error
		s, err = Open(dir, s.hook)
		if err != nil {
			t.Fatalf("reopen after crash: %v", err)
		}
		checkVisible()
		for ver := range snaps {
			checkStagedChunks(ver)
		}
	}

	const ops = 400
	for step := 0; step < ops; step++ {
		ver := uint64(1 + rng.Intn(3))
		snap := snaps[ver]
		crashed = false

		// Randomly arm a one-shot crash at a random cut point.
		if rng.Intn(10) == 0 {
			armed = true
			armedCut = CutPoints()[rng.Intn(len(CutPoints()))]
		}

		switch rng.Intn(8) {
		case 0: // offer manifest
			_, err := s.Offer(snap.meta)
			if crashed {
				crashAndReopen()
			} else if err != nil && !errors.Is(err, ErrStaleVersion) {
				t.Fatalf("step %d: Offer v%d: %v", step, ver, err)
			}

		case 1, 2, 3: // write a good chunk (possibly duplicate)
			idx := rng.Intn(snap.meta.NumChunks())
			err := s.WriteChunk(ver, idx, chunkOf(snap.data, snap.meta, idx))
			if crashed {
				crashAndReopen()
			} else if err != nil && !errors.Is(err, ErrStaleVersion) && !errors.Is(err, ErrUnknownVersion) {
				t.Fatalf("step %d: WriteChunk v%d[%d]: %v", step, ver, idx, err)
			} else if err == nil {
				checkStagedChunks(ver)
			}

		case 4: // overlapping conflicting chunk: right length, wrong bytes
			idx := rng.Intn(snap.meta.NumChunks())
			bad := make([]byte, snap.meta.ChunkLen(idx))
			rng.Read(bad)
			err := s.WriteChunk(ver, idx, bad)
			if crashed {
				crashAndReopen()
			} else if err == nil {
				// Acceptable only if random bytes collided with the hash
				// (negligible) — treat as failure to be strict.
				t.Fatalf("step %d: corrupt chunk v%d[%d] accepted", step, ver, idx)
			} else if !errors.Is(err, ErrChunkHashMismatch) && !errors.Is(err, ErrStaleVersion) && !errors.Is(err, ErrUnknownVersion) {
				t.Fatalf("step %d: corrupt chunk error = %v", step, err)
			}
			checkStagedChunks(ver)

		case 5: // validate
			err := s.Validate(ver)
			if crashed {
				crashAndReopen()
			} else if err != nil && !errors.Is(err, ErrIncomplete) && !errors.Is(err, ErrUnknownVersion) {
				t.Fatalf("step %d: Validate v%d: %v", step, ver, err)
			}

		case 6: // install
			err := s.Install(ver)
			if crashed {
				pendingInstall = ver
				crashAndReopen()
			} else if err == nil {
				if installedVer != 0 && ver <= installedVer {
					t.Fatalf("step %d: installed stale v%d over v%d", step, ver, installedVer)
				}
				installedVer = ver
				checkVisible()
			} else if !errors.Is(err, ErrStaleVersion) && !errors.Is(err, ErrUnknownVersion) && !errors.Is(err, ErrIncomplete) {
				t.Fatalf("step %d: Install v%d: %v", step, ver, err)
			}

		case 7: // simulated process restart without injected fault
			crashAndReopen()
		}
	}

	// Convergence: from any state, cleanly delivering the highest version
	// must install it and every byte must match the model.
	top := snaps[3]
	if _, err := s.Offer(top.meta); err != nil {
		// v3 may already be installed from the random phase.
		if !errors.Is(err, ErrStaleVersion) || installedVer != 3 {
			t.Fatalf("final Offer: %v", err)
		}
		if got := readInstalled(t, s); !bytes.Equal(got, top.data) {
			t.Fatalf("final visible state diverged from model")
		}
		return
	}
	for i := 0; i < top.meta.NumChunks(); i++ {
		if err := s.WriteChunk(3, i, chunkOf(top.data, top.meta, i)); err != nil {
			t.Fatalf("final WriteChunk %d: %v", i, err)
		}
	}
	if err := s.Validate(3); err != nil {
		t.Fatalf("final Validate: %v", err)
	}
	if err := s.Install(3); err != nil {
		t.Fatalf("final Install: %v", err)
	}
	if got := readInstalled(t, s); !bytes.Equal(got, top.data) {
		t.Fatalf("final visible state diverged from model")
	}
}
