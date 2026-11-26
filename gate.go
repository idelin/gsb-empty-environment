package admission

import (
	"math/big"
	"sort"
	"sync"
)

// Gate is an atomic release-admission state machine.  It is safe for
// concurrent use.
type Gate struct {
	mu sync.Mutex

	vars     []variableInfo
	byName   map[string]int
	policies []internalPolicy

	baseline  *Batch
	committed []*Batch
	rows      []internalRow
	aux       []internalAux

	eval  *stateEval
	prior *stateEval

	version uint64
	digest  string
	history []Record
	maxEnum uint64
}

// NewGate validates the configuration and constructs a gate.  The optional
// baseline batch is prior knowledge: policy ratios are measured against the
// joint projection cardinality of the baseline feasible set.
func NewGate(cfg Config) (*Gate, error) {
	vars, byName, err := buildVariables(cfg.Variables)
	if err != nil {
		return nil, err
	}
	policies, err := buildPolicies(cfg.Policies, vars, byName)
	if err != nil {
		return nil, err
	}
	maxEnum := cfg.MaxEnumeration
	if maxEnum == 0 {
		maxEnum = defaultMaxEnumeration
	}
	var rows []internalRow
	var aux []internalAux
	if cfg.Baseline != nil {
		pb, err := buildBatch(cfg.Baseline, vars, byName)
		if err != nil {
			return nil, err
		}
		rows = append(rows, pb.rows...)
		aux = append(aux, pb.aux...)
	}
	eval, err := evaluateState(rows, aux, vars, policies, maxEnum)
	if err != nil {
		return nil, err
	}
	if !eval.consistent {
		return nil, admissionError(CodeInconsistent, "baseline", "", "baseline constraints have no solution")
	}
	g := &Gate{
		vars:     vars,
		byName:   byName,
		policies: policies,
		baseline: cfg.Baseline.clone(),
		rows:     rows,
		aux:      aux,
		eval:     eval,
		prior:    eval,
		maxEnum:  maxEnum,
	}
	g.digest = g.computeDigest()
	return g, nil
}

// New is an alias for NewGate.
func New(cfg Config) (*Gate, error) { return NewGate(cfg) }

// State is a point-in-time view of a gate.  All counters are exact.
type State struct {
	Version              uint64   `json:"version"`
	Digest               string   `json:"digest"`
	Variables            int      `json:"variables"`
	LinearConstraints    int      `json:"linear_constraints"`
	AuxiliaryConstraints int      `json:"auxiliary_constraints"`
	PriorFeasibleCount   *big.Int `json:"prior_feasible_count"`
	FeasibleCount        *big.Int `json:"feasible_count"`
	AdmittedBatches      []string `json:"admitted_batches"`
}

// State returns a consistent snapshot of the committed state.
func (g *Gate) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]string, len(g.committed))
	for i, b := range g.committed {
		ids[i] = b.ID
	}
	return State{
		Version:              g.version,
		Digest:               g.digest,
		Variables:            len(g.vars),
		LinearConstraints:    len(g.rows),
		AuxiliaryConstraints: len(g.aux),
		PriorFeasibleCount:   cloneInt(g.prior.feasible),
		FeasibleCount:        cloneInt(g.eval.feasible),
		AdmittedBatches:      ids,
	}
}

// Record is one committed history entry.
type Record struct {
	Sequence      uint64         `json:"sequence"`
	BatchID       string         `json:"batch_id"`
	FeasibleCount *big.Int       `json:"feasible_count"`
	StateDigest   string         `json:"state_digest"`
	Policies      []PolicyReport `json:"policies,omitempty"`
}

// History returns a copy of the committed admission history.
func (g *Gate) History() []Record {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Record, len(g.history))
	for i, r := range g.history {
		out[i] = r
		out[i].FeasibleCount = cloneInt(r.FeasibleCount)
		out[i].Policies = clonePolicyReports(r.Policies)
	}
	return out
}

// PolicyReport records the exact joint-projection counts for one policy.
type PolicyReport struct {
	Name             string                `json:"name"`
	Method           string                `json:"method"`
	Prior            *big.Int              `json:"prior_joint_cardinality"`
	Previous         *big.Int              `json:"previous_joint_cardinality"`
	Post             *big.Int              `json:"post_joint_cardinality"`
	CumulativeShrink *big.Rat              `json:"cumulative_shrink"`
	BatchShrink      *big.Rat              `json:"batch_shrink"`
	MaxShrink        *big.Rat              `json:"max_shrink"`
	Allowed          bool                  `json:"allowed"`
	Projection       ProjectionCertificate `json:"projection_certificate"`
}

