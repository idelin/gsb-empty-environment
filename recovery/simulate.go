package recovery

import "sort"

func copyKV(kv map[Key]Value) map[Key]Value {
	out := make(map[Key]Value, len(kv))
	for k, v := range kv {
		out[k] = v
	}
	return out
}

// keyUniverse is every key appearing in the problem. The universe is finite
// (at most six keys) and absent keys read as the empty value.
func (p *Problem) keyUniverse() []Key {
	if p.cachedKeys != nil {
		return p.cachedKeys
	}
	set := map[Key]struct{}{}
	for _, t := range p.Txns {
		for _, r := range t.Reads {
			set[r.Low] = struct{}{}
			set[r.High] = struct{}{}
		}
		if t.HasWrite {
			set[t.WriteKey] = struct{}{}
		}
	}
	for k := range p.TargetKV {
		set[k] = struct{}{}
	}
	for _, s := range p.Sites {
		for k := range s.KV {
			set[k] = struct{}{}
		}
	}
	out := make([]Key, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	p.cachedKeys = out
	return out
}

// readOK evaluates the complete half-open interval predicate over every
// universe key in [Low, High). Keys never explicitly stored read as empty.
func (p *Problem) readOK(kv map[Key]Value, r Read) bool {
	for _, k := range p.keyUniverse() {
		if k < r.Low || k >= r.High {
			continue
		}
		if kv[k] != r.Want {
			return false
		}
	}
	return true
}

func (p *Problem) txnReadOK(kv map[Key]Value, t Txn) bool {
	for _, r := range t.Reads {
		if !p.readOK(kv, r) {
			return false
		}
	}
	return true
}

func applyTxn(kv map[Key]Value, t Txn) map[Key]Value {
	if !t.HasWrite {
		return kv
	}
	next := copyKV(kv)
	if t.WriteVal == "" {
		delete(next, t.WriteKey)
	} else {
		next[t.WriteKey] = t.WriteVal
	}
	return next
}

// realizable asks whether some serial ordering of the committed transactions
// produces the exact persisted key/value state of the site.
func (p *Problem) realizable(s Site, byID map[TxnID]*Txn, order map[Group][]TxnID) bool {
	committed := make([]Txn, 0, s.Committed.Count())
	for _, id := range p.orderedIDs() {
		if s.Committed.Contains(id) {
			committed = append(committed, *byID[id])
		}
	}
	initial := map[Key]Value{}
	type frame struct {
		remaining Bitset
		kv        map[Key]Value
	}
	visited := map[Bitset]map[string]bool{}
	stack := []frame{{remaining: s.Committed, kv: initial}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.remaining == 0 {
			if kvEqual(f.kv, s.KV) {
				return true
			}
			continue
		}
		kvSig := kvSignature(f.kv)
		if seen, ok := visited[f.remaining]; ok && seen[kvSig] {
			continue
		}
		if visited[f.remaining] == nil {
			visited[f.remaining] = map[string]bool{}
		}
		visited[f.remaining][kvSig] = true

		for _, t := range committed {
			if !f.remaining.Contains(t.ID) {
				continue
			}
			if !p.txnReadOK(f.kv, t) {
				continue
			}
			next := f.remaining
			next &^= Bitset(1) << t.ID
			stack = append(stack, frame{remaining: next, kv: applyTxn(f.kv, t)})
		}
	}
	return false
}

func (p *Problem) orderedIDs() []TxnID {
	ids := make([]TxnID, len(p.Txns))
	for i, t := range p.Txns {
		ids[i] = t.ID
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func kvEqual(a, b map[Key]Value) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func kvSignature(kv map[Key]Value) string {
	keys := make([]Key, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var sig []byte
	for _, k := range keys {
		sig = append(sig, []byte(k)...)
		sig = append(sig, 0)
		sig = append(sig, []byte(kv[k])...)
		sig = append(sig, 0)
	}
	return string(sig)
}

// groupTxns returns the transactions of a group in prefix (increasing ID)
// order.
func (p *Problem) groupTxns(g Group) []Txn {
	if p.cachedGroups != nil {
		return p.cachedGroups[g]
	}
	cache := map[Group][]Txn{}
	for _, t := range p.Txns {
		cache[t.Group] = append(cache[t.Group], t)
	}
	for g := range cache {
		sort.Slice(cache[g], func(i, j int) bool { return cache[g][i].ID < cache[g][j].ID })
	}
	p.cachedGroups = cache
	return cache[g]
}

// world is one concrete persisted state the real replica may be in.
type world struct {
	bits Bitset
	kv   map[Key]Value
}

func (w world) signature() string {
	return worldSignature(w.bits, w.kv)
}

func worldSignature(bits Bitset, kv map[Key]Value) string {
	return string(rune(bits)) + "|" + kvSignature(kv)
}

// branch is one possible observation of an attempt together with the world
// that remains after it.
type branch struct {
	receipt Receipt
	next    world
}

// commitBranches enumerates every receipt that the adversary can produce when
// running c against w, given whether the single allowed disconnect is still
// available. Branches with identical (receipt, resulting world) pairs merge.
func (p *Problem) commitBranches(c Command, w world, disconnectAvailable bool) []branch {
	txns := p.groupTxns(c.Group)
	if c.Prefix > len(txns) {
		c.Prefix = len(txns)
	}
	attempt := txns[:c.Prefix]

	bits := w.bits
	kv := copyKV(w.kv)
	out := []branch{}
	seen := map[string]bool{}
	add := func(b branch) {
		key := receiptKey(b.receipt) + "|" + b.next.signature()
		if !seen[key] {
			seen[key] = true
			out = append(out, b)
		}
	}

	// The adversary may disconnect before the first transaction.
	if disconnectAvailable {
		add(branch{receipt: DisconnectReceipt{}, next: world{bits: bits, kv: copyKV(kv)}})
	}

	for _, t := range attempt {
		if bits.Contains(t.ID) {
			// Committed transactions are deduplicated: no predicate, no write.
			if disconnectAvailable {
				add(branch{receipt: DisconnectReceipt{}, next: world{bits: bits, kv: copyKV(kv)}})
			}
			continue
		}
		if !p.txnReadOK(kv, t) {
			add(branch{receipt: RejectReceipt{RejectID: t.ID}, next: world{bits: bits, kv: copyKV(kv)}})
			return out
		}
		kv = applyTxn(kv, t)
		bits.add(t.ID)
		if disconnectAvailable {
			add(branch{receipt: DisconnectReceipt{}, next: world{bits: bits, kv: copyKV(kv)}})
		}
	}
	add(branch{receipt: OKReceipt{}, next: world{bits: bits, kv: kv}})
	return out
}

func receiptKey(r Receipt) string {
	switch v := r.(type) {
	case OKReceipt:
		return "ok"
	case DisconnectReceipt:
		return "disc"
	case RejectReceipt:
		return "rej:" + string(rune(v.RejectID))
	case ProbeReceipt:
		return "probe:" + string(rune(v.Group)) + ":" + string(rune(v.Answer))
	default:
		return "unknown"
	}
}
