package admission

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

var maxSecretModulus = new(big.Int).Lsh(big.NewInt(1), 48)

// Variable declares one unknown secret and the composite modulus of its
// domain.  Secret moduli must satisfy 2 <= Modulus <= 2^48.
type Variable struct {
	Name    string   `json:"name"`
	Modulus *big.Int `json:"modulus"`
}

// Term is a signed integer coefficient multiplying one secret variable.
type Term struct {
	Variable    string   `json:"variable"`
	Coefficient *big.Int `json:"coefficient"`
}

// ReleaseRow publishes
//
//	sum_i Coefficient_i * Variable_i == Value  (mod Modulus).
//
// The row is rejected unless every term defines a homomorphism from the
// variable's secret domain to Z/(Modulus Z), i.e. Modulus divides
// Coefficient_i * Variable_i.Modulus for every term.
//
// A nil Value is treated as zero.  For a well-defined linear map zero is
// always in the image and every non-empty fiber has the same cardinality, so
// omitting the value does not change pure-linear kernel or projection counts.
// When non-linear auxiliary constraints are present, supply the actual
// published value if its coset matters.
type ReleaseRow struct {
	ID      string   `json:"id,omitempty"`
	Modulus *big.Int `json:"modulus"`
	Terms   []Term   `json:"terms"`
	Value   *big.Int `json:"value"`
}

// MappingConstraint is a cross-generation affine mapping
//
//	Target == Multiplier * Source + Offset  (mod Modulus).
//
// If Modulus is nil it defaults to the Target variable's modulus.  A mapping
// with a different modulus is accepted only if it is well defined on both
// source and target domains.
type MappingConstraint struct {
	ID         string   `json:"id,omitempty"`
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Multiplier *big.Int `json:"multiplier"`
	Offset     *big.Int `json:"offset"`
	Modulus    *big.Int `json:"modulus,omitempty"`
}

// ModIntervalConstraint restricts a linear form to a cyclic interval of
// residues.  Low and High must lie in [0, Modulus).  With Wrap=false the arc
// is Low..High inclusive and is empty when Low > High.  With Wrap=true the arc
// runs forward from Low to High modulo Modulus, so Low > High wraps around;
// Low == High denotes the single residue Low.
type ModIntervalConstraint struct {
	ID      string   `json:"id,omitempty"`
	Terms   []Term   `json:"terms"`
	Modulus *big.Int `json:"modulus"`
	Low     *big.Int `json:"low"`
	High    *big.Int `json:"high"`
	Wrap    bool     `json:"wrap,omitempty"`
}

// BitSliceConstraint restricts selected bits of the least non-negative
// residue of a linear form:
//
//	((form mod Modulus) >> Shift) & (2^Width-1) == Value.
type BitSliceConstraint struct {
	ID      string   `json:"id,omitempty"`
	Terms   []Term   `json:"terms"`
	Modulus *big.Int `json:"modulus"`
	Shift   uint     `json:"shift"`
	Width   uint     `json:"width"`
	Value   *big.Int `json:"value"`
}

// ProtectedForm declares one protected linear quantity.  Modulus nil or zero
// means the exact integer value (supported by the exact enumerator, not by the
// pure SNF fast path).
type ProtectedForm struct {
	Name    string   `json:"name,omitempty"`
	Terms   []Term   `json:"terms"`
	Modulus *big.Int `json:"modulus,omitempty"`
}

// CollusionPolicy limits the cumulative shrink of the joint projection of all
// protected forms relative to the gate's prior (baseline) state:
//
//	priorJointCardinality / postJointCardinality <= MaxShrink.
//
// The cardinality is the number of distinct *tuples* of protected values, not
// a product of marginal cardinalities.  If Protected is empty, the variables
// named in Variables are protected individually (their joint projection is
// still evaluated as one tuple).
type CollusionPolicy struct {
	Name      string          `json:"name"`
	Variables []string        `json:"variables,omitempty"`
	Protected []ProtectedForm `json:"protected,omitempty"`
	MaxShrink *big.Rat        `json:"max_shrink"`
}

