package gdict

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxConflictClasses is the input guarantee used by the optimizer: at most
// twelve universal-equivalence classes may contain more than one key group.
// The solver validates this bound before searching.
const MaxConflictClasses = 12

// ErrTooManyConflictClasses reports that the stated conflict-class bound was
// exceeded.
var ErrTooManyConflictClasses = errors.New("gdict: too many conflict equivalence classes")

type occurrence struct {
	part int
	old  uint64
}

type classInfo struct {
	sig       string
	nodes     []int
	canonical Key
}

type candidate struct {
	keyCodes   []uint64 // indexed by active position
	entryCodes []uint64 // canonical partition/old-code order
	pages      int
	exception  int
	used       int
}

type searchState struct {
	n         *normalized
	dag       *dag
	occ       [][]occurrence
	classes   []classInfo
	classOf   []int
	reach     [][]bool
	best      *candidate
	landmarks []uint64
	states    int
	maxState  int
}

// Solve computes a deterministic global dictionary mapping. It first
// minimizes exception translation pages, then the number of distinct
// physical codes actually used, then the lexicographically smallest mapping
// (entries ordered by partition name and old code).
func Solve(cfg Config) (*Plan, error) {
	n, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	d, err := buildDAG(n)
	if err != nil {
		return nil, err
	}
	if len(n.activeIDs) == 0 {
		return buildPlan(n, map[int]uint64{})
	}
	classes, classOf, classOrder, err := buildClasses(n, d)
	if err != nil {
		return nil, err
	}
	conflictClasses := 0
	for _, c := range classes {
		if len(c.nodes) > 1 {
			conflictClasses++
		}
	}
	if conflictClasses > MaxConflictClasses {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyConflictClasses, conflictClasses, MaxConflictClasses)
	}
	s := &searchState{
		n:        n,
		dag:      d,
		classes:  classes,
		classOf:  classOf,
		maxState: n.maxStates,
	}
	s.occ = make([][]occurrence, len(n.activeIDs))
	for pi, p := range n.parts {
		for _, e := range p.entries {
			pos := n.activePos[e.group]
			s.occ[pos] = append(s.occ[pos], occurrence{part: pi, old: e.oldCode})
		}
	}
	s.landmarks = s.buildLandmarks()
	s.reach = make([][]bool, len(n.activeIDs))
	for i := range s.reach {
		s.reach[i] = make([]bool, len(n.activeIDs))
		s.reach[i][i] = true
	}
	for a := 0; a < d.n; a++ {
		for _, b := range d.adj[a] {
			s.reach[a][b] = true
		}
	}
	for k := 0; k < d.n; k++ {
		for i := 0; i < d.n; i++ {
			if !s.reach[i][k] {
				continue
			}
			for j := 0; j < d.n; j++ {
				if s.reach[k][j] {
					s.reach[i][j] = true
				}
			}
		}
	}

	// Generate internal linear extensions once per class.
	classOrders := make([][][]int, len(classes))
	for ci, c := range classes {
		orders, err := s.linearExtensions(c.nodes)
		if err != nil {
			return nil, err
		}
		classOrders[ci] = orders
	}
	var ordered []int
	var visitCombinations func(ci int) error
	visitCombinations = func(ci int) error {
		if ci == len(classOrder) {
			return s.searchOrder(append([]int(nil), ordered...))
		}
		classIdx := classOrder[ci]
		for _, ord := range classOrders[classIdx] {
			before := len(ordered)
			ordered = append(ordered, ord...)
			if err := visitCombinations(ci + 1); err != nil {
				return err
			}
			ordered = ordered[:before]
		}
		return nil
	}
	if err := visitCombinations(0); err != nil {
		return nil, err
	}
	if s.best == nil {
		return nil, s.noFeasibleCertificate()
	}
	codeMap := make(map[int]uint64, len(n.activeIDs))
	for i, gid := range n.activeIDs {
		codeMap[gid] = s.best.keyCodes[i]
	}
	return buildPlan(n, codeMap)
}

