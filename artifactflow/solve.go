package artifactflow

import "sort"

// searchState is the mutable state of one backtracking path.
type searchState struct {
	consumed map[int]bool // receipt used on a consumed (single-use) edge
	produced map[int]int  // produced receipt -> realization index producing it
	realized map[int]bool // selected realization indices
	byStep   map[int]int  // step ordinal -> selected realization index
	roots    map[int]bool // receipts used as root artifacts
}

func newSearchState() *searchState {
	return &searchState{
		consumed: map[int]bool{},
		produced: map[int]int{},
		realized: map[int]bool{},
		byStep:   map[int]int{},
		roots:    map[int]bool{},
	}
}

type solver struct {
	en        *enumeration
	state     *searchState
	budget    int64
	initial   int64
	noGood    map[memoKey]bool
	first     *Assignment
	sources   map[Path][]SourceID
	ambiguity *Ambiguity
	stepOrder map[string]int
}

type memoKey struct {
	receipt  int
	consumed string
	produced string
	realized string
	roots    string
}

// run exhaustively searches every globally consistent correspondence.
func (s *solved) run(en *enumeration) (status Status, cert *Certificate, amb *Ambiguity, reason string, err error) {
	stepOrder := make(map[string]int, len(s.steps))
	for i, st := range s.steps {
		stepOrder[st.step.ID] = i
	}
	sv := &solver{
		en:        en,
		state:     newSearchState(),
		budget:    s.budget,
		initial:   s.budget,
		noGood:    make(map[memoKey]bool),
		sources:   make(map[Path][]SourceID),
		stepOrder: stepOrder,
	}
	defer func() {
		if rec := recover(); rec != nil {
			if bp, ok := rec.(*budgetPanic); ok {
				err = bp.err
				return
			}
			panic(rec)
		}
	}()
	sv.solveManifest(0)
	if sv.ambiguity != nil {
		return StatusAmbiguous, nil, sv.ambiguity, "", nil
	}
	if sv.first != nil {
		return StatusOK, buildCertificate(s, en, sv.first), nil, "", nil
	}
	return StatusInfeasible, nil, nil,
		"no globally consistent receipt/artifact correspondence exists for the manifest", nil
}

// solveManifest resolves manifest entry mi and every entry after it.
func (sv *solver) solveManifest(mi int) bool {
	if sv.ambiguity != nil {
		return true
	}
	if mi == len(sv.en.indexed.manifest) {
		return sv.recordSolution()
	}
	sv.budget--
	if sv.budget <= 0 {
		panic(&budgetPanic{err: &BudgetExceededError{Budget: sv.initial}})
	}
	entry := sv.en.indexed.manifest[mi]
	for _, recIdx := range sv.receiptsFor(entry.path, entry.entry.Digest) {
		next := func() bool { return sv.solveManifest(mi + 1) }
		if sv.solveReceipt(recIdx, next) {
			return true
		}
	}
	return false
}

// solveReceipt makes one required receipt available, either as a root
// artifact or as the output of a selected step realization. On success it
// runs continuation; a false continuation causes a full backtrack.
func (sv *solver) solveReceipt(recIdx int, continuation func() bool) bool {
	if sv.ambiguity != nil {
		return true
	}
	key := sv.memoKey(recIdx)
	if sv.noGood[key] {
		return false
	}
	// Root branch: a root-capable receipt may be supplied directly.
	if _, ok := sv.en.rootByID[recIdx]; ok {
		sv.state.roots[recIdx] = true
		if continuation() {
			return true
		}
		delete(sv.state.roots, recIdx)
		if sv.ambiguity != nil {
			return true
		}
	}
	// Produced branch: choose a realization that emits this receipt.
	for _, realIdx := range sv.en.producer[recIdx] {
		applied, consumed := sv.tryProducer(realIdx)
		if !applied {
			continue
		}
		if continuation() {
			return true
		}
		sv.undoApplication(realIdx, consumed)
		if sv.ambiguity != nil {
			return true
		}
	}
	sv.noGood[key] = true
	return false
}