// Batch is one atomic candidate release.  All constraints in the batch are
// evaluated together; either the whole batch is admitted and appended to the
// gate state, or nothing changes.
type Batch struct {
	ID        string                  `json:"id"`
	Releases  []ReleaseRow            `json:"releases,omitempty"`
	Mappings  []MappingConstraint     `json:"mappings,omitempty"`
	Intervals []ModIntervalConstraint `json:"intervals,omitempty"`
	BitSlices []BitSliceConstraint    `json:"bit_slices,omitempty"`
}

// Shrink is a convenience constructor for a maximum shrink ratio num/den.
func Shrink(num, den int64) *big.Rat {
	if den == 0 {
		panic("admission: zero shrink denominator")
	}
	return big.NewRat(num, den)
}

// Config declares a gate.
type Config struct {
	Variables []Variable        `json:"variables"`
	Policies  []CollusionPolicy `json:"policies,omitempty"`
	Baseline  *Batch            `json:"baseline,omitempty"`
	// MaxEnumeration bounds exhaustive exact counting when auxiliary or
	// unbounded protected constraints are present.  A value of zero uses the
	// package default (5,000,000).  The gate never substitutes an estimate for
	// an exact count: exceeding the bound is an explicit rejection.
	MaxEnumeration uint64 `json:"max_enumeration,omitempty"`
}

const defaultMaxEnumeration = 5_000_000

func (b *Batch) empty() bool {
	return b == nil || (len(b.Releases) == 0 && len(b.Mappings) == 0 &&
		len(b.Intervals) == 0 && len(b.BitSlices) == 0)
}

func (b *Batch) clone() *Batch {
	if b == nil {
		return nil
	}
	out := &Batch{ID: b.ID}
	out.Releases = make([]ReleaseRow, len(b.Releases))
	for i, r := range b.Releases {
		out.Releases[i] = r
		out.Releases[i].Modulus = cloneInt(r.Modulus)
		out.Releases[i].Value = cloneInt(r.Value)
		out.Releases[i].Terms = cloneTerms(r.Terms)
	}
	out.Mappings = make([]MappingConstraint, len(b.Mappings))
	for i, m := range b.Mappings {
		out.Mappings[i] = m
		out.Mappings[i].Multiplier = cloneInt(m.Multiplier)
		out.Mappings[i].Offset = cloneInt(m.Offset)
		out.Mappings[i].Modulus = cloneInt(m.Modulus)
	}
	out.Intervals = make([]ModIntervalConstraint, len(b.Intervals))
	for i, c := range b.Intervals {
		out.Intervals[i] = c
		out.Intervals[i].Modulus = cloneInt(c.Modulus)
		out.Intervals[i].Low = cloneInt(c.Low)
		out.Intervals[i].High = cloneInt(c.High)
		out.Intervals[i].Terms = cloneTerms(c.Terms)
	}
	out.BitSlices = make([]BitSliceConstraint, len(b.BitSlices))
	for i, c := range b.BitSlices {
		out.BitSlices[i] = c
		out.BitSlices[i].Modulus = cloneInt(c.Modulus)
		out.BitSlices[i].Value = cloneInt(c.Value)
		out.BitSlices[i].Terms = cloneTerms(c.Terms)
	}
	return out
}

func cloneTerms(ts []Term) []Term {
	out := make([]Term, len(ts))
	for i, t := range ts {
		out[i] = t
		out[i].Coefficient = cloneInt(t.Coefficient)
	}
	return out
}

// ---------------------------------------------------------------------------
// Internal validated representation
// ---------------------------------------------------------------------------

type variableInfo struct {
	name     string
	modulus  *big.Int
	factors  map[string]int
	position int
}

