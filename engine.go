package netsec

import (
	"errors"
	"sort"
	"sync"
)

// Address is a host address. The package treats addresses as opaque labels;
// the network host is responsible for any address normalization.
type Address string

// GroupID identifies a security group.
type GroupID string

// RuleID identifies an allow rule.
type RuleID string

// Rule allows traffic from every member of SourceGroup to DstPort inside
// TargetGroup. The reference to SourceGroup is one hop: group membership never
// expands transitively through other rules, so cyclic group references are
// legal and cannot cause recursion.
type Rule struct {
	ID          RuleID
	TargetGroup GroupID
	SourceGroup GroupID
	DstPort     uint16
}

// Evidence is one minimal ground proof that a flow is allowed. It consists
// exactly of the matching rule, the source group membership, and the matched
// destination port. No Evidence contains another Evidence.
type Evidence struct {
	Rule          Rule
	SourceAddress Address
}

// AllowKey identifies a derived allow relation: a source address may reach a
// destination port in a target group.
type AllowKey struct {
	Source      Address
	TargetGroup GroupID
	DstPort     uint16
}

// Sentinel errors returned by Apply and Txn builders.
var (
	ErrEmptyTxn       = errors.New("netsec: transaction has no operations")
	ErrInvalidAddress = errors.New("netsec: address must not be empty")
	ErrInvalidGroup   = errors.New("netsec: group id must not be empty")
	ErrInvalidRule    = errors.New("netsec: rule requires non-empty id, source group and target group")
	ErrInvalidPort    = errors.New("netsec: rule destination port must not be zero")
	ErrMemberExists   = errors.New("netsec: group member already present")
	ErrMemberNotFound = errors.New("netsec: group member not found")
	ErrRuleExists     = errors.New("netsec: rule id already exists")
	ErrRuleNotFound   = errors.New("netsec: rule id not found")
	ErrConflictingOps = errors.New("netsec: transaction contains add and remove for the same object")
)

const (
	opAdd    int8 = 1
	opRemove int8 = -1
)

type memberRef struct {
	group GroupID
	addr  Address
}

type ruleOp struct {
	rule   Rule
	remove bool
}

// Txn is a staged set of membership and rule changes that Engine.Apply
// publishes atomically. A Txn must not be reused after Apply.
type Txn struct {
	members map[memberRef]int8
	rules   map[RuleID]ruleOp
	// removedRules carries ids staged for removal so a later PutRule on the
	// same id can implement replace-in-one-transaction semantics.
	removedRules map[RuleID]struct{}
	err          error
}

// NewTxn returns an empty transaction.
func NewTxn() *Txn {
	return &Txn{
		members:      make(map[memberRef]int8),
		rules:        make(map[RuleID]ruleOp),
		removedRules: make(map[RuleID]struct{}),
	}
}

// AddMember stages adding an address to a group.
func (t *Txn) AddMember(group GroupID, addr Address) *Txn {
	t.stageMember(memberRef{group: group, addr: addr}, opAdd)
	return t
}

// RemoveMember stages removing an address from a group.
func (t *Txn) RemoveMember(group GroupID, addr Address) *Txn {
	t.stageMember(memberRef{group: group, addr: addr}, opRemove)
	return t
}

// PutRule stages inserting a rule. A rule id already committed can be
// replaced in the same transaction by staging RemoveRule for that id first.
// Staging two puts for the same id without an intervening remove is rejected
// with ErrConflictingOps.
func (t *Txn) PutRule(rule Rule) *Txn {
	if t.err == nil {
		if _, removed := t.removedRules[rule.ID]; !removed {
			if prev, ok := t.rules[rule.ID]; ok && !prev.remove {
				t.err = ErrConflictingOps
			}
		}
	}
	t.rules[rule.ID] = ruleOp{rule: rule}
	return t
}

// RemoveRule stages deleting a rule.
func (t *Txn) RemoveRule(id RuleID) *Txn {
	t.removedRules[id] = struct{}{}
	t.rules[id] = ruleOp{remove: true}
	return t
}

