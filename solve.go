package artifactflow

import (
	"fmt"
	"sort"
	"strings"
)

type instance struct {
	key      instKey
	artifact artifactKey
	prov     provenance
}

type edgeRecord struct {
	consumer   *receiptInfo
	inputIndex int
	source     instKey
	rule       string
}

type searchState struct {
	selected      map[string]*receiptInfo
	instances     map[instKey]*instance
	consumed      map[instKey]int
	edges         []edgeRecord
	finalSources  []instKey
	finalAssigned []bool
}

func newState(finalCount int) *searchState {
	return &searchState{
		selected:      make(map[string]*receiptInfo),
		instances:     make(map[instKey]*instance),
		consumed:      make(map[instKey]int),
		finalSources:  make([]instKey, finalCount),
		finalAssigned: make([]bool, finalCount),
	}
}

func (s *searchState) clone() *searchState {
	out := &searchState{
		selected:      make(map[string]*receiptInfo, len(s.selected)),
		instances:     make(map[instKey]*instance, len(s.instances)),
		consumed:      make(map[instKey]int, len(s.consumed)),
		edges:         append([]edgeRecord(nil), s.edges...),
		finalSources:  append([]instKey(nil), s.finalSources...),
		finalAssigned: append([]bool(nil), s.finalAssigned...),
	}
	for k, v := range s.selected {
		out.selected[k] = v
	}
	for k, v := range s.instances {
		out.instances[k] = v
	}
	for k, v := range s.consumed {
		out.consumed[k] = v
	}
	return out
}

func (s *searchState) ensureRoot(ri *rootInfo) *instance {
	if inst, ok := s.instances[ri.inst]; ok {
		return inst
	}
	p := newProvenance()
	p.roots[ri.root.ID] = struct{}{}
	p.signers[ri.root.Signer] = struct{}{}
	inst := &instance{
		key:      ri.inst,
		artifact: ri.key,
		prov:     p,
	}
	s.instances[ri.inst] = inst
	return inst
}

func (s *searchState) canUse(key instKey, mode Consumption) bool {
	return mode != Consume || s.consumed[key] == 0
}

func (s *searchState) addEdge(rc *receiptInfo, inputIndex int, source instKey, rule string) {
	s.edges = append(s.edges, edgeRecord{
		consumer:   rc,
		inputIndex: inputIndex,
		source:     source,
		rule:       rule,
	})
	if rc.inputs[inputIndex].Consumption == Consume {
		s.consumed[source]++
	}
}

type sourceOption struct {
	kind        int // 0 existing, 1 root, 2 candidate
	existing    *instance
	root        *rootInfo
	receipt     *receiptInfo
	outputIndex int
	rule        string
	sortKey     string
}

const (
	optionExisting = iota
	optionRoot
	optionCandidate
)

type engine struct {
	spec      *compiledSpec
	solutions []solution
	byTuple   map[string]int
	err       error
}

type solution struct {
	tuple string
	state *searchState
	cert  *Certificate
}

func newEngine(spec *compiledSpec) *engine {
	return &engine{
		spec:    spec,
		byTuple: make(map[string]int),
	}
}

// Verify checks whether the receipt set admits a globally consistent build
// and whether every feasible build gives the same root provenance to each
// sensitive final artifact. A malformed set returns an error; a well-formed
// but unbuildable or ambiguous set returns a Result with the corresponding
// Status.
func Verify(set *ReceiptSet) (*Result, error) {
	spec, err := compileSpec(set)
	if err != nil {
		return nil, err
	}
	e := newEngine(spec)
	e.searchFinals(newState(len(spec.finals)))
	if e.err != nil {
		return nil, e.err
	}
	switch len(e.solutions) {
	case 0:
		return &Result{
			Status: StatusRejected,
			Problems: []Problem{{
				Code:    ProblemNoConsistentBuild,
				Message: "no globally consistent selection of signed receipts and artifact correspondences exists",
			}},
		}, nil
	case 1:
		cert := e.solutions[0].cert
		if err := VerifyCertificate(set, cert); err != nil {
			return nil, fmt.Errorf("artifactflow: internal certificate self-check failed: %w", err)
		}
		return &Result{
			Status:      StatusVerified,
			Certificate: cert,
			RootSets:    rootSetsFromCertificate(cert),
		}, nil
	default:
		result := e.ambiguityResult()
		if result.Ambiguity != nil {
			if err := VerifyCertificate(set, result.Ambiguity.First.Certificate); err != nil {
				return nil, fmt.Errorf("artifactflow: internal ambiguity witness self-check failed: %w", err)
			}
			if err := VerifyCertificate(set, result.Ambiguity.Second.Certificate); err != nil {
				return nil, fmt.Errorf("artifactflow: internal ambiguity witness self-check failed: %w", err)
			}
		}
		return result, nil
	}
}