type internalTerm struct {
	varIndex int
	coeff    *big.Int
}

type internalRow struct {
	id      string
	kind    string
	modulus *big.Int
	factors map[string]int
	terms   []internalTerm
	target  *big.Int
}

type canonicalTerm struct {
	Variable    string `json:"variable"`
	Coefficient string `json:"coefficient"`
}

type internalAux interface {
	auxID() string
	auxKind() string
	eval(x []uint64) bool
	canonical(vars []variableInfo) canonicalAux
}

type internalInterval struct {
	id      string
	modulus *big.Int
	terms   []internalTerm
	low     *big.Int
	high    *big.Int
	wrap    bool
}

func (c *internalInterval) auxID() string   { return c.id }
func (c *internalInterval) auxKind() string { return "interval" }

func (c *internalInterval) eval(x []uint64) bool {
	r := evalFormMod(x, c.terms, c.modulus)
	if !c.wrap {
		return r.Cmp(c.low) >= 0 && r.Cmp(c.high) <= 0
	}
	if c.low.Cmp(c.high) <= 0 {
		return r.Cmp(c.low) >= 0 && r.Cmp(c.high) <= 0
	}
	return r.Cmp(c.low) >= 0 || r.Cmp(c.high) <= 0
}

func (c *internalInterval) canonical(vars []variableInfo) canonicalAux {
	return canonicalAux{
		ID: c.id, Kind: c.auxKind(), Modulus: c.modulus.String(),
		Low: c.low.String(), High: c.high.String(), Wrap: c.wrap,
		Terms: canonicalTermsOf(c.terms, vars),
	}
}

type internalBitSlice struct {
	id      string
	modulus *big.Int
	terms   []internalTerm
	shift   uint
	width   uint
	value   *big.Int
	mask    *big.Int
}

func (c *internalBitSlice) auxID() string   { return c.id }
func (c *internalBitSlice) auxKind() string { return "bit_slice" }

func (c *internalBitSlice) eval(x []uint64) bool {
	r := evalFormMod(x, c.terms, c.modulus)
	got := new(big.Int).Rsh(r, c.shift)
	got.And(got, c.mask)
	return got.Cmp(c.value) == 0
}

func (c *internalBitSlice) canonical(vars []variableInfo) canonicalAux {
	return canonicalAux{
		ID: c.id, Kind: c.auxKind(), Modulus: c.modulus.String(),
		Shift: int(c.shift), Width: int(c.width), Value: c.value.String(),
		Terms: canonicalTermsOf(c.terms, vars),
	}
}

type canonicalAux struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Modulus string          `json:"modulus"`
	Terms   []canonicalTerm `json:"terms"`
	Low     string          `json:"low,omitempty"`
	High    string          `json:"high,omitempty"`
	Wrap    bool            `json:"wrap,omitempty"`
	Shift   int             `json:"shift,omitempty"`
	Width   int             `json:"width,omitempty"`
	Value   string          `json:"value,omitempty"`
}

type internalForm struct {
	name    string
	modulus *big.Int // nil => exact integer
	factors map[string]int
	terms   []internalTerm
	exact   bool
}

type parsedBatch struct {
	id   string
	rows []internalRow
	aux  []internalAux
}

