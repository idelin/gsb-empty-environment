package netsec

import "sort"

// Dimensions of a key type. Every key term owns one union-find variable per
// dimension; same-key aliases merge the variables, while constructors and
// NewKey declarations bind their finite domains.
const (
	dimOwner = iota
	dimPurpose
	dimSession
	dimCount
)

// Clash describes one unsatisfied dimension constraint: the same key term
// (or two aliased key terms) is forced to take two different values in one
// type dimension. Provenance is the set of operations that bound the values.
type Clash struct {
	TermA     string
	TermB     string
	Dimension string
	Values    []string
	Prov      []string
}

// SolveStats summarizes solver work for host diagnostics.
type SolveStats struct {
	Operations  int
	Variables   int
	Clashes     int
	SearchNodes int
	Exact       bool
}

// Report is the result of solving a script.
type Report struct {
	// Confusable is true when the constraints admit at least one type clash,
	// i.e. the script may exhibit key-purpose confusion (or an owner/session
	// mismatch flowing through an alias).
	Confusable bool
	// Clashes lists every unsatisfied dimension constraint found during
	// constraint propagation.
	Clashes []Clash
	// ConflictOps is one minimum-size set of operations whose removal makes
	// every recorded clash disappear.
	ConflictOps []string
	// MinConflictSets lists all minimum-size hitting sets, each one a
	// minimum conflict operation set. Use ConflictOps when only one is needed.
	MinConflictSets [][]string
	// KeyTypes holds the inferred type of every concrete key term. A dimension
	// left empty could not be uniquely determined.
	KeyTypes map[string]KeyType
	Stats    SolveStats
	Errors   []error
}

type solver struct {
	parent []int
	rank   []int
	// dom[dim][root] is the set of values forced onto that equivalence class.
	dom [dimCount]map[int]map[string]struct{}
	// prov[dim][root][value] are the operations that forced the value.
	prov [dimCount]map[int]map[string]map[string]struct{}
	// vars maps a concrete term label to its three union-find variables.
	vars map[string][dimCount]int
}

func newSolver() *solver {
	s := &solver{vars: make(map[string][dimCount]int)}
	for d := 0; d < dimCount; d++ {
		s.dom[d] = make(map[int]map[string]struct{})
		s.prov[d] = make(map[int]map[string]map[string]struct{})
	}
	return s
}

func (s *solver) fresh() int {
	id := len(s.parent)
	s.parent = append(s.parent, id)
	s.rank = append(s.rank, 0)
	return id
}

func (s *solver) find(x int) int {
	root := x
	for s.parent[root] != root {
		root = s.parent[root]
	}
	for s.parent[x] != x {
		next := s.parent[x]
		s.parent[x] = root
		x = next
	}
	return root
}

// termVars returns the three dimension variables of a concrete key term,
// allocating them together so all dimensions of one term stay correlated.
func (s *solver) termVars(t Term) [dimCount]int {
	label := TermLabel(t)
	if vs, ok := s.vars[label]; ok {
		return vs
	}
	vs := [dimCount]int{s.fresh(), s.fresh(), s.fresh()}
	s.vars[label] = vs
	return vs
}

// bind forces dimension dim of term t to value. trackProvenance controls
// whether op is recorded as a removable conflict operation: NewKey
// declarations are model inputs (facts) and therefore never appear in a
// minimum conflict set, whereas constructors and aliases are script actions.
func (s *solver) bind(t Term, dim int, value, op string) {
	s.bindTracked(t, dim, value, op, true)
}

func (s *solver) bindFact(t Term, dim int, value string) {
	s.bindTracked(t, dim, value, "", false)
}

func (s *solver) bindTracked(t Term, dim int, value, op string, track bool) {
	if value == "" {
		return
	}
	v := s.termVars(t)[dim]
	root := s.find(v)
	values := s.dom[dim][root]
	if values == nil {
		values = make(map[string]struct{})
		s.dom[dim][root] = values
	}
	values[value] = struct{}{}
	byValue := s.prov[dim][root]
	if byValue == nil {
		byValue = make(map[string]map[string]struct{})
		s.prov[dim][root] = byValue
	}
	if track && op != "" {
		ops := byValue[value]
		if ops == nil {
			ops = make(map[string]struct{})
			byValue[value] = ops
		}
		ops[op] = struct{}{}
	}
}

// union merges all dimension variables of two aliased key terms and returns
// any clashes that become visible when finite domains are combined.
func (s *solver) union(a, b Term, op string) {
	va := s.termVars(a)
	vb := s.termVars(b)
	for d := 0; d < dimCount; d++ {
		rootA := s.find(va[d])
		rootB := s.find(vb[d])
		ra := rootA
		if rootA != rootB {
			child, parent := rootB, rootA
			if s.rank[rootA] < s.rank[rootB] {
				child, parent = rootA, rootB
				ra = rootB
			}
			if s.dom[d][parent] == nil {
				s.dom[d][parent] = make(map[string]struct{})
			}
			if s.prov[d][parent] == nil {
				s.prov[d][parent] = make(map[string]map[string]struct{})
			}
			for val := range s.dom[d][child] {
				s.dom[d][parent][val] = struct{}{}
				if _, ok := s.prov[d][parent][val]; !ok {
					s.prov[d][parent][val] = make(map[string]struct{})
				}
				for p := range s.prov[d][child][val] {
					s.prov[d][parent][val][p] = struct{}{}
				}
			}
			delete(s.dom[d], child)
			delete(s.prov[d], child)
			s.parent[child] = parent
			if s.rank[parent] == s.rank[child] {
				s.rank[parent]++
			}
		}
		// The alias operation itself is a removable action, so it is
		// recorded as provenance on every value it joins.
		if op != "" {
			if s.dom[d][ra] == nil {
				s.dom[d][ra] = make(map[string]struct{})
			}
			if s.prov[d][ra] == nil {
				s.prov[d][ra] = make(map[string]map[string]struct{})
			}
			for val := range s.dom[d][ra] {
				if _, ok := s.prov[d][ra][val]; !ok {
					s.prov[d][ra][val] = make(map[string]struct{})
				}
				s.prov[d][ra][val][op] = struct{}{}
			}
		}
	}
}