// VerifyReceipts is a convenience wrapper around Verify.
func VerifyReceipts(roots []RootArtifact, receipts []Receipt, finals []FinalArtifact) (*Result, error) {
	return Verify(&ReceiptSet{Roots: roots, Receipts: receipts, Finals: finals})
}

func (e *engine) searchFinals(st *searchState) bool {
	index := -1
	for i := range e.spec.finals {
		if !st.finalAssigned[i] {
			index = i
			break
		}
	}
	if index < 0 {
		return e.recordSolution(st)
	}
	return e.satisfyFinal(st, index, e.searchFinals)
}

func (e *engine) satisfyFinal(st *searchState, index int, k func(*searchState) bool) bool {
	final := e.spec.finals[index]
	options := e.finalOptions(st, final)
	for _, opt := range options {
		switch opt.kind {
		case optionExisting:
			st2 := st.clone()
			st2.finalSources[index] = opt.existing.key
			st2.finalAssigned[index] = true
			if k(st2) {
				return true
			}
		case optionRoot:
			st2 := st.clone()
			inst := st2.ensureRoot(opt.root)
			st2.finalSources[index] = inst.key
			st2.finalAssigned[index] = true
			if k(st2) {
				return true
			}
		case optionCandidate:
			if e.selectReceipt(st, opt.receipt, func(st2 *searchState) bool {
				key := outputInstanceKey(opt.receipt.key, opt.outputIndex)
				inst, ok := st2.instances[key]
				if !ok {
					return false
				}
				st2.finalSources[index] = inst.key
				st2.finalAssigned[index] = true
				return k(st2)
			}) {
				return true
			}
		}
	}
	return false
}

func (e *engine) finalOptions(st *searchState, final *finalInfo) []sourceOption {
	var options []sourceOption
	seenInstances := make(map[instKey]struct{})
	seenCandidates := make(map[string]struct{})

	for _, inst := range st.instances {
		if inst.artifact != final.key {
			continue
		}
		if _, dup := seenInstances[inst.key]; dup {
			continue
		}
		seenInstances[inst.key] = struct{}{}
		options = append(options, sourceOption{
			kind:     optionExisting,
			existing: inst,
			sortKey:  "0:" + inst.key.String(),
		})
	}
	for _, ri := range e.spec.roots {
		if ri.key != final.key || !e.spec.signerAllowed(ri.root.Signer) {
			continue
		}
		if _, used := st.instances[ri.inst]; used {
			continue
		}
		options = append(options, sourceOption{
			kind:    optionRoot,
			root:    ri,
			sortKey: "1:" + ri.root.ID,
		})
	}
	for _, step := range e.spec.steps {
		if _, selected := st.selected[step.id]; selected {
			continue
		}
		seenSemantic := make(map[string]struct{})
		for _, rc := range step.alternatives {
			if !e.spec.signerAllowed(rc.receipt.Signer) {
				continue
			}
			if _, dup := seenSemantic[rc.semKey]; dup {
				continue
			}
			seenSemantic[rc.semKey] = struct{}{}
			for idx, out := range rc.outputs {
				key := artifactKey{tenant: rc.tenant, path: out.Path, digest: out.Digest}
				if key != final.key {
					continue
				}
				dedupe := fmt.Sprintf("%d:%s:%d", len(rc.key), rc.key, idx)
				if _, dup := seenCandidates[dedupe]; dup {
					continue
				}
				seenCandidates[dedupe] = struct{}{}
				options = append(options, sourceOption{
					kind:        optionCandidate,
					receipt:     rc,
					outputIndex: idx,
					sortKey:     "2:" + rc.key + ":" + fmt.Sprint(idx),
				})
			}
		}
	}
	sort.Slice(options, func(i, j int) bool { return options[i].sortKey < options[j].sortKey })
	return options
}

func (e *engine) selectReceipt(st *searchState, rc *receiptInfo, k func(*searchState) bool) bool {
	if _, selected := st.selected[rc.receipt.Step]; selected {
		return false
	}
	if !e.spec.signerAllowed(rc.receipt.Signer) {
		return false
	}
	st2 := st.clone()
	st2.selected[rc.receipt.Step] = rc
	return e.satisfyInputs(st2, rc, 0, func(st3 *searchState) bool {
		e.produce(st3, rc)
		return k(st3)
	})
}

func (e *engine) satisfyInputs(st *searchState, rc *receiptInfo, index int, k func(*searchState) bool) bool {
	if index == len(rc.inputs) {
		return k(st)
	}
	return e.satisfyInput(st, rc, index, func(st2 *searchState) bool {
		return e.satisfyInputs(st2, rc, index+1, k)
	})
}

