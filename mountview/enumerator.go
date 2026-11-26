package mountview

import "fmt"

// concState is one fully concrete reconstruction state used by the
// full-enumeration oracle. Unlike the may-lattice engine, every field here is
// exact; the set of all such states spans the compatible histories.
type concState struct {
	groups map[GroupID]*concGroup
	paths  map[string]*concPath
}

type concGroup struct {
	present bool
	object  ObjectID
	inter   Interval
	perm    Perm
	prop    Propagation
	domain  DomainID
}

type concPath struct {
	symlink bool
	target  string
	group   GroupID
}

// EnumerationReport is the cross-check output for one target.
type EnumerationReport struct {
	Target          string
	Histories       int
	Truncated       bool
	ObjectMay       []ObjectID
	IntervalMay     []Interval
	PermMay         []Perm
	AbsencePossible bool
	Verdict         Verdict
}

// Enumerate reconstructs every concrete history compatible with the signed
// checkpoints and the bounded missing windows, then evaluates target across
// all of them. MaxOps in each GapSpec bounds the search so this reference
// oracle stays exhaustive only for deliberately short logs; Truncated is set
// if the cap is hit (in which case the abstract engine remains authoritative).
func (e *Engine) Enumerate(target string, capHistories int) (*EnumerationReport, error) {
	tp, err := CleanPath(target)
	if err != nil {
		return nil, err
	}
	in := e.lastReplay
	if in == nil {
		return nil, fmt.Errorf("mountview: enumerator requires a successful Replay")
	}
	items, err := orderReplay(in)
	if err != nil {
		return nil, err
	}
	report := &EnumerationReport{Target: tp}
	states := []*concState{e.genesisState()}

	for _, it := range items {
		switch it.kind {
		case itemCheckpoint:
			for _, s := range states {
				s.reconcileCheckpoint(it.ckpt)
			}
		case itemEntry:
			for _, s := range states {
				if err := s.applyEntry(it.entry); err != nil {
					return nil, err
				}
			}
		case itemGap:
			next, truncated := expandGap(states, it.gap, capHistories)
			if truncated {
				report.Truncated = true
			}
			states = next
		}
	}

	report.Histories = len(states)
	objects := setO{}
	intervals := setI{}
	perms := setP{}
	for _, s := range states {
		gid, present, link := s.resolve(tp, 0)
		if !present || link {
			report.AbsencePossible = true
			continue
		}
		g := s.groups[gid]
		if g == nil || !g.present {
			report.AbsencePossible = true
			continue
		}
		objects.add(g.object)
		intervals.add(g.inter)
		perms.add(g.perm)
	}
	report.ObjectMay = objects.list()
	report.IntervalMay = intervals.list()
	report.PermMay = perms.list()
	report.Verdict = enumerateVerdict(report)
	return report, nil
}

func enumerateVerdict(r *EnumerationReport) Verdict {
	if r.Truncated || r.AbsencePossible ||
		len(r.ObjectMay) != 1 || len(r.IntervalMay) != 1 || len(r.PermMay) != 1 {
		return Unknown
	}
	return Recoverable
}

func (e *Engine) genesisState() *concState {
	s := &concState{groups: map[GroupID]*concGroup{}, paths: map[string]*concPath{}}
	if e.lastReplay == nil || len(e.lastReplay.Checkpoints) == 0 {
		return s
	}
	var first *Checkpoint
	for _, c := range e.lastReplay.Checkpoints {
		if first == nil || c.Seq < first.Seq {
			first = c
		}
	}
	s.reconcileCheckpoint(first)
	return s
}

func (s *concState) reconcileCheckpoint(c *Checkpoint) {
	aliveGroups := map[GroupID]bool{}
	for _, b := range c.View {
		aliveGroups[b.Group] = true
		s.groups[b.Group] = &concGroup{
			present: true,
			object:  b.Object,
			inter:   b.Interval,
			perm:    b.Perm,
			prop:    b.Propagation,
			domain:  b.Domain,
		}
		s.paths[b.Path] = &concPath{group: b.Group}
	}
	for id, g := range s.groups {
		if !aliveGroups[id] {
			g.present = false
		}
	}
	for _, l := range c.Links {
		if _, ok := s.paths[l.Path]; ok {
			continue
		}
		s.paths[l.Path] = &concPath{symlink: true, target: l.Target}
	}
}

func (s *concState) applyEntry(en *LogEntry) error {
	switch en.Op {
	case OpBindMount:
		s.groups[en.Group] = &concGroup{
			present: true, object: en.Object, inter: en.Interval,
			perm: en.Perm, prop: en.Prop, domain: en.Domain,
		}
		s.paths[en.Path] = &concPath{group: en.Group}
	case OpUnmount:
		if p := s.paths[en.Path]; p != nil {
			delete(s.paths, en.Path)
		}
	case OpMove:
		if p := s.paths[en.Path]; p != nil {
			delete(s.paths, en.Path)
			s.paths[en.NewPath] = p
		}
	case OpSymlink:
		s.paths[en.Path] = &concPath{symlink: true, target: en.Target}
	case OpHardlink:
		if src := s.paths[en.Path]; src != nil {
			s.paths[en.NewPath] = &concPath{group: src.group}
		}
	case OpSlice:
		if p := s.paths[en.Path]; p != nil {
			if g := s.groups[p.group]; g != nil {
				g.inter = en.Interval
			}
		}
	case OpChmod:
		if p := s.paths[en.Path]; p != nil {
			if g := s.groups[p.group]; g != nil {
				g.perm = en.Perm
			}
		}
	case OpPropagation:
		if p := s.paths[en.Path]; p != nil {
			if g := s.groups[p.group]; g != nil {
				g.prop = en.Prop
				g.domain = en.Domain
			}
		}
	default:
		return fmt.Errorf("mountview: enumerator unknown op %q", en.Op)
	}
	return nil
}

func (s *concState) resolve(p string, hops int) (GroupID, bool, bool) {
	if hops > maxSymlinkHops {
		return 0, false, true
	}
	cell, ok := s.paths[p]
	if !ok {
		return 0, false, false
	}
	if cell.symlink {
		return s.resolve(cell.target, hops+1)
	}
	g := s.groups[cell.group]
	if g == nil || !g.present {
		return 0, false, false
	}
	return cell.group, true, false
}

func (s *concState) clone() *concState {
	cp := &concState{groups: map[GroupID]*concGroup{}, paths: map[string]*concPath{}}
	for id, g := range s.groups {
		gc := *g
		cp.groups[id] = &gc
	}
	for p, c := range s.paths {
		cc := *c
		cp.paths[p] = &cc
	}
	return cp
}