// Report is returned for every admission attempt, including rejected ones
// when enough evaluation was possible.
type Report struct {
	BatchID                string         `json:"batch_id"`
	Admitted               bool           `json:"admitted"`
	Committed              bool           `json:"committed"`
	Version                uint64         `json:"version"`
	PriorFeasibleCount     *big.Int       `json:"prior_feasible_count"`
	PreviousFeasibleCount  *big.Int       `json:"previous_feasible_count"`
	CandidateFeasibleCount *big.Int       `json:"candidate_feasible_count"`
	CommittedFeasibleCount *big.Int       `json:"committed_feasible_count"`
	Policies               []PolicyReport `json:"policies,omitempty"`
	Certificate            *Certificate   `json:"certificate,omitempty"`
	StateDigest            string         `json:"state_digest"`
	Message                string         `json:"message,omitempty"`
}

func clonePolicyReports(in []PolicyReport) []PolicyReport {
	out := make([]PolicyReport, len(in))
	for i, p := range in {
		out[i] = p
		out[i].Prior = cloneInt(p.Prior)
		out[i].Previous = cloneInt(p.Previous)
		out[i].Post = cloneInt(p.Post)
		if p.CumulativeShrink != nil {
			out[i].CumulativeShrink = new(big.Rat).Set(p.CumulativeShrink)
		}
		if p.BatchShrink != nil {
			out[i].BatchShrink = new(big.Rat).Set(p.BatchShrink)
		}
		if p.MaxShrink != nil {
			out[i].MaxShrink = new(big.Rat).Set(p.MaxShrink)
		}
		out[i].Projection = cloneProjectionCertificate(p.Projection)
	}
	return out
}

func cloneProjectionCertificate(in ProjectionCertificate) ProjectionCertificate {
	out := in
	out.Forms = make([]FormCertificate, len(in.Forms))
	for i, f := range in.Forms {
		out.Forms[i] = f
		out.Forms[i].Terms = append([]canonicalTerm(nil), f.Terms...)
	}
	out.NumeratorPrimes = clonePrimeCountCertificates(in.NumeratorPrimes)
	out.DenominatorPrimes = clonePrimeCountCertificates(in.DenominatorPrimes)
	return out
}

func clonePrimeCountCertificates(in []PrimeCountCertificate) []PrimeCountCertificate {
	out := make([]PrimeCountCertificate, len(in))
	for i, p := range in {
		out[i] = p
		out[i].InvariantFactors = append([]string(nil), p.InvariantFactors...)
	}
	return out
}

// Admit evaluates a batch and, if every constraint is consistent and every
// collusion policy remains within its cumulative bound, commits it.  On any
// error the committed state, digest, and history are untouched.
func (g *Gate) Admit(b *Batch) (*Report, error) {
	return g.admit(b, true)
}

// Preview evaluates a batch without committing it.
func (g *Gate) Preview(b *Batch) (*Report, error) {
	return g.admit(b, false)
}

func (g *Gate) admit(b *Batch, commit bool) (*Report, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if b == nil || b.empty() {
		return g.baseReport(b), admissionError(CodeInvalidInput, "admit", "", "batch is empty")
	}
	pb, err := buildBatch(b, g.vars, g.byName)
	if err != nil {
		return g.baseReport(b), err
	}
	candRows := append(append([]internalRow{}, g.rows...), pb.rows...)
	candAux := append(append([]internalAux{}, g.aux...), pb.aux...)
	candEval, err := evaluateState(candRows, candAux, g.vars, g.policies, g.maxEnum)
	if err != nil {
		rep := g.baseReport(b)
		rep.Message = err.Error()
		return rep, err
	}
	rep := g.baseReport(b)
	rep.CandidateFeasibleCount = cloneInt(candEval.feasible)
	if !candEval.consistent {
		rep.Message = "candidate batch is inconsistent with the committed state"
		return rep, admissionError(CodeInconsistent, "admit", "", "%s", rep.Message)
	}

	policyReports, violation := g.policyReports(candEval)
	rep.Policies = policyReports
	rep.Certificate = buildCertificate(g.vars, g.policies, g.prior, g.eval, candEval, candRows, candAux)
	if violation != nil {
		setCertificateState(rep.Certificate, g.digest)
		rep.Message = violation.Error()
		return rep, violation
	}

	rep.Admitted = true
	if !commit {
		rep.Committed = false
		setCertificateState(rep.Certificate, g.digest)
		return rep, nil
	}
	g.rows = candRows
	g.aux = candAux
	g.eval = candEval
	g.committed = append(g.committed, b.clone())
	g.version++
	g.digest = g.computeDigest()
	rep.Committed = true
	rep.Version = g.version
	rep.StateDigest = g.digest
	rep.CommittedFeasibleCount = cloneInt(candEval.feasible)
	rec := Record{
		Sequence:      g.version,
		BatchID:       b.ID,
		FeasibleCount: cloneInt(candEval.feasible),
		StateDigest:   g.digest,
		Policies:      clonePolicyReports(policyReports),
	}
	g.history = append(g.history, rec)
	if rep.Certificate != nil {
		setCertificateState(rep.Certificate, g.digest)
	}
	return rep, nil
}