// tryProducer applies a realization when compatible with current state and
// recursively resolves every input it needs. The second return value lists
// receipts newly marked as consumed, for exact rollback.
func (sv *solver) tryProducer(realIdx int) (bool, []int) {
	r := sv.en.all[realIdx]
	ord := sv.stepOrder[r.stepID]
	if existing, ok := sv.state.byStep[ord]; ok {
		// One build instance realizes at most one candidate per step.
		return existing == realIdx, nil
	}
	if sv.applicationConflict(r) {
		return false, nil
	}
	sv.state.realized[realIdx] = true
	sv.state.byStep[ord] = realIdx
	for _, recIdx := range r.outputs {
		sv.state.produced[recIdx] = realIdx
	}
	var newlyConsumed []int
	for _, slot := range sv.inputNames(realIdx) {
		recIdx := r.inputs[slot]
		if sv.slotConsumed(realIdx, slot) {
			if sv.state.consumed[recIdx] {
				sv.undoApplication(realIdx, newlyConsumed)
				return false, nil
			}
			sv.state.consumed[recIdx] = true
			newlyConsumed = append(newlyConsumed, recIdx)
		}
		ok := false
		sv.solveReceipt(recIdx, func() bool { ok = true; return true })
		if !ok {
			sv.undoApplication(realIdx, newlyConsumed)
			return false, nil
		}
	}
	return true, newlyConsumed
}

func (sv *solver) undoApplication(realIdx int, newlyConsumed []int) {
	r := sv.en.all[realIdx]
	if _, ok := sv.state.realized[realIdx]; !ok {
		return
	}
	delete(sv.state.realized, realIdx)
	delete(sv.state.byStep, sv.stepOrder[r.stepID])
	for _, recIdx := range r.outputs {
		delete(sv.state.produced, recIdx)
	}
	for _, recIdx := range newlyConsumed {
		delete(sv.state.consumed, recIdx)
	}
}

// applicationConflict detects two distinct realizations producing the same
// receipt on one build path, which would fork a receipt's provenance.
func (sv *solver) applicationConflict(r realization) bool {
	for _, recIdx := range r.outputs {
		if existing, ok := sv.state.produced[recIdx]; ok {
			if sv.en.all[existing].dedupKey != r.dedupKey {
				return true
			}
		}
	}
	return false
}

