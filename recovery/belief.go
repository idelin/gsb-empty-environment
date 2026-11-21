package recovery

import "sort"

// belief is the coordinator's uncertainty: every concrete world still
// consistent with all receipts observed so far, plus whether the single
// environment disconnect has already been consumed.
type belief struct {
	disconnected bool
	worlds       map[string]world
}

func newBelief(worlds []world, disconnected bool) belief {
	b := belief{disconnected: disconnected, worlds: map[string]world{}}
	for _, w := range worlds {
		b.worlds[w.signature()] = w
	}
	return b
}

func (b belief) key() string {
	sigs := make([]string, 0, len(b.worlds))
	for s := range b.worlds {
		sigs = append(sigs, s)
	}
	sort.Strings(sigs)
	key := "0|"
	if b.disconnected {
		key = "1|"
	}
	for _, s := range sigs {
		key += s + ";"
	}
	return key
}

func (p *Problem) initialBelief() belief {
	worlds := make([]world, 0, len(p.Sites))
	for _, s := range p.Sites {
		worlds = append(worlds, world{bits: s.Committed, kv: copyKV(s.KV)})
	}
	return newBelief(worlds, false)
}

// atGoal reports that every compatible world equals the target exactly, in
// both bitmap and key/value state.
func (p *Problem) atGoal(b belief) bool {
	for _, w := range b.worlds {
		if w.bits != p.TargetCommitted || !kvEqual(w.kv, p.TargetKV) {
			return false
		}
	}
	return true
}

// observation groups the branches of one action by the public receipt: worlds
// producing the same receipt form the updated belief for that outcome.
type observation struct {
	receipt Receipt
	b       belief
}

// applyCommit returns every distinct receipt the coordinator may observe for
// command c, each paired with the belief remaining afterward.
func (p *Problem) applyCommit(b belief, c Command) []observation {
	groups := map[string]*observation{}
	order := []Receipt{}
	for _, w := range b.worlds {
		for _, br := range p.commitBranches(c, w, !b.disconnected) {
			k := receiptKey(br.receipt)
			obs, ok := groups[k]
			if !ok {
				obs = &observation{receipt: br.receipt, b: belief{
					disconnected: b.disconnected || isDisconnect(br.receipt),
					worlds:       map[string]world{},
				}}
				groups[k] = obs
				order = append(order, br.receipt)
			}
			obs.b.worlds[br.next.signature()] = br.next
		}
	}
	out := make([]observation, 0, len(order))
	for _, r := range order {
		out = append(out, *groups[receiptKey(r)])
	}
	return out
}

// applyProbe returns the parity observations. Probes are reliable and never
// disconnect, so they only split the belief; each answer still costs.
func (p *Problem) applyProbe(b belief, pr Probe) []observation {
	groups := map[Parity]*observation{}
	var order []Parity
	for _, w := range b.worlds {
		answer := p.groupParity(w.bits, pr.Group)
		obs, ok := groups[answer]
		if !ok {
			obs = &observation{receipt: ProbeReceipt{Group: pr.Group, Answer: answer}, b: belief{
				disconnected: b.disconnected,
				worlds:       map[string]world{},
			}}
			groups[answer] = obs
			order = append(order, answer)
		}
		obs.b.worlds[w.signature()] = w
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]observation, 0, len(order))
	for _, a := range order {
		out = append(out, *groups[a])
	}
	return out
}

func (p *Problem) groupParity(bits Bitset, g Group) Parity {
	n := 0
	for _, t := range p.groupTxns(g) {
		if bits.Contains(t.ID) {
			n++
		}
	}
	return parityOf(n)
}

func isDisconnect(r Receipt) bool {
	_, ok := r.(DisconnectReceipt)
	return ok
}

// restrictTo filters a belief to the worlds matching a received receipt after
// redelivery; used by the coordinator so a repeated command after a lost
// receipt cannot re-execute already committed transactions.
func (b belief) clone() belief {
	next := belief{disconnected: b.disconnected, worlds: map[string]world{}}
	for k, w := range b.worlds {
		next.worlds[k] = world{bits: w.bits, kv: copyKV(w.kv)}
	}
	return next
}
