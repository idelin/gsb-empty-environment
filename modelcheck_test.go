package netsec

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	procVictim   uint32 = 1
	procPeer     uint32 = 2
	procAttacker uint32 = 3

	obj1 ObjectID = 11
	obj2 ObjectID = 22
)

const bfsDepth = 5

// sut is the BFS state: a system under test plus the attacker's knowledge
// (a previously issued broker capability and previously recorded refs).
type sut struct {
	sys        System
	broker     *Capability
	hasBroker  bool
	recV       ProcessRef
	recA       ProcessRef
	recH       HandleRef
	hasRec     bool
	staleDupOK bool
}

func (s *sut) Clone() *sut {
	c := *s
	c.sys = s.sys.CloneSystem()
	return &c
}

func (s *sut) Key() string {
	k := s.sys.StateKey()
	if s.hasBroker {
		k += fmt.Sprintf("broker(%d#%d>%d#%d,%s,%d)",
			s.broker.Source.ID, s.broker.Source.Generation,
			s.broker.Target.ID, s.broker.Target.Generation,
			s.broker.ObjectType, s.broker.mac)
	}
	if s.hasRec {
		k += fmt.Sprintf("rec(%v,%v,%v)", s.recV, s.recA, s.recH)
	}
	if s.staleDupOK {
		k += "staleDupOK"
	}
	return k
}

// newSUT builds the scenario: a high-integrity victim holding a read-only
// handle to obj1, and a low-integrity attacker with no handles. Nothing in
// the scenario ever legitimately grants the attacker Write, so any effective
// Write bit held by the attacker is a safety violation.
func newSUT(sys System) *sut {
	sys.AddObject(obj1, FileType)
	sys.AddObject(obj2, FileType)
	sys.AddProcess(procVictim, IntegrityHigh)
	sys.AddProcess(procAttacker, IntegrityLow)
	vref, _ := sys.ProcRef(procVictim)
	if _, err := sys.OpenHandle(vref, obj1, AccessRead, false); err != nil {
		panic(err)
	}
	return &sut{sys: sys}
}

func curRefs(s *sut) (ProcessRef, ProcessRef) {
	v, _ := s.sys.ProcRef(procVictim)
	a, _ := s.sys.ProcRef(procAttacker)
	return v, a
}

func curHandle(s *sut) HandleRef {
	g, _ := s.sys.HandleGeneration(procVictim, 0)
	return HandleRef{Slot: 0, Generation: g}
}

// forgedCap is a capability the attacker crafted itself: correct fields, no
// host MAC. The reviewed host rejects it; the pending-review host does not.
func forgedCap(v, a ProcessRef) *Capability {
	return &Capability{Source: v, Target: a, ObjectType: FileType.Name}
}

func attackerHasWrite(s *sut) bool {
	return s.sys.HasEffectiveAccess(procAttacker, AccessWrite)
}

func dupAction(name string, build func(s *sut) DuplicateRequest) Action[*sut] {
	return Action[*sut]{
		Name:      name,
		Violation: attackerHasWrite,
		Do:        func(s *sut) { _, _ = s.sys.Duplicate(build(s)) },
	}
}

func envAction(name string, do func(s *sut)) Action[*sut] {
	return Action[*sut]{Name: name, Violation: attackerHasWrite, Do: do}
}

func actDupPlainWrite() Action[*sut] {
	return dupAction("dup desired=Write", func(s *sut) DuplicateRequest {
		v, a := curRefs(s)
		return DuplicateRequest{Source: v, SourceHandle: curHandle(s), Target: a, DesiredAccess: AccessWrite, Broker: forgedCap(v, a)}
	})
}

func actDupGenericWrite() Action[*sut] {
	return dupAction("dup desired=GenericWrite", func(s *sut) DuplicateRequest {
		v, a := curRefs(s)
		return DuplicateRequest{Source: v, SourceHandle: curHandle(s), Target: a, DesiredAccess: GenericWrite, Broker: forgedCap(v, a)}
	})
}