func buildClasses(n *normalized, d *dag) ([]classInfo, []int, []int, error) {
	sigOf := func(gid int) string {
		g := n.groups[gid]
		var b strings.Builder
		for _, r := range g.ranks {
			fmt.Fprintf(&b, "%d,", r)
		}
		return b.String()
	}
	bySig := make(map[string]int)
	classOfNode := make([]int, len(n.activeIDs))
	var classes []classInfo
	for pos, gid := range n.activeIDs {
		sig := sigOf(gid)
		ci, ok := bySig[sig]
		if !ok {
			ci = len(classes)
			bySig[sig] = ci
			classes = append(classes, classInfo{sig: sig, canonical: n.groups[gid].canonical})
		}
		classes[ci].nodes = append(classes[ci].nodes, pos)
		classOfNode[pos] = ci
	}
	// Class-level DAG and deterministic topological order.
	cadj := make([][]int, len(classes))
	cindeg := make([]int, len(classes))
	seen := make(map[[2]int]bool)
	for a := 0; a < d.n; a++ {
		for _, b := range d.adj[a] {
			ca, cb := classOfNode[a], classOfNode[b]
			if ca == cb {
				continue
			}
			key := [2]int{ca, cb}
			if seen[key] {
				continue
			}
			seen[key] = true
			cadj[ca] = append(cadj[ca], cb)
			cindeg[cb]++
		}
	}
	var avail []int
	for i := 0; i < len(classes); i++ {
		if cindeg[i] == 0 {
			avail = append(avail, i)
		}
	}
	sort.Slice(avail, func(i, j int) bool { return classes[avail[i]].canonical.Less(classes[avail[j]].canonical) })
	var classOrder []int
	for len(avail) > 0 {
		ci := avail[0]
		avail = avail[1:]
		classOrder = append(classOrder, ci)
		for _, to := range cadj[ci] {
			cindeg[to]--
			if cindeg[to] == 0 {
				avail = append(avail, to)
				sort.Slice(avail, func(i, j int) bool {
					return classes[avail[i]].canonical.Less(classes[avail[j]].canonical)
				})
			}
		}
	}
	if len(classOrder) != len(classes) {
		return nil, nil, nil, incompatible("class-cycle", "collation equivalence classes have contradictory strict order")
	}
	return classes, classOfNode, classOrder, nil
}

func (s *searchState) linearExtensions(nodes []int) ([][]int, error) {
	if len(nodes) == 0 {
		return [][]int{{}}, nil
	}
	local := make(map[int]int, len(nodes))
	for i, node := range nodes {
		local[node] = i
	}
	adj := make([][]int, len(nodes))
	indeg := make([]int, len(nodes))
	internalEdges := 0
	for i, node := range nodes {
		for _, to := range s.dag.adj[node] {
			j, ok := local[to]
			if !ok {
				continue
			}
			adj[i] = append(adj[i], j)
			indeg[j]++
			internalEdges++
		}
	}
	if internalEdges == 0 {
		// n! lower bound on the number of legal orders. Reject before the
		// factorial recursion spends the whole budget.
		bound := 1
		for i := 2; i <= len(nodes); i++ {
			bound *= i
			if bound > s.maxState {
				return nil, fmt.Errorf("%w: universal class with %d unconstrained keys", ErrSearchLimit, len(nodes))
			}
		}
	}
	var out [][]int
	var cur []int
	var rec func() error
	rec = func() error {
		if len(cur) == len(nodes) {
			cp := make([]int, len(cur))
			for i, j := range cur {
				cp[i] = nodes[j]
			}
			out = append(out, cp)
			if len(out) > s.maxState {
				return ErrSearchLimit
			}
			return nil
		}
		// Deterministic tie break by canonical key.
		var choices []int
		for i := range nodes {
			if indeg[i] == 0 {
				choices = append(choices, i)
			}
		}
		sort.Slice(choices, func(a, b int) bool {
			return s.n.groups[nodes[choices[a]]].canonical.Less(s.n.groups[nodes[choices[b]]].canonical)
		})
		for _, j := range choices {
			indeg[j] = -1
			for _, to := range adj[j] {
				indeg[to]--
			}
			cur = append(cur, j)
			if err := rec(); err != nil {
				return err
			}
			cur = cur[:len(cur)-1]
			for _, to := range adj[j] {
				indeg[to]++
			}
			indeg[j] = 0
		}
		return nil
	}
	if err := rec(); err != nil {
		return nil, err
	}
	return out, nil
}