func (e *engine) satisfyInput(st *searchState, rc *receiptInfo, index int, k func(*searchState) bool) bool {
	input := rc.inputs[index]
	options := e.inputOptions(st, rc, index)
	for _, opt := range options {
		switch opt.kind {
		case optionExisting:
			if !st.canUse(opt.existing.key, input.Consumption) {
				continue
			}
			st2 := st.clone()
			st2.addEdge(rc, index, opt.existing.key, opt.rule)
			if k(st2) {
				return true
			}
		case optionRoot:
			st2 := st.clone()
			inst := st2.ensureRoot(opt.root)
			if !st2.canUse(inst.key, input.Consumption) {
				continue
			}
			st2.addEdge(rc, index, inst.key, opt.rule)
			if k(st2) {
				return true
			}
		case optionCandidate:
			if e.selectReceipt(st, opt.receipt, func(st2 *searchState) bool {
				key := outputInstanceKey(opt.receipt.key, opt.outputIndex)
				if _, ok := st2.instances[key]; !ok {
					return false
				}
				if !st2.canUse(key, input.Consumption) {
					return false
				}
				st2.addEdge(rc, index, key, opt.rule)
				return k(st2)
			}) {
				return true
			}
		}
	}
	return false
}

func (e *engine) inputOptions(st *searchState, rc *receiptInfo, index int) []sourceOption {
	input := rc.inputs[index]
	var options []sourceOption
	seenInstances := make(map[instKey]struct{})
	seenCandidates := make(map[string]struct{})

	for _, inst := range st.instances {
		if inst.artifact.tenant != rc.tenant || inst.artifact.digest != input.Digest {
			continue
		}
		rule, ok := ruleFor(rc, inst.artifact.path, input.Path)
		if !ok {
			continue
		}
		if _, dup := seenInstances[inst.key]; dup {
			continue
		}
		seenInstances[inst.key] = struct{}{}
		options = append(options, sourceOption{
			kind:     optionExisting,
			existing: inst,
			rule:     rule,
			sortKey:  "0:" + inst.key.String(),
		})
	}
	for _, ri := range e.spec.roots {
		if ri.key.tenant != rc.tenant || ri.key.digest != input.Digest {
			continue
		}
		if !e.spec.signerAllowed(ri.root.Signer) {
			continue
		}
		if _, used := st.instances[ri.inst]; used {
			continue
		}
		rule, ok := ruleFor(rc, ri.key.path, input.Path)
		if !ok {
			continue
		}
		options = append(options, sourceOption{
			kind:    optionRoot,
			root:    ri,
			rule:    rule,
			sortKey: "1:" + ri.root.ID,
		})
	}
	for _, step := range e.spec.steps {
		if _, selected := st.selected[step.id]; selected {
			continue
		}
		seenSemantic := make(map[string]struct{})
		for _, candidate := range step.alternatives {
			if !e.spec.signerAllowed(candidate.receipt.Signer) {
				continue
			}
			if _, dup := seenSemantic[candidate.semKey]; dup {
				continue
			}
			seenSemantic[candidate.semKey] = struct{}{}
			for idx, out := range candidate.outputs {
				if candidate.tenant != rc.tenant || out.Digest != input.Digest {
					continue
				}
				rule, ok := ruleFor(rc, out.Path, input.Path)
				if !ok {
					continue
				}
				dedupe := fmt.Sprintf("%d:%s:%d", len(candidate.key), candidate.key, idx)
				if _, dup := seenCandidates[dedupe]; dup {
					continue
				}
				seenCandidates[dedupe] = struct{}{}
				options = append(options, sourceOption{
					kind:        optionCandidate,
					receipt:     candidate,
					outputIndex: idx,
					rule:        rule,
					sortKey:     "2:" + candidate.key + ":" + fmt.Sprint(idx),
				})
			}
		}
	}
	sort.Slice(options, func(i, j int) bool { return options[i].sortKey < options[j].sortKey })
	return options
}

func ruleFor(rc *receiptInfo, src, dst string) (string, bool) {
	if src == dst {
		return "", true
	}
	for i := range rc.rules {
		if rc.rules[i].permits(src, dst) {
			return rc.rules[i].raw, true
		}
	}
	return "", false
}

