package mountview

// setG is a finite may-set of hardlink group ids plus an explicit flag for an
// open universe (new group ids that can be minted inside a missing window).
type setG struct {
	m   map[GroupID]bool
	any bool
}

func newSetG() *setG { return &setG{m: map[GroupID]bool{}} }

func (s *setG) add(g GroupID) {
	if s.m == nil {
		s.m = map[GroupID]bool{}
	}
	s.m[g] = true
}

func (s *setG) addAll(o *setG) {
	if o == nil {
		return
	}
	for g := range o.m {
		s.add(g)
	}
	s.any = s.any || o.any
}

func (s *setG) open() { s.any = true }

func (s *setG) list() []GroupID {
	out := make([]GroupID, 0, len(s.m))
	for g := range s.m {
		out = append(out, g)
	}
	return out
}

func (s *setG) singleton() (GroupID, bool) {
	if s.any || len(s.m) != 1 {
		return 0, false
	}
	for g := range s.m {
		return g, true
	}
	return 0, false
}

// setO is a may-set of object ids plus an explicit open-universe flag.
type setO struct {
	m   map[ObjectID]bool
	any bool
}

func newSetO() *setO { return &setO{m: map[ObjectID]bool{}} }

func (s *setO) add(o ObjectID) {
	if s.m == nil {
		s.m = map[ObjectID]bool{}
	}
	s.m[o] = true
}

func (s *setO) list() []ObjectID {
	out := make([]ObjectID, 0, len(s.m))
	for o := range s.m {
		out = append(out, o)
	}
	return out
}

type setI map[Interval]bool

func (s setI) add(i Interval) { s[i] = true }

func (s setI) list() []Interval {
	out := make([]Interval, 0, len(s))
	for i := range s {
		out = append(out, i)
	}
	return out
}

type setP map[Perm]bool

func (s setP) add(p Perm) { s[p] = true }

func (s setP) list() []Perm {
	out := make([]Perm, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	return out
}

type setStr map[string]bool

func (s setStr) add(v string) { s[v] = true }

func (s setStr) list() []string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	return out
}