func canonicalTermsOf(ts []internalTerm, vars []variableInfo) []canonicalTerm {
	out := make([]canonicalTerm, 0, len(ts))
	for _, t := range ts {
		if t.coeff.Sign() == 0 {
			continue
		}
		out = append(out, canonicalTerm{Variable: vars[t.varIndex].name, Coefficient: t.coeff.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Variable < out[j].Variable })
	return out
}

func evalFormMod(x []uint64, terms []internalTerm, modulus *big.Int) *big.Int {
	acc := new(big.Int)
	for _, t := range terms {
		prod := new(big.Int).Mul(t.coeff, new(big.Int).SetUint64(x[t.varIndex]))
		acc.Add(acc, prod)
	}
	return modInt(acc, modulus)
}

func buildVariables(in []Variable) ([]variableInfo, map[string]int, error) {
	if len(in) == 0 {
		return nil, nil, admissionError(CodeInvalidInput, "config", "", "at least one variable is required")
	}
	vars := make([]variableInfo, len(in))
	byName := make(map[string]int, len(in))
	for i, v := range in {
		name := strings.TrimSpace(v.Name)
		if name == "" {
			return nil, nil, admissionError(CodeInvalidInput, "config", "", "variable %d has an empty name", i)
		}
		if _, exists := byName[name]; exists {
			return nil, nil, admissionError(CodeInvalidInput, "config", "", "duplicate variable name %q", name)
		}
		if v.Modulus == nil {
			return nil, nil, admissionError(CodeInvalidInput, "config", "", "variable %q has nil modulus", name)
		}
		if v.Modulus.Cmp(big.NewInt(2)) < 0 || v.Modulus.Cmp(maxSecretModulus) > 0 {
			return nil, nil, admissionError(CodeInvalidInput, "config", "", "variable %q modulus %s is outside [2, 2^48]", name, v.Modulus)
		}
		f, err := factorize(v.Modulus)
		if err != nil {
			return nil, nil, admissionError(CodeInvalidInput, "config", "", "factor modulus of %q: %v", name, err)
		}
		vars[i] = variableInfo{name: name, modulus: cloneInt(v.Modulus), factors: f, position: i}
		byName[name] = i
	}
	return vars, byName, nil
}

func buildPolicies(in []CollusionPolicy, vars []variableInfo, byName map[string]int) ([]internalPolicy, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]internalPolicy, len(in))
	seen := make(map[string]bool, len(in))
	for i, p := range in {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return nil, admissionError(CodeInvalidInput, "config", "", "policy %d has an empty name", i)
		}
		if seen[name] {
			return nil, admissionError(CodeInvalidInput, "config", name, "duplicate policy name")
		}
		seen[name] = true
		if p.MaxShrink == nil || p.MaxShrink.Sign() <= 0 || p.MaxShrink.Cmp(big.NewRat(1, 1)) < 0 {
			return nil, admissionError(CodeInvalidInput, "config", name, "MaxShrink must be a rational >= 1")
		}
		forms := p.Protected
		if len(forms) == 0 {
			if len(p.Variables) == 0 {
				return nil, admissionError(CodeInvalidInput, "config", name, "policy must list Protected forms or Variables")
			}
			forms = make([]ProtectedForm, len(p.Variables))
			for j, vn := range p.Variables {
				idx, ok := byName[strings.TrimSpace(vn)]
				if !ok {
					return nil, admissionError(CodeInvalidInput, "config", name, "unknown variable %q", vn)
				}
				forms[j] = ProtectedForm{
					Name:    vars[idx].name,
					Terms:   []Term{{Variable: vars[idx].name, Coefficient: big.NewInt(1)}},
					Modulus: cloneInt(vars[idx].modulus),
				}
			}
		}
		internalForms := make([]internalForm, len(forms))
		for j, f := range forms {
			fm, err := buildForm(f, vars, byName)
			if err != nil {
				if ae, ok := err.(*AdmissionError); ok {
					return nil, ae
				}
				return nil, admissionError(CodeInvalidInput, "config", name, "protected form %d: %v", j, err)
			}
			internalForms[j] = fm
		}
		out[i] = internalPolicy{
			name:      name,
			forms:     internalForms,
			maxShrink: new(big.Rat).Set(p.MaxShrink),
		}
	}
	return out, nil
}

type internalPolicy struct {
	name      string
	forms     []internalForm
	maxShrink *big.Rat
}