func (e *engine) produce(st *searchState, rc *receiptInfo) {
	inputProv := make([]provenance, len(rc.inputs))
	if len(rc.inputs) > 0 {
		filled := make([]bool, len(rc.inputs))
		for _, edge := range st.edges {
			if edge.consumer != rc {
				continue
			}
			if inst, ok := st.instances[edge.source]; ok {
				inputProv[edge.inputIndex] = inst.prov
				filled[edge.inputIndex] = true
			}
		}
		for i := range inputProv {
			if !filled[i] {
				inputProv[i] = newProvenance()
			}
		}
	}
	for idx, out := range rc.outputs {
		key := outputInstanceKey(rc.key, idx)
		st.instances[key] = &instance{
			key:      key,
			artifact: artifactKey{tenant: rc.tenant, path: out.Path, digest: out.Digest},
			prov:     outputProvenance(rc, inputProv, idx),
		}
	}
}

func (e *engine) complete(st *searchState) (*searchState, bool) {
	var next *stepInfo
	for _, step := range e.spec.steps {
		if _, selected := st.selected[step.id]; !selected {
			next = step
			break
		}
	}
	if next == nil {
		return st, true
	}
	var found *searchState
	seenSemantic := make(map[string]struct{})
	for _, rc := range next.alternatives {
		if _, dup := seenSemantic[rc.semKey]; dup {
			continue
		}
		seenSemantic[rc.semKey] = struct{}{}
		e.selectReceipt(st, rc, func(st2 *searchState) bool {
			done, ok := e.complete(st2)
			if ok {
				found = done
				return true
			}
			return false
		})
		if found != nil {
			break
		}
	}
	return found, found != nil
}

func (e *engine) recordSolution(st *searchState) bool {
	for i, final := range e.spec.finals {
		if !st.finalAssigned[i] {
			return false
		}
		inst := st.instances[st.finalSources[i]]
		if inst == nil {
			return false
		}
		for role := range final.required {
			if _, ok := inst.prov.signers[role]; !ok {
				return false
			}
		}
	}
	tuple := e.tupleKey(st)
	if _, known := e.byTuple[tuple]; known {
		return len(e.byTuple) >= 2
	}
	done, ok := e.complete(st)
	if !ok {
		return false
	}
	cert, err := buildCertificate(e.spec, done)
	if err != nil {
		e.err = err
		return true
	}
	e.byTuple[tuple] = len(e.solutions)
	e.solutions = append(e.solutions, solution{tuple: tuple, state: done, cert: cert})
	return len(e.solutions) >= 2
}

func (e *engine) tupleKey(st *searchState) string {
	var b strings.Builder
	for i := range e.spec.finals {
		if !e.spec.sensitive(i) {
			continue
		}
		source, assigned := st.finalSources[i], st.finalAssigned[i]
		if !assigned {
			continue
		}
		inst := st.instances[source]
		if inst == nil {
			continue
		}
		roots := inst.prov.rootList()
		fmt.Fprintf(&b, "%d|%d|", i, len(roots))
		for _, root := range roots {
			fmt.Fprintf(&b, "%d:%s", len(root), root)
		}
		b.WriteByte(';')
	}
	return b.String()
}

func (e *engine) ambiguityResult() *Result {
	first := e.solutions[0]
	second := e.solutions[1]
	firstByIndex := finalRecordsByIndex(first.cert)
	secondByIndex := finalRecordsByIndex(second.cert)

	index := -1
	for i := range e.spec.finals {
		if !e.spec.sensitive(i) {
			continue
		}
		a, okA := firstByIndex[i]
		b, okB := secondByIndex[i]
		if !okA || !okB || !equalStrings(a.Roots, b.Roots) {
			index = i
			break
		}
	}
	if index < 0 {
		index = 0
	}
	a, _ := firstByIndex[index]
	b, _ := secondByIndex[index]
	amb := &Ambiguity{
		FinalIndex: index,
		Artifact:   e.spec.finals[index].final,
		First:      attributionFrom(a, first.cert),
		Second:     attributionFrom(b, second.cert),
	}
	return &Result{
		Status:      StatusAmbiguous,
		Ambiguity:   amb,
		Certificate: first.cert,
		RootSets:    rootSetsFromCertificate(first.cert),
	}
}

func finalRecordsByIndex(cert *Certificate) map[int]FinalRecord {
	out := make(map[int]FinalRecord, len(cert.Finals))
	for _, rec := range cert.Finals {
		out[rec.Index] = rec
	}
	return out
}

func attributionFrom(rec FinalRecord, cert *Certificate) Attribution {
	return Attribution{
		Roots:       append([]string(nil), rec.Roots...),
		Receipts:    append([]string(nil), rec.Receipts...),
		Signers:     append([]Role(nil), rec.Signers...),
		Instance:    rec.Instance,
		Certificate: cert,
	}
}

func rootSetsFromCertificate(cert *Certificate) []FinalRoots {
	out := make([]FinalRoots, 0, len(cert.Finals))
	for _, rec := range cert.Finals {
		out = append(out, FinalRoots{Index: rec.Index, Roots: append([]string(nil), rec.Roots...)})
	}
	return out
}