type codeCandidate struct {
	code  uint64
	merge bool // code == previous code
}

func (s *searchState) searchOrder(order []int) error {
	m := len(order)
	posOf := make([]int, m)
	for i, node := range order {
		posOf[node] = i
	}
	for a := 0; a < s.dag.n; a++ {
		for _, b := range s.dag.adj[a] {
			if posOf[a] >= posOf[b] {
				return fmt.Errorf("gdict: internal error: non-topological order")
			}
		}
	}
	codes := make([]uint64, m)
	assigned := make([]bool, m)
	var dfs func(idx int, prev uint64, havePrev bool, used int) error
	dfs = func(idx int, prev uint64, havePrev bool, used int) error {
		s.states++
		if s.states > s.maxState {
			return ErrSearchLimit
		}
		if s.best != nil {
			lb := s.partialRuns(codes, assigned, order)
			if lb > s.best.pages {
				return nil
			}
			if lb == s.best.pages && used > s.best.used {
				return nil
			}
		}
		if idx == m {
			entryCodes := s.entryVector(codes, order)
			pages, exc, err := s.pageStats(codes, order)
			if err != nil {
				return nil
			}
			keyCodes := make([]uint64, m)
			for i, node := range order {
				keyCodes[node] = codes[i]
			}
			cand := &candidate{keyCodes: keyCodes, entryCodes: entryCodes, pages: pages, exception: exc, used: used}
			if betterCandidate(cand, s.best) {
				s.best = cand
			}
			return nil
		}
		node := order[idx]
		gid := s.n.activeIDs[node]
		mergeOK := false
		if idx > 0 {
			mergeOK = s.canShare(order[idx-1], node)
		}
		cands, err := s.candidates(idx, node, order, codes, assigned, prev, havePrev, mergeOK)
		if err != nil {
			return err
		}
		for _, cc := range cands {
			if cc.code >= s.n.globalLimit {
				continue
			}
			if havePrev {
				if cc.merge {
					if !mergeOK || cc.code != prev {
						continue
					}
				} else if cc.code <= prev {
					continue
				}
			}
			if f := s.n.groups[gid].fixed; f != nil && cc.code != *f {
				continue
			}
			if !s.allowedFor(gid, cc.code, mergeOK && cc.merge) {
				continue
			}
			codes[idx] = cc.code
			assigned[idx] = true
			newUsed := used
			if !havePrev || cc.code != prev {
				newUsed++
			}
			if err := dfs(idx+1, cc.code, true, newUsed); err != nil {
				return err
			}
			assigned[idx] = false
		}
		return nil
	}
	return dfs(0, 0, false, 0)
}

func (s *searchState) canShare(a, b int) bool {
	if s.classOf[a] != s.classOf[b] {
		return false
	}
	return !s.reach[a][b] && !s.reach[b][a]
}

func (s *searchState) allowedFor(gid int, code uint64, merging bool) bool {
	if code >= s.n.globalLimit {
		return false
	}
	if inIntervals(code, s.n.holes) {
		return false
	}
	if f := s.n.groups[gid].fixed; f != nil {
		return code == *f
	}
	// A fixed code belongs to its logical key group and may not be stolen.
	for _, other := range s.n.activeIDs {
		f := s.n.groups[other].fixed
		if f != nil && *f == code && other != gid {
			if !merging || !s.canShare(s.n.activePos[gid], s.n.activePos[other]) {
				return false
			}
		}
	}
	return true
}

