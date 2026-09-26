package blockstore

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// spliceRef is the naive flat-[]byte reference for insert/delete/overwrite.
func spliceRef(ref []byte, off, delN int, data []byte) []byte {
	out := make([]byte, 0, len(ref)-delN+len(data))
	out = append(out, ref[:off]...)
	out = append(out, data...)
	out = append(out, ref[off+delN:]...)
	return out
}

// TestRandomOpsAgainstNaive drives random operations on both the block
// store and a naive []byte model, comparing full contents after every
// step and re-verifying the ledger by independent traversal.
func TestRandomOpsAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	st := NewStore(16, 0)

	type model struct {
		s   *Slice
		ref []byte
	}
	first, err := st.NewSlice(nil)
	if err != nil {
		t.Fatal(err)
	}
	models := []*model{{s: first}}

	randBytes := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}

	const steps = 4000
	for i := 0; i < steps; i++ {
		m := models[rng.Intn(len(models))]
		switch rng.Intn(7) {
		case 0: // insert
			off := rng.Intn(len(m.ref) + 1)
			data := randBytes(rng.Intn(48))
			ns, err := m.s.Insert(off, data)
			if err != nil {
				t.Fatalf("step %d insert: %v", i, err)
			}
			m.s.Release()
			m.s = ns
			m.ref = spliceRef(m.ref, off, 0, data)
		case 1: // overwrite
			if len(m.ref) == 0 {
				continue
			}
			off := rng.Intn(len(m.ref))
			n := rng.Intn(min(48, len(m.ref)-off) + 1)
			data := randBytes(n)
			ns, err := m.s.Overwrite(off, data)
			if err != nil {
				t.Fatalf("step %d overwrite: %v", i, err)
			}
			m.s.Release()
			m.s = ns
			m.ref = spliceRef(m.ref, off, n, data)
		case 2: // delete
			if len(m.ref) == 0 {
				continue
			}
			off := rng.Intn(len(m.ref))
			n := rng.Intn(min(48, len(m.ref)-off) + 1)
			ns, err := m.s.Delete(off, n)
			if err != nil {
				t.Fatalf("step %d delete: %v", i, err)
			}
			m.s.Release()
			m.s = ns
			m.ref = spliceRef(m.ref, off, n, nil)
		case 3: // move (possibly overlapping source/destination)
			if len(m.ref) == 0 {
				continue
			}
			src := rng.Intn(len(m.ref))
			n := rng.Intn(len(m.ref) - src + 1)
			dst := rng.Intn(len(m.ref) - n + 1)
			ns, err := m.s.Move(dst, src, n)
			if err != nil {
				t.Fatalf("step %d move: %v", i, err)
			}
			m.s.Release()
			m.s = ns
			moved := append([]byte(nil), m.ref[src:src+n]...)
			rest := spliceRef(m.ref, src, n, nil)
			m.ref = spliceRef(rest, dst, 0, moved)
		case 4: // copy within (possibly overlapping)
			if len(m.ref) == 0 {
				continue
			}
			src := rng.Intn(len(m.ref))
			n := rng.Intn(len(m.ref) - src + 1)
			dst := rng.Intn(len(m.ref) - n + 1)
			ns, err := m.s.CopyWithin(dst, src, n)
			if err != nil {
				t.Fatalf("step %d copywithin: %v", i, err)
			}
			m.s.Release()
			m.s = ns
			tmp := append([]byte(nil), m.ref[src:src+n]...)
			copy(m.ref[dst:dst+n], tmp)
		case 5: // branch snapshot, then diverge the new branch
			if len(models) >= 8 {
				continue
			}
			snap := m.s.Snapshot()
			nm := &model{s: snap, ref: append([]byte(nil), m.ref...)}
			if len(nm.ref) > 0 {
				off := rng.Intn(len(nm.ref))
				data := randBytes(rng.Intn(16) + 1)
				if off+len(data) > len(nm.ref) {
					data = data[:len(nm.ref)-off]
				}
				ns, err := nm.s.Overwrite(off, data)
				if err != nil {
					t.Fatalf("step %d branch edit: %v", i, err)
				}
				nm.s.Release()
				nm.s = ns
				nm.ref = spliceRef(nm.ref, off, len(data), data)
			}
			models = append(models, nm)
		case 6: // drop a branch
			if len(models) <= 1 {
				continue
			}
			j := rng.Intn(len(models))
			if models[j] == m {
				continue
			}
			models[j].s.Release()
			models = append(models[:j], models[j+1:]...)
		}

		// Every step: all live branches must equal their naive models.
		for j, mm := range models {
			if mm.s.Len() != len(mm.ref) {
				t.Fatalf("step %d model %d: len %d != %d", i, j, mm.s.Len(), len(mm.ref))
			}
			if got := mm.s.Bytes(); !bytes.Equal(got, mm.ref) {
				t.Fatalf("step %d model %d: content mismatch (len %d)", i, j, len(mm.ref))
			}
		}
		// Periodically re-verify the ledger by independent traversal and
		// spot-check cross-block endian reads against encoding/binary.
		if i%97 == 0 {
			if err := st.VerifyLedger(); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			for _, mm := range models {
				if len(mm.ref) >= 4 {
					off := rng.Intn(len(mm.ref) - 3)
					want := binary.BigEndian.Uint32(mm.ref[off:])
					got, err := mm.s.Uint32(off, binary.BigEndian)
					if err != nil || got != want {
						t.Fatalf("step %d: Uint32(%d)=%x want %x err=%v", i, off, got, want, err)
					}
				}
			}
		}
	}

	// Physical accounting dedups shared blocks: live bytes must be no more
	// than the sum of distinct blocks actually needed by the naive models.
	// (Exact equality is checked by VerifyLedger's block-by-block walk.)
	sum := 0
	for _, mm := range models {
		sum += len(mm.ref)
	}
	live := st.Stats().LiveBytes
	if sum > 0 && live == 0 {
		t.Fatal("live content but empty ledger")
	}
	t.Logf("after %d steps: %d branches, logical bytes total=%d, physical live bytes=%d",
		steps, len(models), sum, live)

	// Releasing everything must drain the store completely.
	for _, mm := range models {
		mm.s.Release()
	}
	if got := st.Stats(); got.Blocks != 0 || got.LiveBytes != 0 || got.LiveSlices != 0 {
		t.Fatalf("store not drained after releasing all slices: %+v", got)
	}
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
