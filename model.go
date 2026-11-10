package main

import (
	"fmt"
	"strings"
)

// State is one thread's five capability sets plus the per-cap evidence of
// when each capability first appeared in the effective set.
type State struct {
	Permitted   CapMask // P
	Inheritable CapMask // I
	Effective   CapMask // E
	Ambient     CapMask // A
	Bounding    CapMask // B

	firstEffective map[uint]Evidence
}

// Evidence records why a capability bit first became effective.
type Evidence struct {
	Step   int
	Op     string
	Detail string
}

// Event is one parsed line from the replay trace.
type Event struct {
	Line   int
	Kind   string
	Target string // exec path, or cap name for prctl operations

	// capset target sets / init sets
	P, I, E, B       CapMask
	boundingProvided bool

	// exec file capability sets
	FP, FI CapMask
	FE     bool // file effective is a single flag bit

	// exec mode flags
	SUID   bool
	SGID   bool
	Nosuid bool

	// ambient / bounding operation arguments
	Caps CapMask
	Mode string // raise|lower|drop
}

// initialState models a freshly execed unprivileged process: full bounding
// set, everything else empty.
func initialState() *State {
	s := &State{
		Bounding:       AllCaps,
		firstEffective: map[uint]Evidence{},
	}
	s.recordEffective(0, "init", "present in the starting effective set")
	return s
}

// applyInit replaces the starting sets. Bounding defaults to the full set.
func (s *State) applyInit(ev Event) error {
	s.Permitted = ev.P
	s.Inheritable = ev.I
	s.Effective = ev.E
	s.Ambient = 0
	s.Bounding = AllCaps
	if ev.lineSetsBounding() {
		s.Bounding = ev.B
	}
	// Ambient can only ever be (P & I); normalize defensively.
	s.Ambient &= s.Permitted & s.Inheritable
	s.recordEffective(ev.Line, "init", "present in the declared starting effective set")
	return nil
}

func (ev Event) lineSetsBounding() bool { return ev.B != 0 || ev.boundingProvided }

// applyExec replays the capabilities(7) transformation done by execve(2) for
// a non-root thread, with the bounding set and ambient/securebit rules.
func (s *State) applyExec(ev Event) error {
	privileged := ev.isPrivileged() && !ev.Nosuid

	// On MS_NOSUID mounts file capabilities and setuid/setgid are ignored,
	// so the file is treated as unprivileged: fP, fI, fE all behave as zero.
	fP, fI, fE := ev.FP, ev.FI, ev.FE
	if ev.Nosuid {
		fP, fI, fE = 0, 0, false
	}

	oldAmbient := s.Ambient

	// P'(cap) = (P(cap) & B(cap))             [legacy root rule, empty here]
	// P'(cap) = (I(cap) & fI(cap)) | (B(cap) & fP(cap)) | A(cap)
	newP := (s.Inheritable & fI) | (s.Bounding & fP) | s.Ambient

	// P'(cap) must be a subset of the bounding set; the B&fP term already
	// enforces that for file permitted caps, and ambient/inheritable terms
	// are bounded by construction once a cap has been dropped from B.

	// I'(cap) = I(cap): inheritable survives exec unchanged.
	newI := s.Inheritable

	// E'(cap) = fE ? P'(cap) : A(cap)
	var newE CapMask
	if fE {
		newE = newP
	} else {
		newE = s.Ambient
	}

	// A'(cap) = A(cap) for an unprivileged file, cleared otherwise.
	newA := s.Ambient
	if privileged {
		newA = 0
	}

	s.Permitted, s.Inheritable, s.Effective, s.Ambient = newP, newI, newE, newA

	s.recordExecEffective(ev, fP, fI, fE, oldAmbient)
	return nil
}

// isPrivileged mirrors the kernel's "privileged exec" test: any file
// capability present, or a set-user-ID / set-group-ID mode bit.
func (ev Event) isPrivileged() bool {
	return ev.FP != 0 || ev.FI != 0 || ev.FE || ev.SUID || ev.SGID
}

func (s *State) recordExecEffective(ev Event, fP, fI CapMask, fE bool, oldA CapMask) {
	for _, bit := range s.Effective.bits() {
		if _, seen := s.firstEffective[bit]; seen {
			continue
		}
		detail := s.explainExecBit(bit, ev, fP, fI, fE, oldA)
		s.firstEffective[bit] = Evidence{
			Step:   ev.Line,
			Op:     "exec " + ev.Target,
			Detail: detail,
		}
	}
}