func actDupSameAccessConflict() Action[*sut] {
	return dupAction("dup sameAccess+GenericWrite", func(s *sut) DuplicateRequest {
		v, a := curRefs(s)
		return DuplicateRequest{Source: v, SourceHandle: curHandle(s), Target: a, SameAccess: true, DesiredAccess: GenericWrite, Broker: forgedCap(v, a)}
	})
}

func actDupForgedBroker() Action[*sut] {
	return dupAction("dup desired=Write forged-broker", func(s *sut) DuplicateRequest {
		v, a := curRefs(s)
		return DuplicateRequest{Source: v, SourceHandle: curHandle(s), Target: a, DesiredAccess: AccessWrite, Broker: forgedCap(v, a)}
	})
}

func actDupStaleHandleWrite() Action[*sut] {
	return dupAction("dup stale-handle desired=Write", func(s *sut) DuplicateRequest {
		v, a := curRefs(s)
		h := curHandle(s)
		if h.Generation > 0 {
			h.Generation--
		}
		return DuplicateRequest{Source: v, SourceHandle: h, Target: a, DesiredAccess: AccessWrite, Broker: forgedCap(v, a)}
	})
}

func actDupStaleHandleSameAccess() Action[*sut] {
	return dupAction("dup stale-handle sameAccess", func(s *sut) DuplicateRequest {
		v, a := curRefs(s)
		h := curHandle(s)
		if h.Generation > 0 {
			h.Generation--
		}
		return DuplicateRequest{Source: v, SourceHandle: h, Target: a, SameAccess: true, Broker: forgedCap(v, a)}
	})
}

func actCloseVictim() Action[*sut] {
	return envAction("close victim slot0", func(s *sut) {
		v, _ := curRefs(s)
		_ = s.sys.CloseHandle(v, curHandle(s))
	})
}

func actReopenObj2(access Access) Action[*sut] {
	return envAction(fmt.Sprintf("victim reopens obj2 (%s)", accessName(access)), func(s *sut) {
		v, _ := curRefs(s)
		_, _ = s.sys.OpenHandle(v, obj2, access, false)
	})
}

func actRestartAttacker() Action[*sut] {
	return envAction("restart attacker", func(s *sut) { s.sys.RestartProcess(procAttacker) })
}

func actIssueBroker() Action[*sut] {
	return envAction("issue broker cap", func(s *sut) {
		v, a := curRefs(s)
		if c, err := s.sys.IssueBrokerCapability(v, a, FileType.Name); err == nil {
			s.broker, s.hasBroker = c, true
		}
	})
}

func actRecordRefs() Action[*sut] {
	return envAction("record refs", func(s *sut) {
		s.recV, s.recA = curRefs(s)
		s.recH = curHandle(s)
		s.hasRec = true
	})
}

// actDupRecorded replays the exact pre-recorded request with the stored
// broker capability. The violation flag is raised only if the duplicate
// succeeds while the recorded process generations are already stale.
func actDupRecorded() Action[*sut] {
	return Action[*sut]{
		Name:      "dup recorded-refs stored-cap",
		Violation: func(s *sut) bool { return s.staleDupOK },
		Do: func(s *sut) {
			if !s.hasRec || !s.hasBroker {
				return
			}
			_, err := s.sys.Duplicate(DuplicateRequest{
				Source: s.recV, SourceHandle: s.recH, Target: s.recA,
				DesiredAccess: AccessWrite, Broker: s.broker,
			})
			if err != nil {
				return
			}
			curV, curA := curRefs(s)
			if s.recV != curV || s.recA != curA {
				s.staleDupOK = true
			}
		},
	}
}

func accessName(a Access) string {
	switch a {
	case AccessRead:
		return "read"
	case AccessRead | AccessWrite:
		return "read|write"
	default:
		return fmt.Sprintf("%x", a)
	}
}