func (s *searchState) nextAllowed(gid int, at uint64) (uint64, bool) {
	limit := s.n.globalLimit
	x := at
	for x < limit {
		if h, ok := containingHole(s.n.holes, x); ok {
			if h.Hi >= limit-1 {
				return 0, false
			}
			x = h.Hi + 1
			continue
		}
		if s.allowedFor(gid, x, false) {
			return x, true
		}
		x++
	}
	return 0, false
}

func containingHole(holes []Interval, code uint64) (Interval, bool) {
	i := sort.Search(len(holes), func(i int) bool { return holes[i].Hi >= code })
	if i < len(holes) && holes[i].Lo <= code && code <= holes[i].Hi {
		return holes[i], true
	}
	return Interval{}, false
}

func (s *searchState) candidates(idx, node int, order []int, codes []uint64, assigned []bool, prev uint64, havePrev, mergeOK bool) ([]codeCandidate, error) {
	gid := s.n.activeIDs[node]
	var lower uint64
	if havePrev {
		lower = prev + 1
		if lower < prev {
			return nil, nil
		}
	}
	set := make(map[uint64]bool)
	add := func(c uint64) {
		if c >= s.n.globalLimit {
			return
		}
		if havePrev && c < lower {
			return
		}
		set[c] = true
	}
	if f := s.n.groups[gid].fixed; f != nil {
		if !havePrev || *f > prev || (*f == prev && mergeOK) {
			set[*f] = true
		}
	} else {
		if havePrev && mergeOK {
			set[prev] = true
		}
		if c, ok := s.nextAllowed(gid, lower); ok {
			add(c)
			for k := uint64(1); k <= 4; k++ {
				if c2, ok := s.nextAllowed(gid, c+k); ok {
					add(c2)
				}
			}
		}
		// Landmarks: fixed codes and hole boundaries.
		for _, other := range s.n.activeIDs {
			if f := s.n.groups[other].fixed; f != nil {
				add(*f)
				if *f > 0 {
					add(*f - 1)
				}
				if *f+1 >= *f {
					add(*f + 1)
				}
			}
		}
		for _, h := range s.n.holes {
			if h.Lo > 0 {
				add(h.Lo - 1)
			}
			if h.Hi+1 > h.Hi {
				add(h.Hi + 1)
			}
		}
		// Align with each already-started affine run in each partition.
		for _, oc := range s.occ[node] {
			add(oc.old)
			for j := 0; j < idx; j++ {
				if !assigned[j] {
					continue
				}
				other := order[j]
				for _, oo := range s.occ[other] {
					if oo.part != oc.part {
						continue
					}
					d := int64(codes[j]) - int64(oo.old)
					if c, ok := addInt64(int64(oc.old), d); ok && c >= 0 {
						add(uint64(c))
					}
				}
			}
			// Align current key's occurrence with any fixed code.
			for _, other := range s.n.activeIDs {
				if f := s.n.groups[other].fixed; f != nil {
					for _, of := range s.occ[s.n.activePos[other]] {
						d := int64(*f) - int64(of.old)
						if c, ok := addInt64(int64(oc.old), d); ok && c >= 0 {
							add(uint64(c))
						}
					}
				}
			}
			// Align with a landmark code used by any entry of this partition.
			// This is what lets a run match a future fixed/compact anchor:
			// code(e)=L  =>  code(oc)=oc.old+(L-e.old).
			for _, e := range s.n.parts[oc.part].entries {
				for _, lm := range s.landmarks {
					d := int64(lm) - int64(e.oldCode)
					if c, ok := addInt64(int64(oc.old), d); ok && c >= 0 {
						add(uint64(c))
					}
				}
			}
		}
	}
	out := make([]codeCandidate, 0, len(set))
	for c := range set {
		merge := havePrev && c == prev && mergeOK
		out = append(out, codeCandidate{code: c, merge: merge})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].code != out[j].code {
			return out[i].code < out[j].code
		}
		return !out[i].merge && out[j].merge
	})
	if len(out) > 65536 {
		return nil, fmt.Errorf("%w: more than 65536 candidate codes at key %s", ErrSearchLimit, s.n.groups[gid].canonical)
	}
	return out, nil
}