func buildForm(f ProtectedForm, vars []variableInfo, byName map[string]int) (internalForm, error) {
	mod := cloneInt(f.Modulus)
	exact := mod.Sign() == 0
	if !exact && mod.Sign() < 0 {
		return internalForm{}, fmt.Errorf("negative modulus")
	}
	terms, err := combineTerms(f.Terms, vars, byName, mod, exact)
	if err != nil {
		return internalForm{}, err
	}
	var factors map[string]int
	if !exact {
		factors, err = factorize(mod)
		if err != nil {
			return internalForm{}, err
		}
	}
	name := f.Name
	if name == "" {
		name = "form"
	}
	return internalForm{name: name, modulus: mod, factors: factors, terms: terms, exact: exact}, nil
}

func combineTerms(in []Term, vars []variableInfo, byName map[string]int, modulus *big.Int, exact bool) ([]internalTerm, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if err := nilTermCheck(in); err != nil {
		return nil, err
	}
	coeffs := make(map[int]*big.Int)
	for _, t := range in {
		idx, ok := byName[strings.TrimSpace(t.Variable)]
		if !ok {
			return nil, admissionError(CodeInvalidInput, "constraint", "", "unknown variable %q", t.Variable)
		}
		c := cloneInt(t.Coefficient)
		if !exact {
			if modulus == nil || modulus.Sign() <= 0 {
				return nil, admissionError(CodeInvalidInput, "constraint", "", "missing positive modulus")
			}
			if err := checkWellDefined(c, vars[idx], modulus, t.Variable); err != nil {
				return nil, err
			}
			c = modInt(c, modulus)
		}
		if old, ok := coeffs[idx]; ok {
			old.Add(old, c)
		} else {
			coeffs[idx] = c
		}
		if !exact {
			coeffs[idx] = modInt(coeffs[idx], modulus)
		}
	}
	out := make([]internalTerm, 0, len(coeffs))
	for idx, c := range coeffs {
		if c.Sign() == 0 {
			continue
		}
		out = append(out, internalTerm{varIndex: idx, coeff: cloneInt(c)})
	}
	sort.Slice(out, func(i, j int) bool { return vars[out[i].varIndex].name < vars[out[j].varIndex].name })
	return out, nil
}

func checkWellDefined(coeff *big.Int, v variableInfo, modulus *big.Int, varName string) error {
	if modulus.Sign() <= 0 {
		return admissionError(CodeInvalidInput, "constraint", "", "modulus must be positive")
	}
	prod := new(big.Int).Mul(coeff, v.modulus)
	if new(big.Int).Mod(prod, modulus).Sign() != 0 {
		need := new(big.Int).Quo(modulus, gcdInt(modulus, v.modulus))
		return admissionError(CodeNonWellDefined, "constraint", "",
			"coefficient %s on %q (domain Z/%s Z) is not well defined modulo %s; coefficient must be a multiple of %s",
			coeff, varName, v.modulus, modulus, need)
	}
	return nil
}

