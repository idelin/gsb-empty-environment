package recovery

// instanceA is a hand-computed three-transaction / two-key instance whose
// optimal worst-case fee is known independently of the planner.
func instanceA() *Problem {
	txns := []Txn{
		{ID: 0, Group: 0, Reads: []Read{{Low: "k0", High: "k1", Want: ""}}, WriteKey: "k0", WriteVal: "a", HasWrite: true},
		{ID: 1, Group: 0, Reads: []Read{{Low: "k0", High: "k1", Want: "a"}}, WriteKey: "k1", WriteVal: "b", HasWrite: true},
		{ID: 2, Group: 0, Reads: []Read{{Low: "k0", High: "k1", Want: "a"}},
			WriteKey: "k0", WriteVal: "c", HasWrite: true},
	}
	sites := []Site{
		{Name: "empty", Committed: 0, KV: map[Key]Value{}},
		{Name: "one", Committed: 1, KV: map[Key]Value{"k0": "a"}},
		{Name: "two", Committed: 3, KV: map[Key]Value{"k0": "a", "k1": "b"}},
	}
	return &Problem{
		Txns:            txns,
		Sites:           sites,
		TargetKV:        map[Key]Value{"k0": "c", "k1": "b"},
		TargetCommitted: BitsetAll(3),
		Commands: []Command{
			{Name: "c1", Group: 0, Prefix: 1, Cost: 5},
			{Name: "c2", Group: 0, Prefix: 2, Cost: 9},
			{Name: "c3", Group: 0, Prefix: 3, Cost: 14},
		},
		Probes: []Probe{{Name: "p0", Group: 0, Cost: 4}},
	}
}

// instanceB is genuinely unsolvable: one starting site already committed the
// target transaction with a value the fixed command library cannot repair.
func instanceB() *Problem {
	txns := []Txn{
		{ID: 0, Group: 0, Reads: []Read{{Low: "k0", High: "k1", Want: ""}}, WriteKey: "k0", WriteVal: "z", HasWrite: true},
	}
	sites := []Site{
		{Name: "empty", Committed: 0, KV: map[Key]Value{}},
		{Name: "wrong", Committed: 1, KV: map[Key]Value{"k0": "z"}},
	}
	return &Problem{
		Txns:            txns,
		Sites:           sites,
		TargetKV:        map[Key]Value{"k0": "a"},
		TargetCommitted: 1,
		Commands:        []Command{{Name: "c1", Group: 0, Prefix: 1, Cost: 3}},
		Probes:          []Probe{{Name: "p0", Group: 0, Cost: 2}},
	}
}

// instanceC keeps every site with the same bitmap (and hence identical
// summaries) while the persisted values diverge.
func instanceC() *Problem {
	txns := []Txn{
		{ID: 0, Group: 0, Reads: nil, WriteKey: "k0", WriteVal: "a", HasWrite: true},
		{ID: 1, Group: 1, Reads: nil, WriteKey: "k0", WriteVal: "b", HasWrite: true},
	}
	sites := []Site{
		{Name: "a-last", Committed: BitsetAll(2), KV: map[Key]Value{"k0": "a"}},
		{Name: "b-last", Committed: BitsetAll(2), KV: map[Key]Value{"k0": "b"}},
	}
	return &Problem{
		Txns:            txns,
		Sites:           sites,
		TargetKV:        map[Key]Value{"k0": "z"},
		TargetCommitted: BitsetAll(2),
		Commands: []Command{
			{Name: "g0all", Group: 0, Prefix: 1, Cost: 7},
			{Name: "g1all", Group: 1, Prefix: 1, Cost: 7},
		},
		Probes: []Probe{
			{Name: "p0", Group: 0, Cost: 1},
			{Name: "p1", Group: 1, Cost: 1},
		},
	}
}

// instanceD creates the "same committed set, different final writer" case:
// two groups can write the same key in different serial orders.
func instanceD() *Problem {
	txns := []Txn{
		{ID: 0, Group: 0, Reads: nil, WriteKey: "k0", WriteVal: "a", HasWrite: true},
		{ID: 1, Group: 1, Reads: nil, WriteKey: "k0", WriteVal: "b", HasWrite: true},
	}
	sites := []Site{
		{Name: "a-last", Committed: Bitset(1), KV: map[Key]Value{"k0": "a"}},
		{Name: "b-last", Committed: Bitset(2), KV: map[Key]Value{"k0": "b"}},
	}
	return &Problem{
		Txns:            txns,
		Sites:           sites,
		TargetKV:        map[Key]Value{"k0": "z"},
		TargetCommitted: BitsetAll(2),
		Commands: []Command{
			{Name: "g0", Group: 0, Prefix: 1, Cost: 4},
			{Name: "g1", Group: 1, Prefix: 1, Cost: 4},
		},
	}
}