func (s *searchState) partialRuns(codes []uint64, assigned []bool, order []int) int {
	total := 0
	for _, part := range s.n.parts {
		var prevDelta int64
		have := false
		runs := 0
		for _, e := range part.entries {
			pos := s.n.activePos[e.group]
			idx := -1
			for i, node := range order {
				if node == pos {
					idx = i
					break
				}
			}
			if idx < 0 || !assigned[idx] {
				continue
			}
			d := int64(codes[idx]) - int64(e.oldCode)
			if !have || d != prevDelta {
				runs++
				prevDelta = d
				have = true
			}
		}
		total += runs
	}
	return total
}

func (s *searchState) entryVector(codes []uint64, order []int) []uint64 {
	pos := make([]int, len(order))
	for i, node := range order {
		pos[node] = i
	}
	var v []uint64
	for _, part := range s.n.parts {
		for _, e := range part.entries {
			v = append(v, codes[pos[s.n.activePos[e.group]]])
		}
	}
	return v
}

func (s *searchState) pageStats(codes []uint64, order []int) (int, int, error) {
	codeOf := make(map[int]uint64, len(order))
	for i, node := range order {
		codeOf[s.n.activeIDs[node]] = codes[i]
	}
	total, exc := 0, 0
	for _, part := range s.n.parts {
		pages, err := computePages(s.n, part, codeOf)
		if err != nil {
			return 0, 0, err
		}
		total += len(pages)
		if len(pages) > 0 {
			exc += len(pages) - 1
		}
	}
	return total, exc, nil
}

func betterCandidate(a, b *candidate) bool {
	if b == nil {
		return true
	}
	if a.exception != b.exception {
		return a.exception < b.exception
	}
	if a.used != b.used {
		return a.used < b.used
	}
	if a.pages != b.pages {
		return a.pages < b.pages
	}
	for i := 0; i < len(a.entryCodes) && i < len(b.entryCodes); i++ {
		if a.entryCodes[i] != b.entryCodes[i] {
			return a.entryCodes[i] < b.entryCodes[i]
		}
	}
	return len(a.entryCodes) < len(b.entryCodes)
}

