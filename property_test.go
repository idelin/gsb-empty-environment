package netsec

import (
	"fmt"
	"math/rand"
	"testing"
)

// oracle keeps an independent copy of the ground facts and recomputes every
// allow relation from scratch after each successful transaction.
type oracle struct {
	members map[GroupID]map[Address]struct{}
	rules   map[RuleID]Rule
}

func newOracle() *oracle {
	return &oracle{
		members: make(map[GroupID]map[Address]struct{}),
		rules:   make(map[RuleID]Rule),
	}
}

func (o *oracle) hasMember(group GroupID, addr Address) bool {
	_, ok := o.members[group][addr]
	return ok
}

func (o *oracle) addMember(group GroupID, addr Address) {
	set := o.members[group]
	if set == nil {
		set = make(map[Address]struct{})
		o.members[group] = set
	}
	set[addr] = struct{}{}
}

func (o *oracle) removeMember(group GroupID, addr Address) {
	delete(o.members[group], addr)
	if len(o.members[group]) == 0 {
		delete(o.members, group)
	}
}

func (o *oracle) explain(src Address, target GroupID, port uint16) map[RuleID]Evidence {
	out := make(map[RuleID]Evidence)
	for id, rule := range o.rules {
		if rule.TargetGroup != target || rule.DstPort != port {
			continue
		}
		if o.hasMember(rule.SourceGroup, src) {
			out[id] = Evidence{Rule: rule, SourceAddress: src}
		}
	}
	return out
}

type modelUniverse struct {
	groups   []GroupID
	addrs    []Address
	ports    []uint16
	rng      *rand.Rand
	ruleSeq  int
	ruleByID map[RuleID]Rule
}

func newUniverse(seed int64) *modelUniverse {
	rng := rand.New(rand.NewSource(seed))
	groups := []GroupID{"g1", "g2", "g3", "g4", "g5"}
	addrs := make([]Address, 8)
	for i := range addrs {
		addrs[i] = Address(fmt.Sprintf("10.0.%d.%d", i/4, 1+i%4))
	}
	return &modelUniverse{
		groups:   groups,
		addrs:    addrs,
		ports:    []uint16{80, 443, 8080, 53},
		rng:      rng,
		ruleByID: make(map[RuleID]Rule),
	}
}

func (u *modelUniverse) newRuleID() RuleID {
	u.ruleSeq++
	return RuleID(fmt.Sprintf("rule-%03d", u.ruleSeq))
}

// validTxn builds a transaction that is legal against both engine and oracle,
// exercising cyclic references, empty groups and shared addresses.
type pendingChange struct {
	addMembers    []memberRef
	removeMembers []memberRef
	putRules      []Rule
	removeRules   []RuleID
}

func (p *pendingChange) commit(o *oracle, u *modelUniverse) {
	for _, ref := range p.addMembers {
		o.addMember(ref.group, ref.addr)
	}
	for _, ref := range p.removeMembers {
		o.removeMember(ref.group, ref.addr)
	}
	for _, rule := range p.putRules {
		o.rules[rule.ID] = rule
		u.ruleByID[rule.ID] = rule
	}
	for _, id := range p.removeRules {
		delete(o.rules, id)
		delete(u.ruleByID, id)
	}
}

func (u *modelUniverse) validTxn(o *oracle) (*Txn, *pendingChange) {
	txn := NewTxn()
	pending := &pendingChange{}
	ops := 1 + u.rng.Intn(4)
	touched := make(map[memberRef]struct{})
	for i := 0; i < ops; i++ {
		switch u.rng.Intn(4) {
		case 0:
			group := u.groups[u.rng.Intn(len(u.groups))]
			addr := u.addrs[u.rng.Intn(len(u.addrs))]
			ref := memberRef{group: group, addr: addr}
			if _, dup := touched[ref]; !dup && !o.hasMember(group, addr) {
				txn.AddMember(group, addr)
				pending.addMembers = append(pending.addMembers, ref)
				touched[ref] = struct{}{}
			}
		case 1:
			group := u.groups[u.rng.Intn(len(u.groups))]
			addr := u.addrs[u.rng.Intn(len(u.addrs))]
			ref := memberRef{group: group, addr: addr}
			if _, dup := touched[ref]; !dup && o.hasMember(group, addr) {
				txn.RemoveMember(group, addr)
				pending.removeMembers = append(pending.removeMembers, ref)
				touched[ref] = struct{}{}
			}
		case 2:
			id := u.newRuleID()
			rule := Rule{
				ID:          id,
				TargetGroup: u.groups[u.rng.Intn(len(u.groups))],
				SourceGroup: u.groups[u.rng.Intn(len(u.groups))],
				DstPort:     u.ports[u.rng.Intn(len(u.ports))],
			}
			txn.PutRule(rule)
			pending.putRules = append(pending.putRules, rule)
		case 3:
			if len(u.ruleByID) > 0 {
				var id RuleID
				for candidate := range u.ruleByID {
					id = candidate
					break
				}
				txn.RemoveRule(id)
				pending.removeRules = append(pending.removeRules, id)
			}
		}
	}
	if txn.empty() {
		return u.validTxn(o)
	}
	return txn, pending
}

