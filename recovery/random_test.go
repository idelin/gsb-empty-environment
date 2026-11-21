package recovery

import (
	"fmt"
	"math/rand"
)

const letters = "abcd"

// randomProblem builds small valid instances inside the task bounds. Sites are
// produced by actually executing transactions, so realizability is guaranteed.
func randomProblem(rng *rand.Rand) *Problem {
	nTxn := 2 + rng.Intn(3) // 2..4 transactions
	nGroups := 1 + rng.Intn(2)
	nKeys := 2 + rng.Intn(2) // 2..3 keys
	keys := make([]Key, nKeys)
	for i := range keys {
		keys[i] = Key("k" + string(rune('0'+i)))
	}

	txns := make([]Txn, nTxn)
	groupOf := make([]Group, nTxn)
	for i := 0; i < nTxn; i++ {
		g := Group(rng.Intn(nGroups))
		groupOf[i] = g
		var reads []Read
		if rng.Intn(2) == 0 && nKeys >= 2 {
			lo := rng.Intn(nKeys - 1)
			hi := lo + 1
			hi = lo + 1 + rng.Intn(nKeys-lo-1)
			reads = append(reads, Read{Low: keys[lo], High: keys[hi], Want: Value(string(letters[rng.Intn(3)]))})
		}
		t := Txn{ID: TxnID(i), Group: g, Reads: reads}
		if rng.Intn(2) == 0 {
			t.HasWrite = true
			t.WriteKey = keys[rng.Intn(nKeys)]
			t.WriteVal = Value(string(letters[rng.Intn(3)]))
		}
		txns[i] = t
	}

	// Reorder group assignment so every group is non-empty and prefixes are
	// meaningful: force the first nGroups txns into distinct groups.
	for g := 0; g < nGroups && g < nTxn; g++ {
		txns[g].Group = Group(g)
		groupOf[g] = Group(g)
	}

	p := &Problem{Txns: txns}
	siteCount := 1 + rng.Intn(3)
	sites := make([]Site, 0, siteCount)
	used := map[string]bool{}
	attempts := 0
	for len(sites) < siteCount {
		attempts++
		bits, kv := randomReachableState(p, rng)
		sig := worldSignature(bits, kv)
		if used[sig] && attempts < 50 {
			continue
		}
		used[sig] = true
		sites = append(sites, Site{Name: fmt.Sprintf("s%d", len(sites)), Committed: bits, KV: kv})
	}
	p.Sites = sites

	// Target is another reachable state that dominates a random site when
	// possible; keep it finite and prefix-closed.
	targetBits, targetKV := randomReachableState(p, rng)
	p.TargetCommitted = targetBits
	p.TargetKV = targetKV

	cmdNames := map[string]bool{}
	for g := 0; g < nGroups; g++ {
		size := 0
		for _, t := range txns {
			if t.Group == Group(g) {
				size++
			}
		}
		for prefix := 1; prefix <= size; prefix++ {
			name := fmt.Sprintf("c%d_%d", g, prefix)
			if cmdNames[name] {
				continue
			}
			cmdNames[name] = true
			p.Commands = append(p.Commands, Command{
				Name: name, Group: Group(g), Prefix: prefix, Cost: 1 + rng.Intn(9),
			})
		}
	}
	for g := 0; g < nGroups; g++ {
		p.Probes = append(p.Probes, Probe{Name: fmt.Sprintf("p%d", g), Group: Group(g), Cost: 1 + rng.Intn(4)})
	}
	return p
}

// randomReachableState simulates transactions in a random group-respecting
// order, stopping early, so the returned (bitmap, kv) is always realizable.
func randomReachableState(p *Problem, rng *rand.Rand) (Bitset, map[Key]Value) {
	kv := map[Key]Value{}
	var bits Bitset
	remaining := append([]Txn(nil), p.Txns...)
	for len(remaining) > 0 {
		// Only choose transactions whose group predecessor is committed, which
		// keeps the bitmap prefix-closed.
		candidates := make([]int, 0)
		for i, t := range remaining {
			if p.groupPredecessorCommitted(t, bits) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			break
		}
		choice := candidates[rng.Intn(len(candidates))]
		t := remaining[choice]
		if p.txnReadOK(kv, t) {
			kv = applyTxn(kv, t)
			bits.add(t.ID)
		}
		remaining = append(remaining[:choice], remaining[choice+1:]...)
		if rng.Intn(3) == 0 {
			break
		}
	}
	return bits, copyKV(kv)
}

func (p *Problem) groupPredecessorCommitted(t Txn, bits Bitset) bool {
	order := p.groupTxns(t.Group)
	for i, x := range order {
		if x.ID == t.ID {
			return i == 0 || bits.Contains(order[i-1].ID)
		}
	}
	return false
}