func (t *Txn) stageMember(ref memberRef, op int8) {
	if t.err == nil {
		if prev, ok := t.members[ref]; ok && prev != op {
			t.err = ErrConflictingOps
		}
	}
	t.members[ref] = op
}

func (t *Txn) empty() bool {
	return len(t.members) == 0 && len(t.rules) == 0
}

// Engine is the incremental security-group decision engine. It is safe for
// concurrent use: reads take a read lock and Apply takes a write lock, so a
// transaction is published atomically with respect to all queries.
type Engine struct {
	mu sync.RWMutex

	// Ground state.
	known   map[GroupID]struct{}
	members map[GroupID]map[Address]struct{}
	rules   map[RuleID]Rule

	// Derived state. srcRules indexes rules by their referenced source group.
	srcRules map[GroupID]map[RuleID]Rule
	ruleEv   map[RuleID]map[AllowKey]Evidence
	allows   map[AllowKey]map[RuleID]Evidence

	version uint64
}

// NewEngine returns an empty engine.
func NewEngine() *Engine {
	return &Engine{
		known:    make(map[GroupID]struct{}),
		members:  make(map[GroupID]map[Address]struct{}),
		rules:    make(map[RuleID]Rule),
		srcRules: make(map[GroupID]map[RuleID]Rule),
		ruleEv:   make(map[RuleID]map[AllowKey]Evidence),
		allows:   make(map[AllowKey]map[RuleID]Evidence),
	}
}

// Version returns a counter that increments once per successfully published
// transaction. Failed Apply calls leave it unchanged.
func (e *Engine) Version() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.version
}

// Apply validates the transaction against the committed state and publishes
// every staged change atomically. On validation failure no state changes.
func (e *Engine) Apply(t *Txn) error {
	if t == nil || t.empty() {
		return ErrEmptyTxn
	}
	if t.err != nil {
		return t.err
	}
	if err := e.validate(t); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	affected := e.affectedRules(t)
	before := make(map[RuleID]map[AllowKey]Evidence, len(affected))
	for id := range affected {
		if ev := e.ruleEv[id]; len(ev) > 0 {
			before[id] = cloneEvidence(ev)
		}
	}

	e.applyMembers(t)
	e.applyRules(t)

	for id := range affected {
		rule, ok := e.rules[id]
		var after map[AllowKey]Evidence
		if ok {
			after = e.ruleEvidence(rule)
		}
		e.reconcile(id, before[id], after)
	}

	e.version++
	return nil
}

// validate checks every staged operation against both the transaction itself
// and the committed state. It performs no mutation.
func (e *Engine) validate(t *Txn) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for ref, op := range t.members {
		if ref.group == "" {
			return ErrInvalidGroup
		}
		if ref.addr == "" {
			return ErrInvalidAddress
		}
		_, exists := e.members[ref.group][ref.addr]
		if op == opAdd && exists {
			return ErrMemberExists
		}
		if op == opRemove && !exists {
			return ErrMemberNotFound
		}
	}

	for id, op := range t.rules {
		if op.remove {
			if _, exists := e.rules[id]; !exists {
				return ErrRuleNotFound
			}
			continue
		}
		rule := op.rule
		if id == "" || rule.TargetGroup == "" || rule.SourceGroup == "" {
			return ErrInvalidRule
		}
		if rule.ID != id {
			return ErrInvalidRule
		}
		if rule.DstPort == 0 {
			return ErrInvalidPort
		}
		_, exists := e.rules[id]
		_, removedInTxn := t.removedRules[id]
		if exists && !removedInTxn {
			return ErrRuleExists
		}
	}
	return nil
}

// affectedRules returns the only rules whose derived relations may change:
// rules explicitly added or removed by the transaction, plus rules that
// reference any group whose membership changes. No full rule scan is needed.
func (e *Engine) affectedRules(t *Txn) map[RuleID]struct{} {
	affected := make(map[RuleID]struct{}, len(t.rules))
	for id := range t.rules {
		affected[id] = struct{}{}
	}
	for ref := range t.members {
		for id := range e.srcRules[ref.group] {
			affected[id] = struct{}{}
		}
	}
	return affected
}