func allActions() []Action[*sut] {
	return []Action[*sut]{
		actDupPlainWrite(),
		actDupGenericWrite(),
		actDupSameAccessConflict(),
		actDupForgedBroker(),
		actDupStaleHandleWrite(),
		actCloseVictim(),
		actReopenObj2(AccessRead),
		actRestartAttacker(),
		actIssueBroker(),
		actRecordRefs(),
		actDupRecorded(),
	}
}

type category struct {
	name    string
	actions []Action[*sut]
}

func categories() []category {
	return []category{
		{"generic mapping escalation", []Action[*sut]{actDupGenericWrite()}},
		{"sameAccess conflict", []Action[*sut]{actDupSameAccessConflict()}},
		{"forged broker capability", []Action[*sut]{actDupForgedBroker()}},
		{"close/reuse race", []Action[*sut]{
			actCloseVictim(),
			actReopenObj2(AccessRead | AccessWrite),
			actDupStaleHandleSameAccess(),
		}},
		{"target process restart (stale binding)", []Action[*sut]{
			actRecordRefs(),
			actIssueBroker(),
			actRestartAttacker(),
			actDupRecorded(),
		}},
		{"full action set", allActions()},
	}
}

// TestSecureHostHasNoViolation proves (within the bounded state space) that
// the reviewed host never lets the attacker obtain an ungranted Write and
// never accepts a duplicate bound to stale generations.
func TestSecureHostHasNoViolation(t *testing.T) {
	for _, c := range categories() {
		trace, found := BFS(newSUT(NewHost(0x5eed)), c.actions, bfsDepth)
		if found {
			t.Fatalf("%s: secure host violated via %v", c.name, trace)
		}
	}
}

// TestReviewHostViolations demonstrates that the pending-review host is
// exploitable in every category and logs the shortest trace for each.
func TestReviewHostViolations(t *testing.T) {
	for _, c := range categories() {
		trace, found := BFS(newSUT(NewReviewHost(0x5eed)), c.actions, bfsDepth)
		if !found {
			t.Fatalf("%s: expected a violation trace on the review host", c.name)
		}
		t.Logf("%s: shortest trace (%d steps): %s", c.name, len(trace), strings.Join(trace, " -> "))
	}
}

// TestGenerateTraceReport writes TRACES.md with the shortest counterexample
// trace per category for the pending-review implementation.
func TestGenerateTraceReport(t *testing.T) {
	var b strings.Builder
	b.WriteString("# BFS counterexample traces\n\n")
	b.WriteString("Scenario: high-integrity victim holds a read-only File handle (slot 0);\n")
	b.WriteString("the low-integrity attacker holds nothing. Violation: attacker obtains an\n")
	b.WriteString("effective Write it was never granted (or a duplicate bound to stale\n")
	b.WriteString("generations succeeds). Search is breadth-first, so traces are minimal\n")
	b.WriteString(fmt.Sprintf("within depth %d.\n\n", bfsDepth))
	for _, c := range categories() {
		fmt.Fprintf(&b, "## %s\n\n", c.name)
		trace, found := BFS(newSUT(NewReviewHost(0x5eed)), c.actions, bfsDepth)
		if found {
			fmt.Fprintf(&b, "ReviewHost (pending review): VIOLATION in %d step(s):\n\n", len(trace))
			for i, step := range trace {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
			}
		} else {
			b.WriteString("ReviewHost (pending review): no violation found.\n")
		}
		_, secureFound := BFS(newSUT(NewHost(0x5eed)), c.actions, bfsDepth)
		if secureFound {
			b.WriteString("\nHost (reviewed): VIOLATION (unexpected!)\n\n")
		} else {
			fmt.Fprintf(&b, "\nHost (reviewed): no violation reachable within depth %d.\n\n", bfsDepth)
		}
	}
	if err := os.WriteFile("TRACES.md", []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write TRACES.md: %v", err)
	}
}