func (s *searchState) buildLandmarks() []uint64 {
	set := map[uint64]bool{0: true}
	for _, f := range s.n.fixed {
		set[f.Code] = true
		if f.Code > 0 {
			set[f.Code-1] = true
			if c, ok := s.prevAllowedAny(f.Code - 1); ok {
				set[c] = true
			}
		}
		if f.Code+1 > f.Code && f.Code+1 < s.n.globalLimit {
			set[f.Code+1] = true
			if c, ok := s.nextAllowedAny(f.Code + 1); ok {
				set[c] = true
			}
		}
	}
	for _, h := range s.n.holes {
		if h.Lo > 0 {
			set[h.Lo-1] = true
			if c, ok := s.prevAllowedAny(h.Lo - 1); ok {
				set[c] = true
			}
		}
		if h.Hi+1 > h.Hi && h.Hi+1 < s.n.globalLimit {
			set[h.Hi+1] = true
			if c, ok := s.nextAllowedAny(h.Hi + 1); ok {
				set[c] = true
			}
		}
	}
	if c, ok := s.nextAllowedAny(0); ok {
		set[c] = true
	}
	out := make([]uint64, 0, len(set))
	for c := range set {
		if c < s.n.globalLimit {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *searchState) nextAllowedAny(at uint64) (uint64, bool) {
	limit := s.n.globalLimit
	x := at
	for x < limit {
		if h, ok := containingHole(s.n.holes, x); ok {
			if h.Hi >= limit-1 {
				return 0, false
			}
			x = h.Hi + 1
			continue
		}
		reserved := false
		for _, f := range s.n.fixed {
			if f.Code == x {
				reserved = true
				break
			}
		}
		if !reserved {
			return x, true
		}
		x++
	}
	return 0, false
}

func (s *searchState) noFeasibleCertificate() error {
	cert := Certificate{
		Kind:        "no-feasible-mapping",
		Message:     "no assignment fits the global code space while preserving every listed query",
		Requirement: fmt.Sprintf("the code space [0,%d) has too few admissible codes for the constrained keys, or a fixed/hole boundary blocks the required order", s.n.globalLimit),
	}
	seen := map[[2]int]bool{}
	for a := 0; a < s.dag.n && len(cert.Observations) < 2; a++ {
		for _, b := range s.dag.adj[a] {
			if seen[[2]int{a, b}] {
				continue
			}
			seen[[2]int{a, b}] = true
			if r, ok := s.dag.reasons[[2]int{a, b}]; ok {
				cert.Observations = append(cert.Observations, r.observation())
				if len(cert.Observations) == 2 {
					break
				}
			}
		}
	}
	if len(cert.Observations) < 2 {
		cert.Observations = append(cert.Observations, s.cutObservations()...)
		if len(cert.Observations) > 2 {
			cert.Observations = cert.Observations[:2]
		}
	}
	for _, f := range s.n.fixed {
		cert.Fixed = append(cert.Fixed, f)
	}
	if len(cert.Observations) == 0 {
		if len(cert.Fixed) >= 2 {
			cert.Kind = "fixed-code-space-conflict"
		} else {
			cert.Observations = append(cert.Observations,
				Observation{Kind: "code-space", Detail: fmt.Sprintf("global space [0,%d)", s.n.globalLimit)},
				Observation{Kind: "code-space", Detail: fmt.Sprintf("%d active keys require distinct codes or holes block them", len(s.n.activeIDs))},
			)
		}
	}
	if len(cert.Observations) == 1 {
		cert.Observations = append(cert.Observations, Observation{Kind: "code-space", Detail: fmt.Sprintf("global space [0,%d) could not accommodate the remaining order constraints", s.n.globalLimit)})
	}
	return &IncompatibleError{Cert: cert}
}

func (s *searchState) cutObservations() []Observation {
	var out []Observation
	for _, part := range s.n.parts {
		for _, c := range part.cuts {
			var left, right *entryInfo
			for i := range part.entries {
				e := &part.entries[i]
				before := e.oldCode < c.OldCode
				if c.Bound == LE {
					before = e.oldCode <= c.OldCode
				}
				if before && left == nil {
					left = e
				}
				if !before && right == nil {
					right = e
				}
			}
			if left != nil && right != nil {
				out = append(out, Observation{
					Kind:         "range-scan",
					Partition:    part.name,
					Cut:          c.Name,
					OldCode:      c.OldCode,
					Bound:        c.Bound,
					LeftKey:      left.key,
					RightKey:     right.key,
					LeftOldCode:  left.oldCode,
					RightOldCode: right.oldCode,
				})
				if len(out) == 2 {
					return out
				}
			}
		}
	}
	return out
}

func (s *searchState) prevAllowedAny(at uint64) (uint64, bool) {
	if at >= s.n.globalLimit {
		at = s.n.globalLimit - 1
	}
	for {
		if h, ok := containingHole(s.n.holes, at); ok {
			if h.Lo == 0 {
				return 0, false
			}
			at = h.Lo - 1
			continue
		}
		reserved := false
		for _, f := range s.n.fixed {
			if f.Code == at {
				reserved = true
				break
			}
		}
		if !reserved {
			return at, true
		}
		if at == 0 {
			return 0, false
		}
		at--
	}
}