func (sv *solver) inputNames(realIdx int) []string {
	r := sv.en.all[realIdx]
	names := make([]string, 0, len(r.inputs))
	for name := range r.inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (sv *solver) slotConsumed(realIdx int, slot string) bool {
	r := sv.en.all[realIdx]
	for _, st := range sv.en.indexed.steps {
		if st.step.ID != r.stepID {
			continue
		}
		for _, ci := range st.candidates {
			if ci.candidate.Name != r.candidate {
				continue
			}
			for _, si := range ci.inputs {
				if si.slot.Name == slot {
					return si.slot.Consumed
				}
			}
		}
	}
	return false
}

func (sv *solver) receiptsFor(path Path, d Digest) []int {
	nd := normalizeDigest(d)
	var out []int
	for i, ir := range sv.en.indexed.receipts {
		if ir.path == path && ir.normDigest == nd {
			out = append(out, i)
		}
	}
	return out
}

// recordSolution is called when all manifest entries are resolved. It keeps
// the first full correspondence and compares every later one against it on
// the root-source attribution of each sensitive manifest entry.
func (sv *solver) recordSolution() bool {
	assignment := sv.snapshot()
	if sv.first == nil {
		sv.first = &assignment
		for _, mi := range sv.en.indexed.manifest {
			if mi.entry.Sensitive {
				sv.sources[mi.path] = sv.rootSources(mi)
			}
		}
		return false // keep enumerating other correspondences
	}
	for _, mi := range sv.en.indexed.manifest {
		if !mi.entry.Sensitive {
			continue
		}
		got := sv.rootSources(mi)
		if !sourceSetsEqual(got, sv.sources[mi.path]) {
			sv.ambiguity = &Ambiguity{
				ManifestPath:  mi.path,
				Digest:        mi.entry.Digest,
				FirstSources:  sv.sources[mi.path],
				SecondSources: got,
				First:         *sv.first,
				Second:        assignment,
			}
			return true
		}
	}
	return false
}

// rootSources computes the root sources transitively reachable from one
// manifest entry through the selected derivation edges.
func (sv *solver) rootSources(mi manifestIndex) []SourceID {
	visited := map[int]bool{}
	set := map[SourceID]bool{}
	var walk func(recIdx int)
	walk = func(recIdx int) {
		if visited[recIdx] {
			return
		}
		visited[recIdx] = true
		receipt := sv.en.indexed.receipts[recIdx].receipt
		if receipt.IsRoot() {
			set[receipt.RootSource] = true
		}
		if realIdx, ok := sv.state.produced[recIdx]; ok {
			for _, inRec := range sv.en.all[realIdx].inputs {
				walk(inRec)
			}
		}
	}
	for _, recIdx := range sv.receiptsFor(mi.path, mi.entry.Digest) {
		if sv.state.roots[recIdx] {
			walk(recIdx)
		}
		if _, produced := sv.state.produced[recIdx]; produced {
			walk(recIdx)
		}
	}
	return sortedSourceIDs(set)
}

func sourceSetsEqual(a, b []SourceID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// snapshot materializes the current state into a portable Assignment.
func (sv *solver) snapshot() Assignment {
	realIDs := make([]int, 0, len(sv.state.realized))
	for id := range sv.state.realized {
		realIDs = append(realIDs, id)
	}
	sort.Ints(realIDs)
	steps := make([]RealizedStep, 0, len(realIDs))
	for _, id := range realIDs {
		r := sv.en.all[id]
		steps = append(steps, RealizedStep{
			StepID:         r.stepID,
			CandidateName:  r.candidate,
			InputReceipts:  sv.bindings(r.inputs),
			OutputReceipts: sv.bindings(r.outputs),
		})
	}
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].StepID != steps[j].StepID {
			return sv.stepOrder[steps[i].StepID] < sv.stepOrder[steps[j].StepID]
		}
		return steps[i].CandidateName < steps[j].CandidateName
	})
	rootIDs := make([]int, 0, len(sv.state.roots))
	for id := range sv.state.roots {
		rootIDs = append(rootIDs, id)
	}
	sort.Ints(rootIDs)
	roots := make([]string, 0, len(rootIDs))
	for _, id := range rootIDs {
		roots = append(roots, sv.en.indexed.receipts[id].receipt.ID)
	}
	return Assignment{Steps: steps, Roots: roots}
}

func (sv *solver) bindings(slots map[string]int) []SlotBinding {
	names := make([]string, 0, len(slots))
	for name := range slots {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SlotBinding, 0, len(names))
	for _, name := range names {
		out = append(out, SlotBinding{
			Slot:      name,
			ReceiptID: sv.en.indexed.receipts[slots[name]].receipt.ID,
		})
	}
	return out
}

func (sv *solver) memoKey(recIdx int) memoKey {
	return memoKey{
		receipt:  recIdx,
		consumed: intSetKey(sv.state.consumed),
		produced: intMapKey(sv.state.produced),
		realized: intSetKey(sv.state.realized),
		roots:    intSetKey(sv.state.roots),
	}
}

func intSetKey(m map[int]bool) string {
	ids := make([]int, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Ints(ids)
	buf := make([]byte, 0, len(ids)*3)
	for _, id := range ids {
		buf = append(buf, byte('a'+id/26), byte('a'+id%26), ',')
	}
	return string(buf)
}

func intMapKey(m map[int]int) string {
	ids := make([]int, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Ints(ids)
	buf := make([]byte, 0, len(ids)*5)
	for _, id := range ids {
		buf = append(buf, byte('a'+id/26), byte('a'+id%26), '=')
		buf = append(buf, byte('a'+m[id]/26), byte('a'+m[id]%26), ',')
	}
	return string(buf)
}