func setCertificateState(c *Certificate, stateDigest string) {
	if c == nil {
		return
	}
	c.StateDigest = stateDigest
	c.CanonicalSHA256 = c.computeDigest()
}

func (g *Gate) baseReport(b *Batch) *Report {
	rep := &Report{
		PriorFeasibleCount:     cloneInt(g.prior.feasible),
		PreviousFeasibleCount:  cloneInt(g.eval.feasible),
		CommittedFeasibleCount: cloneInt(g.eval.feasible),
		Version:                g.version,
		StateDigest:            g.digest,
	}
	if b != nil {
		rep.BatchID = b.ID
	}
	return rep
}

func (g *Gate) policyReports(post *stateEval) ([]PolicyReport, error) {
	out := make([]PolicyReport, len(g.policies))
	var first *AdmissionError
	for i, p := range g.policies {
		priorCard := g.prior.projections[i]
		prevCard := g.eval.projections[i]
		postCard := post.projections[i]
		cum := new(big.Rat)
		if postCard.Sign() > 0 {
			cum.SetFrac(priorCard, postCard)
		}
		batch := new(big.Rat)
		if postCard.Sign() > 0 {
			batch.SetFrac(prevCard, postCard)
		}
		allowed := postCard.Sign() > 0 && cum.Cmp(p.maxShrink) <= 0
		out[i] = PolicyReport{
			Name:             p.name,
			Method:           post.projectionData[i].method,
			Prior:            cloneInt(priorCard),
			Previous:         cloneInt(prevCard),
			Post:             cloneInt(postCard),
			CumulativeShrink: cum,
			BatchShrink:      batch,
			MaxShrink:        new(big.Rat).Set(p.maxShrink),
			Allowed:          allowed,
			Projection:       projectionCertificate(p, post.projectionData[i], g.vars),
		}
		if !allowed && first == nil {
			first = admissionError(CodeCollusionExceeded, "admit", p.name,
				"joint protected-cardinality shrink %s/%s = %s exceeds maximum %s (previous %s)",
				priorCard, postCard, cum.RatString(), p.maxShrink.RatString(), prevCard)
		}
	}
	if first != nil {
		return out, first
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// State evaluation
// ---------------------------------------------------------------------------

type projectionData struct {
	method      string
	base        *systemCount
	combined    *systemCount
	cardinality *big.Int
	feasible    *big.Int
}

type stateEval struct {
	consistent     bool
	linear         *systemCount
	feasible       *big.Int
	projections    []*big.Int
	projectionData []projectionData
}

func evaluateState(rows []internalRow, aux []internalAux, vars []variableInfo, policies []internalPolicy, cap uint64) (*stateEval, error) {
	linear, err := countSystem(rows, vars)
	if err != nil {
		return nil, err
	}
	eval := &stateEval{consistent: linear.consistent, linear: linear}
	if !linear.consistent {
		eval.feasible = big.NewInt(0)
		eval.projections = make([]*big.Int, len(policies))
		eval.projectionData = make([]projectionData, len(policies))
		for i := range eval.projections {
			eval.projections[i] = big.NewInt(0)
		}
		return eval, nil
	}
	needEnum := len(aux) > 0
	for _, p := range policies {
		for _, f := range p.forms {
			if f.exact {
				needEnum = true
			}
		}
	}
	if !needEnum {
		eval.feasible = cloneInt(linear.kernel)
		eval.projections = make([]*big.Int, len(policies))
		eval.projectionData = make([]projectionData, len(policies))
		for i, p := range policies {
			card, combined, err := projectionFromBase(linear, rows, vars, p.forms)
			if err != nil {
				return nil, err
			}
			eval.projections[i] = card
			eval.projectionData[i] = projectionData{
				method: "crt-snf", base: linear, combined: combined, cardinality: card,
				feasible: cloneInt(linear.kernel),
			}
		}
		return eval, nil
	}

	sets := make([]map[string]struct{}, len(policies))
	for i := range sets {
		sets[i] = make(map[string]struct{})
	}
	er, err := enumerateSystem(rows, vars, cap, enumCallbacks{onPoint: func(x []uint64) bool {
		for _, a := range aux {
			if !a.eval(x) {
				return false
			}
		}
		for i, p := range policies {
			sets[i][formTupleKey(x, p.forms)] = struct{}{}
		}
		return true
	}})
	if err != nil {
		return nil, err
	}
	eval.feasible = er.feasible
	if er.feasible.Sign() == 0 {
		eval.consistent = false
	}
	eval.projections = make([]*big.Int, len(policies))
	eval.projectionData = make([]projectionData, len(policies))
	for i := range policies {
		card := big.NewInt(int64(len(sets[i])))
		eval.projections[i] = card
		eval.projectionData[i] = projectionData{
			method: "exact-enumeration", base: linear, cardinality: card,
			feasible: cloneInt(er.feasible),
		}
	}
	return eval, nil
}

func projectionFromBase(base *systemCount, rows []internalRow, vars []variableInfo, forms []internalForm) (*big.Int, *systemCount, error) {
	formRows, err := formsToRows(forms)
	if err != nil {
		return nil, nil, err
	}
	combined, err := countSystem(append(homogenizeRows(rows), formRows...), vars)
	if err != nil {
		return nil, nil, err
	}
	if !combined.consistent {
		return big.NewInt(0), combined, nil
	}
	q, rem := new(big.Int).QuoRem(base.kernel, combined.kernel, new(big.Int))
	if rem.Sign() != 0 {
		return nil, nil, admissionError(CodeInternal, "projection", "", "non-integral projection cardinality")
	}
	return q, combined, nil
}

func formTupleKey(x []uint64, forms []internalForm) string {
	if len(forms) == 0 {
		return ""
	}
	var b []byte
	for _, f := range forms {
		var z *big.Int
		if f.exact {
			z = new(big.Int)
			for _, t := range f.terms {
				z.Add(z, new(big.Int).Mul(t.coeff, new(big.Int).SetUint64(x[t.varIndex])))
			}
		} else {
			z = evalFormMod(x, f.terms, f.modulus)
		}
		b = append(b, z.String()...)
		b = append(b, 0)
	}
	return string(b)
}

func (g *Gate) computeDigest() string {
	type varDesc struct {
		Name    string `json:"name"`
		Modulus string `json:"modulus"`
	}
	type policyDesc struct {
		Name      string   `json:"name"`
		Variables []string `json:"variables,omitempty"`
		Forms     []string `json:"forms"`
		MaxShrink string   `json:"max_shrink"`
	}
	vs := make([]varDesc, len(g.vars))
	for i, v := range g.vars {
		vs[i] = varDesc{Name: v.name, Modulus: v.modulus.String()}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Name < vs[j].Name })
	ps := make([]policyDesc, len(g.policies))
	for i, p := range g.policies {
		forms := make([]string, len(p.forms))
		for j, f := range p.forms {
			forms[j] = canonicalFormKey(f, g.vars)
		}
		ps[i] = policyDesc{Name: p.name, Forms: forms, MaxShrink: p.maxShrink.RatString()}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	ids := make([]string, len(g.committed))
	for i, b := range g.committed {
		ids[i] = b.ID
	}
	payload := struct {
		Variables []varDesc    `json:"variables"`
		Policies  []policyDesc `json:"policies"`
		Baseline  *Batch       `json:"baseline,omitempty"`
		Batches   []*Batch     `json:"batches"`
		BatchIDs  []string     `json:"batch_ids"`
	}{Variables: vs, Policies: ps, Baseline: g.baseline, Batches: g.committed, BatchIDs: ids}
	return digestJSON(payload)
}
