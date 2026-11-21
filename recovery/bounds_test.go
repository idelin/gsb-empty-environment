package recovery

import "testing"

// TestMaxBounds exercises the advertised input limits: ten transactions, six
// keys and twelve starting sites in one solvable instance.
func TestMaxBounds(t *testing.T) {
	const nTxn, nKeys, nSites = 10, 6, 12
	txns := make([]Txn, nTxn)
	for i := 0; i < nTxn; i++ {
		txns[i] = Txn{
			ID:       TxnID(i),
			Group:    Group(i % 3),
			HasWrite: true,
			WriteKey: Key("k" + string(rune('0'+i%nKeys))),
			WriteVal: Value("v"),
		}
	}
	sites := make([]Site, nSites)
	for i := range sites {
		// Prefix-closed bitmaps within each of the three groups.
		var bits Bitset
		for g := 0; g < 3; g++ {
			limit := (i + g) % 4
			for j := 0; j < nTxn; j++ {
				if int(txns[j].Group) == g {
					inGroup := j / 3
					if inGroup < limit {
						bits.add(TxnID(j))
					}
				}
			}
		}
		kv := map[Key]Value{}
		for j := 0; j < nTxn; j++ {
			if bits.Contains(TxnID(j)) {
				kv[txns[j].WriteKey] = txns[j].WriteVal
			}
		}
		sites[i] = Site{Name: "site" + string(rune('a'+i)), Committed: bits, KV: kv}
	}
	p := &Problem{
		Txns:            txns,
		Sites:           sites,
		TargetKV:        map[Key]Value{},
		TargetCommitted: BitsetAll(nTxn),
	}
	// Every transaction writes the same value, so replaying all remaining
	// transactions converges every site to one exact target key/value state.
	for k := 0; k < nKeys; k++ {
		p.TargetKV[Key("k"+string(rune('0'+k)))] = "v"
	}
	for g := Group(0); g < 3; g++ {
		size := 0
		for _, tx := range txns {
			if tx.Group == g {
				size++
			}
		}
		for prefix := 1; prefix <= size; prefix++ {
			p.Commands = append(p.Commands, Command{
				Name:  "c-" + string(rune('0'+g)) + "-" + string(rune('0'+prefix)),
				Group: g, Prefix: prefix, Cost: 1,
			})
		}
		p.Probes = append(p.Probes, Probe{
			Name: "p-" + string(rune('0'+g)), Group: g, Cost: 1,
		})
	}

	if err := p.Validate(); err != nil {
		t.Fatalf("validate max bounds: %v", err)
	}
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve max bounds: %v", err)
	}
	if !sol.Solvable() {
		t.Fatal("full-commit target must be reachable via the command library")
	}
	if sol.NodeCount() == 0 {
		t.Fatal("expected explored belief nodes")
	}
}