func TestRandomTransactionsMatchOracle(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		eng := NewEngine()
		ora := newOracle()
		u := newUniverse(seed)

		for iteration := 0; iteration < 300; iteration++ {
			preVersion := eng.Version()
			txn, pending := u.validTxn(ora)
			if err := eng.Apply(txn); err != nil {
				t.Fatalf("seed=%d iter=%d unexpected error: %v", seed, iteration, err)
			}
			pending.commit(ora, u)
			if eng.Version() != preVersion+1 {
				t.Fatalf("seed=%d iter=%d version did not increment once", seed, iteration)
			}

			for _, src := range u.addrs {
				for _, target := range u.groups {
					for _, port := range u.ports {
						want := ora.explain(src, target, port)
						got := eng.Explain(src, target, port)
						if len(want) != len(got) {
							t.Fatalf("seed=%d iter=%d key=(%s,%s,%d) want %d evidence got %d",
								seed, iteration, src, target, port, len(want), len(got))
						}
						gotIDs := make(map[RuleID]Evidence, len(got))
						for _, ev := range got {
							gotIDs[ev.Rule.ID] = ev
						}
						for id, wantEv := range want {
							gotEv, ok := gotIDs[id]
							if !ok || gotEv != wantEv {
								t.Fatalf("seed=%d iter=%d evidence mismatch for rule %s: want %+v got %+v",
									seed, iteration, id, wantEv, gotEv)
							}
						}
						if (len(want) > 0) != eng.Allows(src, target, port) {
							t.Fatalf("seed=%d iter=%d Allows/Explain disagreement", seed, iteration)
						}
					}
				}
			}

			report := eng.CheckRebuild()
			if !report.Consistent {
				t.Fatalf("seed=%d iter=%d rebuild mismatches: %v", seed, iteration, report.Mismatches)
			}
		}
	}
}

// FuzzReplay drives a fixed universe with a byte stream; every successful
// transaction is immediately cross-checked against a full rebuild.
func FuzzReplay(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
	f.Fuzz(func(t *testing.T, stream []byte) {
		eng := NewEngine()
		ora := newOracle()
		u := newUniverse(99)

		for _, b := range stream {
			txn := NewTxn()
			pending := &pendingChange{}
			group := u.groups[int(b>>5)%len(u.groups)]
			addr := u.addrs[int(b>>2)%len(u.addrs)]
			port := u.ports[int(b)%len(u.ports)]

			switch b % 5 {
			case 0:
				if !ora.hasMember(group, addr) {
					txn.AddMember(group, addr)
					pending.addMembers = append(pending.addMembers, memberRef{group: group, addr: addr})
				}
			case 1:
				if ora.hasMember(group, addr) {
					txn.RemoveMember(group, addr)
					pending.removeMembers = append(pending.removeMembers, memberRef{group: group, addr: addr})
				}
			case 2:
				id := u.newRuleID()
				rule := Rule{ID: id, TargetGroup: group, SourceGroup: u.groups[int(b/3)%len(u.groups)], DstPort: port}
				txn.PutRule(rule)
				pending.putRules = append(pending.putRules, rule)
			case 3:
				for id := range u.ruleByID {
					txn.RemoveRule(id)
					pending.removeRules = append(pending.removeRules, id)
					break
				}
			case 4:
				id := u.newRuleID()
				rule := Rule{ID: id, TargetGroup: group, SourceGroup: group, DstPort: port}
				txn.PutRule(rule)
				pending.putRules = append(pending.putRules, rule)
			}

			if txn.empty() {
				continue
			}
			if err := eng.Apply(txn); err != nil {
				t.Fatalf("unexpected apply error: %v", err)
			}
			pending.commit(ora, u)

			for _, src := range u.addrs {
				for _, target := range u.groups {
					for _, p := range u.ports {
						if len(ora.explain(src, target, p)) != len(eng.Explain(src, target, p)) {
							t.Fatal("oracle and engine diverged")
						}
					}
				}
			}
			if report := eng.CheckRebuild(); !report.Consistent {
				t.Fatalf("rebuild mismatches: %v", report.Mismatches)
			}
		}
	})
}