func buildBatch(b *Batch, vars []variableInfo, byName map[string]int) (parsedBatch, error) {
	out := parsedBatch{}
	if b == nil {
		return out, nil
	}
	out.id = b.ID
	for i, r := range b.Releases {
		if r.Modulus == nil || r.Modulus.Sign() <= 0 {
			return out, admissionError(CodeInvalidInput, "batch", "", "release %d has missing or non-positive modulus", i)
		}
		row, err := buildRow(r.ID, "release", r.Modulus, r.Terms, r.Value, vars, byName)
		if err != nil {
			return out, err
		}
		out.rows = append(out.rows, row)
	}
	for i, m := range b.Mappings {
		ti, ok := byName[strings.TrimSpace(m.Target)]
		if !ok {
			return out, admissionError(CodeInvalidInput, "batch", "", "mapping %d has unknown target %q", i, m.Target)
		}
		si, ok := byName[strings.TrimSpace(m.Source)]
		if !ok {
			return out, admissionError(CodeInvalidInput, "batch", "", "mapping %d has unknown source %q", i, m.Source)
		}
		mod := cloneInt(m.Modulus)
		if mod.Sign() == 0 {
			mod.Set(vars[ti].modulus)
		}
		terms := []Term{
			{Variable: vars[ti].name, Coefficient: big.NewInt(1)},
			{Variable: vars[si].name, Coefficient: new(big.Int).Neg(cloneInt(m.Multiplier))},
		}
		row, err := buildRow(m.ID, "mapping", mod, terms, m.Offset, vars, byName)
		if err != nil {
			return out, err
		}
		out.rows = append(out.rows, row)
	}
	for i, c := range b.Intervals {
		if c.Modulus == nil || c.Modulus.Cmp(big.NewInt(2)) < 0 {
			return out, admissionError(CodeInvalidInput, "batch", "", "interval %d modulus must be >= 2", i)
		}
		if c.Low == nil || c.High == nil {
			return out, admissionError(CodeInvalidInput, "batch", "", "interval %d has nil endpoint", i)
		}
		if c.Low.Sign() < 0 || c.Low.Cmp(c.Modulus) >= 0 || c.High.Sign() < 0 || c.High.Cmp(c.Modulus) >= 0 {
			return out, admissionError(CodeInvalidInput, "batch", "", "interval %d endpoints must lie in [0, modulus)", i)
		}
		terms, err := combineTerms(c.Terms, vars, byName, c.Modulus, false)
		if err != nil {
			return out, err
		}
		out.aux = append(out.aux, &internalInterval{
			id: c.ID, modulus: cloneInt(c.Modulus), terms: terms,
			low: cloneInt(c.Low), high: cloneInt(c.High), wrap: c.Wrap,
		})
	}
	for i, c := range b.BitSlices {
		if c.Modulus == nil || c.Modulus.Sign() <= 0 {
			return out, admissionError(CodeInvalidInput, "batch", "", "bit slice %d modulus must be positive", i)
		}
		if c.Width > 4096 {
			return out, admissionError(CodeInvalidInput, "batch", "", "bit slice %d width is too large", i)
		}
		mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), c.Width), big.NewInt(1))
		val := cloneInt(c.Value)
		if val.Sign() < 0 || val.Cmp(mask) > 0 {
			return out, admissionError(CodeInvalidInput, "batch", "", "bit slice %d value does not fit width %d", i, c.Width)
		}
		terms, err := combineTerms(c.Terms, vars, byName, c.Modulus, false)
		if err != nil {
			return out, err
		}
		out.aux = append(out.aux, &internalBitSlice{
			id: c.ID, modulus: cloneInt(c.Modulus), terms: terms,
			shift: c.Shift, width: c.Width, value: val, mask: mask,
		})
	}
	return out, nil
}

func buildRow(id, kind string, modulus *big.Int, terms []Term, value *big.Int, vars []variableInfo, byName map[string]int) (internalRow, error) {
	if modulus == nil || modulus.Sign() <= 0 {
		return internalRow{}, admissionError(CodeInvalidInput, "constraint", "", "row %q has non-positive modulus", id)
	}
	if err := nilTermCheck(terms); err != nil {
		return internalRow{}, err
	}
	combined, err := combineTerms(terms, vars, byName, modulus, false)
	if err != nil {
		return internalRow{}, err
	}
	f, err := factorize(modulus)
	if err != nil {
		return internalRow{}, admissionError(CodeInvalidInput, "constraint", "", "factor row modulus %s: %v", modulus, err)
	}
	return internalRow{
		id: id, kind: kind, modulus: cloneInt(modulus), factors: f,
		terms: combined, target: modInt(value, modulus),
	}, nil
}

func nilTermCheck(terms []Term) error {
	for _, t := range terms {
		if t.Coefficient == nil {
			return admissionError(CodeInvalidInput, "constraint", "", "term for %q has nil coefficient", t.Variable)
		}
	}
	return nil
}
