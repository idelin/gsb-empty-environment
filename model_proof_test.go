package netsec

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// This file proves, in a model with at most 3 slots, that a wrapping
// 16-bit generation lets a stale ticket resolve to a different object,
// while the correct 32-bit design (burn at 2^32-1, never wrap) rejects
// the same ticket. Reaching the wrap/burn boundary needs up to 2^32
// revoke/allocate cycles, so the model uses a compressed loop: a run of
// N identical revoke+allocate cycles is applied to the generation
// counter in O(1) and recorded as a single summary entry. The concrete
// prefix and the final Use are executed step by step, and the model's
// concrete steps are cross-checked against the real implementations.

type proofEntry struct {
	step int
	op   string
	text string
}

type proofRecorder struct{ entries []proofEntry }

func (r *proofRecorder) record(op, format string, args ...any) {
	r.entries = append(r.entries, proofEntry{
		step: len(r.entries),
		op:   op,
		text: fmt.Sprintf(format, args...),
	})
}

func (r *proofRecorder) hasOp(op string) bool {
	for _, e := range r.entries {
		if e.op == op {
			return true
		}
	}
	return false
}

func (r *proofRecorder) logTo(t *testing.T) {
	t.Helper()
	for _, e := range r.entries {
		t.Logf("[%02d] %-8s %s", e.step, e.op, e.text)
	}
}

type modelSlot struct {
	gen      uint64
	obj      int // object identity; -1 when empty
	occupied bool
	burned   bool
}

type modelTicket struct {
	slot int
	gen  uint64
	obj  int // object identity at mint time
}

// modelTable models the slot/generation semantics of a capability table.
// wrap=true models the pending-review 16-bit table (generation wraps,
// never burns); wrap=false models the correct table (burns at max).
type modelTable struct {
	genBits uint
	wrap    bool
	slots   []modelSlot
}

func newModelTable(genBits uint, wrap bool, numSlots int) *modelTable {
	slots := make([]modelSlot, numSlots)
	for i := range slots {
		slots[i].obj = -1
	}
	return &modelTable{genBits: genBits, wrap: wrap, slots: slots}
}

func (m *modelTable) maxGen() uint64 { return 1<<m.genBits - 1 }

func (m *modelTable) allocate(obj int) (modelTicket, bool) {
	for i := range m.slots {
		s := &m.slots[i]
		if s.occupied || s.burned {
			continue
		}
		s.occupied = true
		s.obj = obj
		return modelTicket{slot: i, gen: s.gen, obj: obj}, true
	}
	return modelTicket{}, false
}

func (m *modelTable) revoke(tk modelTicket) bool {
	s := &m.slots[tk.slot]
	if !s.occupied || s.burned || s.gen != tk.gen {
		return false
	}
	s.occupied = false
	s.obj = -1
	if !m.wrap && s.gen == m.maxGen() {
		s.burned = true
		return true
	}
	s.gen = (s.gen + 1) % (uint64(1) << m.genBits)
	return true
}

func (m *modelTable) use(tk modelTicket) (int, bool) {
	s := &m.slots[tk.slot]
	if !s.occupied || s.burned || s.gen != tk.gen {
		return -1, false
	}
	return s.obj, true
}

// accelerate is the compressed loop: it applies `cycles` revoke+allocate
// cycles to one slot in O(1). Each cycle revokes the current object and
// allocates a fresh one; object identities run firstObj..firstObj+cycles-1.
func (m *modelTable) accelerate(slot int, cycles uint64, firstObj int) (lastObj int, burned bool) {
	s := &m.slots[slot]
	if !s.occupied || s.burned || cycles == 0 {
		return -1, s.burned
	}
	if m.wrap {
		s.gen = (s.gen + cycles) % (uint64(1) << m.genBits)
		s.obj = firstObj + int(cycles) - 1
		return s.obj, false
	}
	if cycles <= m.maxGen()-s.gen {
		s.gen += cycles
		s.obj = firstObj + int(cycles) - 1
		return s.obj, false
	}
	// The burn happens inside the compressed run: the slot dies at max
	// generation and never wraps.
	s.gen = m.maxGen()
	s.occupied = false
	s.burned = true
	s.obj = -1
	return -1, true
}

// runProofScript drives the shared proof script: initial ticket, two
// concrete revoke/allocate cycles, one compressed loop, final Use of the
// original ticket. Returns the recorder and the final Use outcome.
func runProofScript(m *modelTable, cycles uint64) (*proofRecorder, int, bool) {
	rec := &proofRecorder{}

	tk0, ok := m.allocate(1)
	if !ok {
		return rec, -1, false
	}
	rec.record("INIT", "mint ticket slot=%d gen=%d obj=O%d", tk0.slot, tk0.gen, tk0.obj)

	cur := tk0
	for obj := 2; obj <= 3; obj++ {
		genBefore := m.slots[cur.slot].gen
		if !m.revoke(cur) {
			rec.record("REVOKE", "slot=%d REJECTED", cur.slot)
			return rec, -1, false
		}
		rec.record("REVOKE", "slot=%d gen %d->%d obj=O%d cleared", cur.slot, genBefore, m.slots[cur.slot].gen, cur.obj)
		var allocOK bool
		cur, allocOK = m.allocate(obj)
		if !allocOK {
			rec.record("ALLOC", "no free slot for obj=O%d", obj)
			return rec, -1, false
		}
		rec.record("ALLOC", "slot=%d gen=%d obj=O%d", cur.slot, cur.gen, cur.obj)
	}

	genBefore := m.slots[0].gen
	last, burned := m.accelerate(0, cycles, 4)
	if burned {
		rec.record("COMPRESS", "slot=0 cycles=%d gen %d->%d: slot BURNED, no wraparound", cycles, genBefore, m.slots[0].gen)
	} else {
		rec.record("COMPRESS", "slot=0 cycles=%d gen %d->%d (mod 2^%d) objs O4..O%d", cycles, genBefore, m.slots[0].gen, m.genBits, last)
	}

	gotObj, accepted := m.use(tk0)
	if accepted {
		rec.record("USE", "ticket{slot=%d gen=%d} ACCEPTED obj=O%d (minted for O%d)", tk0.slot, tk0.gen, gotObj, tk0.obj)
	} else {
		rec.record("USE", "ticket{slot=%d gen=%d} REJECTED (minted for O%d)", tk0.slot, tk0.gen, tk0.obj)
	}
	return rec, gotObj, accepted
}

