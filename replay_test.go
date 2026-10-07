package statestore

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// branch is a live fork under test: a store plus its reference model.
type branch struct {
	store  *Store
	model  map[string]int64
	seq    uint64
	deltas []Delta // every delta applied since genesis, in order
	// historyFrom is the lowest seq the store still retains verifiable
	// history for; deltas older than that are subsumed by a snapshot
	// and cannot be verified, so replaying them is rejected.
	historyFrom uint64
}

var genesis = Snapshot{Seq: 0, State: map[string]int64{}, Digest: digestOf(0, map[string]int64{})}

func newBranch() *branch {
	return &branch{store: NewStore(), model: map[string]int64{}}
}

func modelApply(model map[string]int64, ops []Op) {
	for _, op := range ops {
		applyOp(model, op)
	}
}

// check verifies the store against its reference model.
func (b *branch) check(t *testing.T) {
	t.Helper()
	if got := b.store.Seq(); got != b.seq {
		t.Fatalf("seq=%d, want %d", got, b.seq)
	}
	if got := b.store.Digest(); got != digestOf(b.seq, b.model) {
		t.Fatalf("head digest mismatch at seq=%d", b.seq)
	}
	snap := b.store.Snapshot()
	if len(snap.State) != len(b.model) {
		t.Fatalf("state size=%d, want %d", len(snap.State), len(b.model))
	}
	for k, v := range b.model {
		if got, ok := snap.State[k]; !ok || got != v {
			t.Fatalf("state[%q]=%d,%v, want %d,true", k, got, ok, v)
		}
	}
}

func randomOps(r *rand.Rand) []Op {
	n := 1 + r.Intn(4)
	ops := make([]Op, 0, n)
	for i := 0; i < n; i++ {
		key := "k" + strconv.Itoa(r.Intn(10))
		switch r.Intn(3) {
		case 0:
			ops = append(ops, Op{Kind: OpSet, Key: key, Value: int64(r.Intn(1000))})
		case 1:
			ops = append(ops, Op{Kind: OpAdd, Key: key, Value: int64(r.Intn(100))})
		default:
			ops = append(ops, Op{Kind: OpDel, Key: key})
		}
	}
	return ops
}

func tamperDigest(r *rand.Rand, d Digest) Digest {
	d[r.Intn(len(d))] ^= byte(1 << uint(r.Intn(8)))
	return d
}