func (e *Engine) applyMembers(t *Txn) {
	for ref, op := range t.members {
		e.known[ref.group] = struct{}{}
		set := e.members[ref.group]
		if set == nil {
			set = make(map[Address]struct{})
			e.members[ref.group] = set
		}
		if op == opAdd {
			set[ref.addr] = struct{}{}
		} else {
			delete(set, ref.addr)
			if len(set) == 0 {
				delete(e.members, ref.group)
			}
		}
	}
}

func (e *Engine) applyRules(t *Txn) {
	for id, op := range t.rules {
		if op.remove {
			e.detachRule(e.rules[id])
			delete(e.rules, id)
			continue
		}
		if _, replaced := t.removedRules[id]; replaced {
			if old, ok := e.rules[id]; ok {
				e.detachRule(old)
				delete(e.rules, id)
			}
		}
		rule := op.rule
		e.known[rule.TargetGroup] = struct{}{}
		e.known[rule.SourceGroup] = struct{}{}
		e.rules[id] = rule
		set := e.srcRules[rule.SourceGroup]
		if set == nil {
			set = make(map[RuleID]Rule)
			e.srcRules[rule.SourceGroup] = set
		}
		set[id] = rule
	}
}

func (e *Engine) detachRule(rule Rule) {
	set := e.srcRules[rule.SourceGroup]
	delete(set, rule.ID)
	if len(set) == 0 {
		delete(e.srcRules, rule.SourceGroup)
	}
}

// ruleEvidence computes the complete minimal evidence set of one rule from
// the current ground state. Membership expansion is one hop, so cyclic
// references terminate immediately.
func (e *Engine) ruleEvidence(rule Rule) map[AllowKey]Evidence {
	members := e.members[rule.SourceGroup]
	if len(members) == 0 {
		return nil
	}
	out := make(map[AllowKey]Evidence, len(members))
	for addr := range members {
		key := AllowKey{Source: addr, TargetGroup: rule.TargetGroup, DstPort: rule.DstPort}
		out[key] = Evidence{Rule: rule, SourceAddress: addr}
	}
	return out
}

// reconcile diffs one rule's old and new evidence into the shared allow
// index. The union across rules is preserved per AllowKey.
func (e *Engine) reconcile(id RuleID, before, after map[AllowKey]Evidence) {
	for key := range before {
		if _, keep := after[key]; keep {
			continue
		}
		set := e.allows[key]
		delete(set, id)
		if len(set) == 0 {
			delete(e.allows, key)
		}
	}
	for key, ev := range after {
		if _, had := before[key]; had {
			continue
		}
		set := e.allows[key]
		if set == nil {
			set = make(map[RuleID]Evidence)
			e.allows[key] = set
		}
		set[id] = ev
	}
	if len(after) == 0 {
		delete(e.ruleEv, id)
	} else {
		e.ruleEv[id] = after
	}
}

func cloneEvidence(in map[AllowKey]Evidence) map[AllowKey]Evidence {
	out := make(map[AllowKey]Evidence, len(in))
	for key, ev := range in {
		out[key] = ev
	}
	return out
}

// Allows reports whether the source address currently has at least one minimal
// evidence for reaching the port in the target group.
func (e *Engine) Allows(src Address, target GroupID, port uint16) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.allows[AllowKey{Source: src, TargetGroup: target, DstPort: port}]) > 0
}

// Explain returns every minimal Evidence that allows the queried flow, sorted
// by rule id for deterministic output. Rules are combined by union: each
// matching rule contributes its own independent evidence.
func (e *Engine) Explain(src Address, target GroupID, port uint16) []Evidence {
	e.mu.RLock()
	defer e.mu.RUnlock()

	set := e.allows[AllowKey{Source: src, TargetGroup: target, DstPort: port}]
	if len(set) == 0 {
		return nil
	}
	out := make([]Evidence, 0, len(set))
	for _, ev := range set {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rule.ID != out[j].Rule.ID {
			return out[i].Rule.ID < out[j].Rule.ID
		}
		if out[i].SourceAddress != out[j].SourceAddress {
			return out[i].SourceAddress < out[j].SourceAddress
		}
		return out[i].Rule.SourceGroup < out[j].Rule.SourceGroup
	})
	return out
}