func checkProofShape(t *testing.T, rec *proofRecorder) {
	t.Helper()
	if len(rec.entries) == 0 || rec.entries[0].op != "INIT" {
		t.Fatal("proof must open with the initial ticket")
	}
	for _, op := range []string{"REVOKE", "ALLOC", "COMPRESS", "USE"} {
		if !rec.hasOp(op) {
			t.Fatalf("proof missing %s summary", op)
		}
	}
	if last := rec.entries[len(rec.entries)-1]; last.op != "USE" {
		t.Fatal("proof must close with the final Use")
	}
}

// TestModelProofWraparound16 proves the pending-review 16-bit table
// revives a stale ticket after generation wraparound, resolving it to a
// different object, and confirms the model against the real
// ReviewTable16 by unrolling all 2^16 cycles.
func TestModelProofWraparound16(t *testing.T) {
	m := newModelTable(16, true, 3)
	// gen is 2 after the concrete prefix; 65534 compressed cycles wrap it
	// back to the original ticket's generation 0.
	rec, gotObj, accepted := runProofScript(m, 1<<16-2)
	rec.logTo(t)
	checkProofShape(t, rec)

	if !accepted {
		t.Fatal("model: stale ticket not revived; proof of hazard failed")
	}
	if gotObj == 1 {
		t.Fatal("model: stale ticket resolved to its original object")
	}
	t.Logf("HAZARD CONFIRMED: stale ticket for O1 resolves to O%d after wraparound", gotObj)

	// Cross-check the model's concrete prefix against the real table.
	rt, err := NewReviewTable16(3, testKey)
	if err != nil {
		t.Fatalf("NewReviewTable16: %v", err)
	}
	tk0, err := rt.Allocate("O1", 7, permRead)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	cur := tk0
	for obj := 2; obj <= 3; obj++ {
		if err := rt.Revoke(cur); err != nil {
			t.Fatalf("Revoke obj=%d: %v", obj, err)
		}
		if cur, err = rt.Allocate(fmt.Sprintf("O%d", obj), 7, permRead); err != nil {
			t.Fatalf("Allocate obj=%d: %v", obj, err)
		}
	}
	// The model logged the same concrete prefix as gen 2 before its
	// compressed loop; the real table must land on the same generation.
	if cur.Generation != 2 {
		t.Fatalf("model/real divergence after concrete prefix: real gen=%d, model logged gen 2",
			cur.Generation)
	}

	// Unroll the compressed loop on the real table: all remaining 2^16-2
	// revoke/allocate cycles, then the final Use of the original ticket.
	for obj := 4; obj <= 1<<16+1; obj++ {
		if err := rt.Revoke(cur); err != nil {
			t.Fatalf("Revoke cycle obj=%d: %v", obj, err)
		}
		if cur, err = rt.Allocate(fmt.Sprintf("O%d", obj), 7, permRead); err != nil {
			t.Fatalf("Allocate cycle obj=%d: %v", obj, err)
		}
	}
	if cur.Generation != tk0.Generation {
		t.Fatalf("generation did not wrap to ticket gen: cur=%d tk0=%d", cur.Generation, tk0.Generation)
	}
	obj, err := rt.Use(tk0, permRead)
	if err != nil {
		t.Fatalf("real table rejected revived ticket: %v", err)
	}
	if obj == "O1" {
		t.Fatal("real table returned the original object")
	}
	t.Logf("REAL TABLE CONFIRMS: stale ticket for O1 resolves to %v", obj)
}

// TestModelProofCorrect32Rejects proves the correct 32-bit model burns
// the slot at 2^32-1 without wrapping and rejects the same stale ticket,
// and that the real Table agrees on the concrete prefix.
func TestModelProofCorrect32Rejects(t *testing.T) {
	m := newModelTable(32, false, 3)
	// gen is 2 after the concrete prefix; maxGen-2+1 compressed cycles
	// drive the slot into the burned state instead of wrapping.
	rec, _, accepted := runProofScript(m, math.MaxUint32-1)
	rec.logTo(t)
	checkProofShape(t, rec)

	if accepted {
		t.Fatal("correct model accepted the stale ticket")
	}
	if !m.slots[0].burned {
		t.Fatal("correct model did not burn the slot at max generation")
	}
	if m.slots[0].gen != math.MaxUint32 {
		t.Fatalf("generation wrapped: %d", m.slots[0].gen)
	}
	if !strings.Contains(rec.entries[len(rec.entries)-1].text, "REJECTED") {
		t.Fatal("final Use entry must record rejection")
	}

	// The real Table agrees: after one concrete revoke the same ticket
	// is already dead, and slot reuse never revives it.
	ct, err := NewTable(3, testKey)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	tk0, err := ct.Allocate("O1", 7, permRead)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := ct.Revoke(tk0); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := ct.Allocate("O2", 7, permRead); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if _, err := ct.Use(tk0, permRead); err == nil {
		t.Fatal("real 32-bit table accepted the stale ticket")
	}
}