// TestRandomReplay drives a randomized state machine over multiple
// forks, checking chain integrity, idempotent replay, corrupt-digest
// rejection, cross-baseline isolation and recovery atomicity against a
// reference model.
func TestRandomReplay(t *testing.T) {
	seed := int64(20261007)
	if v := os.Getenv("STATESTORE_SEED"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("bad STATESTORE_SEED: %v", err)
		}
		seed = parsed
	}
	r := rand.New(rand.NewSource(seed))
	t.Logf("seed=%d", seed)

	branches := []*branch{newBranch()}

	for step := 0; step < 4000; step++ {
		b := branches[r.Intn(len(branches))]
		switch r.Intn(8) {
		case 0, 1: // extend a branch with a fresh delta
			ops := randomOps(r)
			d := b.store.NewDelta(ops)
			applied, err := b.store.Apply(d)
			if err != nil || !applied {
				t.Fatalf("step %d: apply: applied=%v err=%v", step, applied, err)
			}
			modelApply(b.model, ops)
			b.seq++
			b.deltas = append(b.deltas, d)
			b.check(t)

		case 2: // replay an already applied delta
			if len(b.deltas) == 0 {
				continue
			}
			d := b.deltas[r.Intn(len(b.deltas))]
			applied, err := b.store.Apply(d)
			if d.Seq >= b.historyFrom {
				// Verifiable replay: idempotent no-op.
				if err != nil || applied {
					t.Fatalf("step %d: replay seq=%d: applied=%v err=%v", step, d.Seq, applied, err)
				}
			} else {
				// Subsumed by a snapshot: cannot be verified, rejected.
				if err != ErrChainBroken {
					t.Fatalf("step %d: replay subsumed seq=%d: err=%v", step, d.Seq, err)
				}
			}
			b.check(t)

		case 3: // corrupt post-digest must be rejected without mutation
			ops := randomOps(r)
			d := b.store.NewDelta(ops)
			d.PostDigest = tamperDigest(r, d.PostDigest)
			if _, err := b.store.Apply(d); err != ErrDigestMismatch {
				t.Fatalf("step %d: corrupt post-digest: err=%v", step, err)
			}
			b.check(t)

		case 4: // broken chain: wrong pre-digest or future sequence
			ops := randomOps(r)
			d := b.store.NewDelta(ops)
			if r.Intn(2) == 0 {
				d.PreDigest = tamperDigest(r, d.PreDigest)
			} else {
				d.Seq += 1 + uint64(r.Intn(3))
			}
			if _, err := b.store.Apply(d); err != ErrChainBroken {
				t.Fatalf("step %d: broken chain: err=%v", step, err)
			}
			b.check(t)

		case 5: // a branch delta must not attach to another baseline
			if len(branches) < 2 {
				continue
			}
			other := branches[r.Intn(len(branches))]
			if other == b {
				continue
			}
			d := b.store.NewDelta(randomOps(r))
			if other.store.Digest() == d.PreDigest {
				continue // identical heads: attach would be legitimate
			}
			if _, err := other.store.Apply(d); err != ErrChainBroken {
				t.Fatalf("step %d: cross-baseline attach: err=%v", step, err)
			}
			other.check(t)

		case 6: // fork a new branch from an existing head
			if len(branches) >= 8 {
				continue
			}
			fork := &branch{
				store:  NewStore(),
				model:  make(map[string]int64, len(b.model)),
				seq:    b.seq,
				deltas: append([]Delta(nil), b.deltas...),
			}
			for k, v := range b.model {
				fork.model[k] = v
			}
			if r.Intn(2) == 0 {
				// Snapshot fork: history before the fork point is
				// subsumed by the snapshot.
				if err := fork.store.Restore(b.store.Snapshot(), nil); err != nil {
					t.Fatalf("step %d: snapshot fork: %v", step, err)
				}
				fork.historyFrom = b.seq
			} else {
				// Full-chain fork: snapshot + every delta since genesis.
				if err := fork.store.Restore(genesis, b.deltas); err != nil {
					t.Fatalf("step %d: chain fork: %v", step, err)
				}
			}
			fork.check(t)
			branches = append(branches, fork)

		case 7: // recovery from snapshot + delta chain
			// Pick a snapshot point the branch can still prove: either
			// genesis with the full chain, or the current head alone.
			fresh := NewStore()
			if r.Intn(2) == 0 {
				if err := fresh.Restore(genesis, b.deltas); err != nil {
					t.Fatalf("step %d: recover from genesis: %v", step, err)
				}
			} else {
				if err := fresh.Restore(b.store.Snapshot(), nil); err != nil {
					t.Fatalf("step %d: recover from head snapshot: %v", step, err)
				}
			}
			recovered := &branch{store: fresh, model: b.model, seq: b.seq}
			recovered.check(t)

			// Corrupt one delta in the chain: recovery must fail and
			// the target store must be left untouched (atomicity).
			if len(b.deltas) > 0 {
				bad := append([]Delta(nil), b.deltas...)
				i := r.Intn(len(bad))
				switch r.Intn(3) {
				case 0:
					bad[i].PostDigest = tamperDigest(r, bad[i].PostDigest)
				case 1:
					bad[i].PreDigest = tamperDigest(r, bad[i].PreDigest)
				default:
					bad[i].Ops = append(bad[i].Ops, Op{Kind: OpAdd, Key: "evil", Value: 1})
				}
				victim := branches[r.Intn(len(branches))]
				before := victim.store.Snapshot()
				if err := victim.store.Restore(genesis, bad); err == nil {
					t.Fatalf("step %d: corrupt chain recovered without error", step)
				}
				after := victim.store.Snapshot()
				if after.Seq != before.Seq || after.Digest != before.Digest {
					t.Fatalf("step %d: failed Restore was not atomic", step)
				}
				victim.check(t)
			}
		}
	}

	for i, b := range branches {
		b.check(t)
		t.Logf("branch %d: final seq=%d keys=%d", i, b.seq, len(b.model))
	}
}