func (s *solver) checkClash(dim int, root int, label string) []Clash {
	values := s.dom[dim][root]
	if len(values) <= 1 {
		return nil
	}
	provSet := make(map[string]struct{})
	valueList := make([]string, 0, len(values))
	for val := range values {
		valueList = append(valueList, val)
		for p := range s.prov[dim][root][val] {
			provSet[p] = struct{}{}
		}
	}
	sort.Strings(valueList)
	provList := make([]string, 0, len(provSet))
	for p := range provSet {
		provList = append(provList, p)
	}
	sort.Strings(provList)
	return []Clash{{
		TermA:     label,
		TermB:     label,
		Dimension: dimName(dim),
		Values:    valueList,
		Prov:      provList,
	}}
}

func dimName(dim int) string {
	switch dim {
	case dimOwner:
		return "owner"
	case dimPurpose:
		return "purpose"
	case dimSession:
		return "session"
	default:
		return "unknown"
	}
}

// Solve expands all templates, posts the finite-domain constraints and decides
// whether the script can confuse key purposes. The returned report always
// carries a minimum conflict operation set when clashes exist.
//
// searchBudget bounds the minimum-hitting-set search; pass 0 for the package
// default. When the budget is exhausted the report still contains a
// minimum-cardinality set found so far but Stats.Exact is set to false.
func Solve(script *Script, searchBudget int) *Report {
	rep := &Report{KeyTypes: make(map[string]KeyType)}
	if script == nil {
		rep.Errors = append(rep.Errors, errNilScript())
		return rep
	}
	eng := &engine{script: script, opSeen: make(map[string]struct{})}
	ops, errs := eng.instantiate()
	rep.Errors = append(rep.Errors, errs...)

	s := newSolver()
	var clashGroups [][]string
	addClashes := func(cs []Clash) {
		for _, c := range cs {
			rep.Clashes = append(rep.Clashes, c)
			clashGroups = append(clashGroups, append([]string(nil), c.Prov...))
		}
	}

	for _, op := range ops {
		switch op.Kind {
		case KindNewKey:
			s.bindFact(op.Key, dimOwner, op.Owner)
			s.bindFact(op.Key, dimPurpose, string(op.Purpose))
			s.bindFact(op.Key, dimSession, op.Session)
		case KindEncrypt:
			s.bind(op.Key, dimPurpose, string(PurposeEncrypt), op.ID)
		case KindSign:
			s.bind(op.Key, dimPurpose, string(PurposeVerify), op.ID)
		case KindDerive:
			s.bind(op.Key, dimPurpose, string(PurposeDerive), op.ID)
		case KindBindKey:
			if len(op.Payload) != 1 {
				rep.Errors = append(rep.Errors, errBadBind(op.ID))
				continue
			}
			s.union(op.Key, op.Payload[0], op.ID)
		}
	}

	for dim := 0; dim < dimCount; dim++ {
		seenRoots := make(map[int]struct{})
		for _, vs := range s.vars {
			root := s.find(vs[dim])
			if _, ok := seenRoots[root]; ok {
				continue
			}
			seenRoots[root] = struct{}{}
			values := s.dom[dim][root]
			if len(values) > 1 {
				label := s.rootLabel(dim, root)
				addClashes(s.checkClash(dim, root, label))
			}
		}
	}

	rep.Confusable = len(rep.Clashes) > 0
	if rep.Confusable {
		sets, nodes, exact := minimumHittingSets(clashGroups, searchBudget)
		rep.MinConflictSets = sets
		if len(sets) > 0 {
			rep.ConflictOps = sets[0]
		}
		rep.Stats.SearchNodes = nodes
		rep.Stats.Exact = exact
	} else {
		rep.Stats.Exact = true
	}

	for label, vs := range s.vars {
		ownerRoot := s.find(vs[dimOwner])
		purposeRoot := s.find(vs[dimPurpose])
		sessionRoot := s.find(vs[dimSession])
		rep.KeyTypes[label] = KeyType{
			Owner:     soleValue(s.dom[dimOwner][ownerRoot]),
			Purpose:   Purpose(soleValue(s.dom[dimPurpose][purposeRoot])),
			SessionID: soleValue(s.dom[dimSession][sessionRoot]),
		}
	}
	rep.Stats.Operations = len(ops)
	rep.Stats.Variables = len(s.parent)
	rep.Stats.Clashes = len(rep.Clashes)
	return rep
}

func (s *solver) rootLabel(dim, root int) string {
	for label, vs := range s.vars {
		if s.find(vs[dim]) == root {
			return label
		}
	}
	return ""
}

func soleValue(values map[string]struct{}) string {
	if len(values) != 1 {
		return ""
	}
	for v := range values {
		return v
	}
	return ""
}