func (s *State) explainExecBit(bit uint, ev Event, fP, fI CapMask, fE bool, oldA CapMask) string {
	m := CapMask(1) << bit
	var reasons []string
	if s.Inheritable&fI&m != 0 {
		reasons = append(reasons, "P' bit from thread inheritable & file inheritable (I & fI)")
	}
	if s.Bounding&fP&m != 0 {
		reasons = append(reasons, "P' bit from bounding & file permitted (B & fP)")
	}
	if oldA&m != 0 && !ev.isPrivileged() {
		reasons = append(reasons, "P'/E' bit from ambient set retained across an unprivileged exec (A)")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "effective bit derived from the new permitted set")
	}

	if !fE {
		return "fE=0 so E' = A: ambient cap retained across exec of an unprivileged file"
	}
	return "fE=1 so E' = P': " + strings.Join(reasons, "; ")
}

// applyCapset replays capset(2) permission checks and set replacement.
func (s *State) applyCapset(ev Event) error {
	oldP, oldI, oldE := s.Permitted, s.Inheritable, s.Effective

	// A requested set may never contain bits outside the capability universe.
	if ev.P|ev.I|ev.E > AllCaps {
		return fmt.Errorf("line %d capset: EPERM, unknown capability above CAP_LAST_CAP", ev.Line)
	}

	var violations []string
	// New permitted must be a subset of the old permitted set.
	if missing := ev.P &^ oldP; missing != 0 {
		violations = append(violations, fmt.Sprintf(
			"EPERM: new permitted adds %s not held in old permitted set", renderSet(missing)))
	}
	// Without CAP_SETPCAP in the effective set, new inheritable may only use
	// bits already in (old inheritable | old permitted).
	if oldE&CapSetPCAP == 0 {
		if missing := ev.I &^ (oldI | oldP); missing != 0 {
			violations = append(violations, fmt.Sprintf(
				"EPERM: without cap_setpcap in E, new inheritable adds %s outside I|P", renderSet(missing)))
		}
	}
	// New effective must be a subset of the new permitted set.
	if missing := ev.E &^ ev.P; missing != 0 {
		violations = append(violations, fmt.Sprintf(
			"EPERM: new effective contains %s absent from new permitted", renderSet(missing)))
	}
	if len(violations) != 0 {
		return fmt.Errorf("line %d capset rejected, state unchanged: %s", ev.Line, strings.Join(violations, "; "))
	}

	s.Permitted, s.Inheritable, s.Effective = ev.P, ev.I, ev.E

	// The ambient set is automatically reined in to (P & I); the kernel
	// drops ambient bits that no longer satisfy the invariant.
	s.Ambient &= s.Permitted & s.Inheritable

	for _, bit := range s.Effective.bits() {
		if _, seen := s.firstEffective[bit]; seen {
			continue
		}
		s.firstEffective[bit] = Evidence{
			Step:   ev.Line,
			Op:     "capset",
			Detail: "capset installed the bit in the new effective set (it is a member of new permitted)",
		}
	}
	return nil
}

// applyAmbient replays prctl(PR_CAP_AMBIENT, ...).
func (s *State) applyAmbient(ev Event) error {
	switch ev.Mode {
	case "raise":
		var violations []string
		if missing := ev.Caps &^ s.Permitted; missing != 0 {
			violations = append(violations, fmt.Sprintf(
				"EPERM: %s not in permitted set", renderSet(missing)))
		}
		if missing := ev.Caps &^ s.Inheritable; missing != 0 {
			violations = append(violations, fmt.Sprintf(
				"EPERM: %s not in inheritable set", renderSet(missing)))
		}
		if len(violations) != 0 {
			return fmt.Errorf("line %d PR_CAP_AMBIENT_RAISE rejected, state unchanged: %s",
				ev.Line, strings.Join(violations, "; "))
		}
		s.Ambient |= ev.Caps
	case "lower":
		s.Ambient &^= ev.Caps
	case "drop":
		// PR_CAP_AMBIENT_CLEAR_ALL: no capability arguments.
		s.Ambient = 0
	default:
		return fmt.Errorf("line %d: unknown ambient mode %q", ev.Line, ev.Mode)
	}
	return nil
}

// applyBounding replays prctl(PR_CAPBSET_DROP, ...).
func (s *State) applyBounding(ev Event) error {
	if s.Effective&CapSetPCAP == 0 {
		return fmt.Errorf("line %d PR_CAPBSET_DROP rejected, state unchanged: EPERM, cap_setpcap not in effective set",
			ev.Line)
	}
	s.Bounding &^= ev.Caps
	// Dropping a bounding bit also removes it from permitted, inheritable and
	// ambient: the kernel keeps every thread set within the bounding set.
	s.Permitted &^= ev.Caps
	s.Inheritable &^= ev.Caps
	s.Effective &^= ev.Caps
	s.Ambient &^= ev.Caps
	return nil
}

func (s *State) recordEffective(line int, op, detail string) {
	for _, bit := range s.Effective.bits() {
		if _, seen := s.firstEffective[bit]; seen {
			continue
		}
		s.firstEffective[bit] = Evidence{Step: line, Op: op, Detail: detail}
	}
}
