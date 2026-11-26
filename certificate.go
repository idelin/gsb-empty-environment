package admission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// Certificate is a canonical, reviewable record of the cardinalities used for
// an admission decision.  Invariant factors are the Smith normal form
// invariants of the prime-power presentation matrix, so they are independent
// of elimination order.
type Certificate struct {
	Method          string               `json:"method"`
	Domain          string               `json:"domain_cardinality"`
	Linear          LinearCertificate    `json:"linear_system"`
	Policies        []PolicyCertificate  `json:"policy_projections"`
	Auxiliary       AuxiliaryCertificate `json:"auxiliary"`
	CanonicalSHA256 string               `json:"canonical_sha256"`
	StateDigest     string               `json:"state_digest,omitempty"`
}

// LinearCertificate decomposes the committed linear system by prime powers.
type LinearCertificate struct {
	Method     string                  `json:"method"`
	Domain     string                  `json:"domain_cardinality"`
	Feasible   string                  `json:"feasible_cardinality"`
	Consistent bool                    `json:"consistent"`
	Primes     []PrimePowerCertificate `json:"prime_powers"`
}

// PrimePowerCertificate is one CRT component.  The displayed invariant
// factors are the canonical Smith invariants of [A | diag(p^v)].
type PrimePowerCertificate struct {
	Prime            string          `json:"prime"`
	DomainExponents  []ExponentEntry `json:"domain_exponents"`
	RowExponents     []RowExponent   `json:"row_exponents"`
	InvariantFactors []string        `json:"invariant_factors"`
	DeterminantIndex string          `json:"determinant_index"`
	DomainSize       string          `json:"domain_size"`
	CodomainSize     string          `json:"codomain_size"`
	ImageSize        string          `json:"image_size"`
	KernelSize       string          `json:"kernel_size"`
	Consistent       bool            `json:"consistent"`
}

type ExponentEntry struct {
	Name     string `json:"name"`
	Exponent int    `json:"exponent"`
}

type RowExponent struct {
	Descriptor string `json:"descriptor"`
	Exponent   int    `json:"exponent"`
}

// PolicyCertificate records the exact joint projection certificate for the
// prior, pre-batch, and post-batch states.
type PolicyCertificate struct {
	Name             string                `json:"name"`
	MaxShrink        string                `json:"max_shrink"`
	CumulativeShrink string                `json:"cumulative_shrink"`
	WithinLimit      bool                  `json:"within_limit"`
	Prior            ProjectionCertificate `json:"prior"`
	Previous         ProjectionCertificate `json:"previous"`
	Post             ProjectionCertificate `json:"post"`
}

// ProjectionCertificate certifies |P(K)| = |ker Phi| / |ker(Phi,P)| for the
// pure linear case.  For auxiliary/exact-value constraints it records an exact
// enumeration rather than an estimate.
type ProjectionCertificate struct {
	Name               string                  `json:"name"`
	Method             string                  `json:"method"`
	Forms              []FormCertificate       `json:"protected_forms"`
	Numerator          string                  `json:"numerator_kernel"`
	Denominator        string                  `json:"denominator_intersection_kernel"`
	Cardinality        string                  `json:"joint_cardinality"`
	NumeratorPrimes    []PrimeCountCertificate `json:"numerator_prime_counts,omitempty"`
	DenominatorPrimes  []PrimeCountCertificate `json:"denominator_prime_counts,omitempty"`
	EnumeratedFeasible string                  `json:"enumerated_feasible,omitempty"`
}

type FormCertificate struct {
	Name    string          `json:"name"`
	Modulus string          `json:"modulus"`
	Terms   []canonicalTerm `json:"terms"`
}

type PrimeCountCertificate struct {
	Prime            string   `json:"prime"`
	InvariantFactors []string `json:"invariant_factors"`
	DomainSize       string   `json:"domain_size"`
	CodomainSize     string   `json:"codomain_size"`
	KernelSize       string   `json:"kernel_size"`
	Consistent       bool     `json:"consistent"`
}

type AuxiliaryCertificate struct {
	Count       int            `json:"count"`
	Canonical   []canonicalAux `json:"canonical"`
	ExactMethod string         `json:"exact_method,omitempty"`
	Feasible    string         `json:"exact_feasible,omitempty"`
}

func buildCertificate(vars []variableInfo, policies []internalPolicy, prior, prev, post *stateEval, rows []internalRow, aux []internalAux) *Certificate {
	cert := &Certificate{
		Domain: post.linear.domain.String(),
		Linear: buildLinearCertificate(rows, vars, post.linear),
	}
	method := "crt-snf"
	if len(aux) > 0 {
		method = "crt-snf+exact-enumeration"
	}
	for _, d := range post.projectionData {
		if d.method == "exact-enumeration" {
			method = "crt-snf+exact-enumeration"
		}
	}
	cert.Method = method
	type namedIndex struct {
		name string
		idx  int
	}
	order := make([]namedIndex, len(policies))
	for i, p := range policies {
		order[i] = namedIndex{name: p.name, idx: i}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].name < order[j].name })
	for _, ni := range order {
		p := policies[ni.idx]
		priorCard := prior.projections[ni.idx]
		postCard := post.projections[ni.idx]
		ratio := new(big.Rat)
		if postCard.Sign() > 0 {
			ratio.SetFrac(priorCard, postCard)
		}
		cert.Policies = append(cert.Policies, PolicyCertificate{
			Name:             p.name,
			MaxShrink:        p.maxShrink.RatString(),
			CumulativeShrink: ratio.RatString(),
			WithinLimit:      postCard.Sign() > 0 && ratio.Cmp(p.maxShrink) <= 0,
			Prior:            projectionCertificate(p, prior.projectionData[ni.idx], vars),
			Previous:         projectionCertificate(p, prev.projectionData[ni.idx], vars),
			Post:             projectionCertificate(p, post.projectionData[ni.idx], vars),
		})
	}
	if len(aux) > 0 {
		cert.Auxiliary.Count = len(aux)
		cert.Auxiliary.ExactMethod = "full affine solution-set enumeration"
		cert.Auxiliary.Feasible = post.feasible.String()
		for _, a := range aux {
			cert.Auxiliary.Canonical = append(cert.Auxiliary.Canonical, a.canonical(vars))
		}
		sort.Slice(cert.Auxiliary.Canonical, func(i, j int) bool {
			ki := canonicalAuxKey(cert.Auxiliary.Canonical[i])
			kj := canonicalAuxKey(cert.Auxiliary.Canonical[j])
			if ki != kj {
				return ki < kj
			}
			return cert.Auxiliary.Canonical[i].ID < cert.Auxiliary.Canonical[j].ID
		})
	}
	if method == "crt-snf+exact-enumeration" && cert.Auxiliary.ExactMethod == "" {
		cert.Auxiliary.ExactMethod = "exact affine enumeration for protected exact-value forms"
		cert.Auxiliary.Feasible = post.feasible.String()
	}
	cert.CanonicalSHA256 = cert.computeDigest()
	return cert
}

func buildLinearCertificate(rows []internalRow, vars []variableInfo, sc *systemCount) LinearCertificate {
	out := LinearCertificate{
		Method: "crt+smith-normal-form", Domain: sc.domain.String(),
		Consistent: sc.consistent, Feasible: sc.kernel.String(),
	}
	for _, pr := range sc.primes {
		pc := PrimePowerCertificate{
			Prime:            pr.system.p.String(),
			DeterminantIndex: pr.index.String(),
			DomainSize:       pr.domain.String(),
			CodomainSize:     pr.codomain.String(),
			KernelSize:       pr.kernel.String(),
			Consistent:       pr.consistent,
		}
		for j, orig := range pr.system.varIdx {
			pc.DomainExponents = append(pc.DomainExponents, ExponentEntry{
				Name: vars[orig].name, Exponent: pr.system.varExp[j],
			})
		}
		sort.Slice(pc.DomainExponents, func(i, j int) bool { return pc.DomainExponents[i].Name < pc.DomainExponents[j].Name })
		for _, r := range canonicalPrimeRows(pr.system, vars) {
			pc.RowExponents = append(pc.RowExponents, RowExponent{
				Descriptor: canonicalPrimeRowKey(r), Exponent: r.Exponent,
			})
		}
		for _, d := range pr.invariants {
			pc.InvariantFactors = append(pc.InvariantFactors, d.String())
		}
		if pr.index.Sign() > 0 {
			img, rem := new(big.Int).QuoRem(pr.codomain, pr.index, new(big.Int))
			if rem.Sign() == 0 {
				pc.ImageSize = img.String()
			}
		}
		out.Primes = append(out.Primes, pc)
	}
	return out
}

func projectionCertificate(p internalPolicy, d projectionData, vars []variableInfo) ProjectionCertificate {
	out := ProjectionCertificate{
		Name: p.name, Method: d.method, Cardinality: d.cardinality.String(),
	}
	for _, f := range p.forms {
		mod := "exact"
		if !f.exact {
			mod = f.modulus.String()
		}
		out.Forms = append(out.Forms, FormCertificate{
			Name: f.name, Modulus: mod, Terms: canonicalTermsOf(f.terms, vars),
		})
	}
	sort.Slice(out.Forms, func(i, j int) bool {
		return formCertKey(out.Forms[i]) < formCertKey(out.Forms[j])
	})
	if d.method == "exact-enumeration" {
		if d.feasible != nil {
			out.Numerator = d.feasible.String()
			out.EnumeratedFeasible = d.feasible.String()
		}
		return out
	}
	if d.base != nil {
		out.Numerator = d.base.kernel.String()
		out.NumeratorPrimes = primeCounts(d.base.primes)
	}
	if d.combined != nil {
		out.Denominator = d.combined.kernel.String()
		out.DenominatorPrimes = primeCounts(d.combined.primes)
	}
	return out
}

func formCertKey(f FormCertificate) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func primeCounts(prs []*primeResult) []PrimeCountCertificate {
	out := make([]PrimeCountCertificate, 0, len(prs))
	for _, pr := range prs {
		pc := PrimeCountCertificate{
			Prime: pr.system.p.String(), DomainSize: pr.domain.String(),
			CodomainSize: pr.codomain.String(), KernelSize: pr.kernel.String(),
			Consistent: pr.consistent,
		}
		for _, d := range pr.invariants {
			pc.InvariantFactors = append(pc.InvariantFactors, d.String())
		}
		out = append(out, pc)
	}
	return out
}

func canonicalFormKey(f internalForm, vars []variableInfo) string {
	mod := "exact"
	if !f.exact {
		mod = f.modulus.String()
	}
	terms := canonicalTermsOf(f.terms, vars)
	return fmt.Sprintf("%s|%v", mod, terms)
}

func canonicalAuxKey(a canonicalAux) string {
	a.ID = ""
	b, _ := json.Marshal(a)
	return string(b)
}

func digestJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// computeDigest hashes the canonical mathematical certificate, excluding
// mutable audit fields such as the state digest itself.
func (c *Certificate) computeDigest() string {
	if c == nil {
		return ""
	}
	cp := *c
	cp.CanonicalSHA256 = ""
	cp.StateDigest = ""
	return digestJSON(cp)
}

// Verify recomputes every displayed cardinality from the prime-power
// invariant factors and checks the canonical digest.  It is deliberately
// independent of the gate that produced the certificate.
func (c *Certificate) Verify() error {
	if c == nil {
		return fmt.Errorf("admission: nil certificate")
	}
	linearProd := big.NewInt(1)
	domainProd := big.NewInt(1)
	for _, pc := range c.Linear.Primes {
		p, ok := new(big.Int).SetString(pc.Prime, 10)
		if !ok {
			return fmt.Errorf("admission: bad prime %q", pc.Prime)
		}
		domain := big.NewInt(1)
		for _, e := range pc.DomainExponents {
			if e.Exponent < 0 {
				return fmt.Errorf("admission: negative exponent")
			}
			domain.Mul(domain, new(big.Int).Exp(p, big.NewInt(int64(e.Exponent)), nil))
		}
		codomain := big.NewInt(1)
		for _, r := range pc.RowExponents {
			if r.Exponent < 0 {
				return fmt.Errorf("admission: negative row exponent")
			}
			codomain.Mul(codomain, new(big.Int).Exp(p, big.NewInt(int64(r.Exponent)), nil))
		}
		index := big.NewInt(1)
		var prev *big.Int
		for _, ds := range pc.InvariantFactors {
			d, ok := new(big.Int).SetString(ds, 10)
			if !ok || d.Sign() <= 0 {
				return fmt.Errorf("admission: bad invariant factor %q", ds)
			}
			if prev != nil && new(big.Int).Mod(d, prev).Sign() != 0 {
				return fmt.Errorf("admission: invariant factors not divisibility ordered")
			}
			prev = d
			index.Mul(index, d)
		}
		if pc.DomainSize != domain.String() || pc.CodomainSize != codomain.String() || pc.DeterminantIndex != index.String() {
			return fmt.Errorf("admission: prime %s size fields do not reconstruct", pc.Prime)
		}
		if index.Sign() == 0 {
			return fmt.Errorf("admission: zero determinant index")
		}
		img, rem := new(big.Int).QuoRem(codomain, index, new(big.Int))
		if rem.Sign() != 0 || pc.ImageSize != img.String() {
			return fmt.Errorf("admission: prime %s image size mismatch", pc.Prime)
		}
		num := new(big.Int).Mul(domain, index)
		kernel, rem := new(big.Int).QuoRem(num, codomain, new(big.Int))
		if rem.Sign() != 0 || pc.KernelSize != kernel.String() {
			return fmt.Errorf("admission: prime %s kernel size mismatch", pc.Prime)
		}
		linearProd.Mul(linearProd, kernel)
		domainProd.Mul(domainProd, domain)
	}
	if c.Linear.Domain != domainProd.String() || c.Domain != c.Linear.Domain {
		return fmt.Errorf("admission: global domain size mismatch")
	}
	if c.Linear.Feasible != linearProd.String() {
		return fmt.Errorf("admission: feasible cardinality mismatch")
	}
	if c.Auxiliary.Count != len(c.Auxiliary.Canonical) {
		return fmt.Errorf("admission: auxiliary certificate count mismatch")
	}
	if c.Auxiliary.Feasible != "" {
		exact := mustBig(c.Auxiliary.Feasible)
		linear := mustBig(c.Linear.Feasible)
		if exact == nil || linear == nil || exact.Cmp(linear) > 0 {
			return fmt.Errorf("admission: auxiliary feasible cardinality exceeds linear relaxation")
		}
	}
	for _, pc := range c.Policies {
		for _, proj := range []ProjectionCertificate{pc.Prior, pc.Previous, pc.Post} {
			if err := verifyProjectionCertificate(proj); err != nil {
				return fmt.Errorf("policy %s: %w", pc.Name, err)
			}
		}
		prior := mustBig(pc.Prior.Cardinality)
		prev := mustBig(pc.Previous.Cardinality)
		post := mustBig(pc.Post.Cardinality)
		if prior == nil || prev == nil || post == nil || prior.Cmp(prev) < 0 || prev.Cmp(post) < 0 {
			return fmt.Errorf("admission: policy %s projection cardinalities are not monotone", pc.Name)
		}
		if pc.MaxShrink == "" {
			return fmt.Errorf("admission: policy %s has no shrink bound", pc.Name)
		}
		bound, ok := new(big.Rat).SetString(pc.MaxShrink)
		if !ok || post.Sign() <= 0 {
			return fmt.Errorf("admission: policy %s has invalid shrink bound or post cardinality", pc.Name)
		}
		ratio := new(big.Rat).SetFrac(prior, post)
		wantAllowed := ratio.Cmp(bound) <= 0
		if pc.WithinLimit != wantAllowed || pc.CumulativeShrink != ratio.RatString() {
			return fmt.Errorf("admission: policy %s shrink decision mismatch", pc.Name)
		}
	}
	if c.CanonicalSHA256 != "" && c.CanonicalSHA256 != c.computeDigest() {
		return fmt.Errorf("admission: canonical certificate digest mismatch")
	}
	return nil
}

// VerifyCertificate is the package-level convenience form of
// Certificate.Verify.
func VerifyCertificate(c *Certificate) error { return c.Verify() }

func verifyProjectionCertificate(p ProjectionCertificate) error {
	if p.Method == "exact-enumeration" {
		card := mustBig(p.Cardinality)
		if card == nil {
			return fmt.Errorf("bad enumeration cardinality %q", p.Cardinality)
		}
		if p.Numerator != "" {
			n := mustBig(p.Numerator)
			if n == nil || card.Cmp(n) > 0 {
				return fmt.Errorf("enumeration projection cardinality exceeds feasible count")
			}
		}
		return nil
	}
	num := big.NewInt(1)
	for _, pc := range p.NumeratorPrimes {
		if err := verifyPrimeCount(pc); err != nil {
			return err
		}
		z := mustBig(pc.KernelSize)
		if z == nil {
			return fmt.Errorf("bad numerator prime kernel")
		}
		num.Mul(num, z)
	}
	den := big.NewInt(1)
	for _, pc := range p.DenominatorPrimes {
		if err := verifyPrimeCount(pc); err != nil {
			return err
		}
		z := mustBig(pc.KernelSize)
		if z == nil {
			return fmt.Errorf("bad denominator prime kernel")
		}
		den.Mul(den, z)
	}
	if p.Numerator != num.String() || p.Denominator != den.String() {
		return fmt.Errorf("projection prime cardinalities do not reconstruct")
	}
	if den.Sign() == 0 {
		return fmt.Errorf("zero denominator intersection kernel")
	}
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() != 0 || p.Cardinality != q.String() {
		return fmt.Errorf("joint projection cardinality mismatch")
	}
	return nil
}

func verifyPrimeCount(pc PrimeCountCertificate) error {
	index := big.NewInt(1)
	var prev *big.Int
	for _, ds := range pc.InvariantFactors {
		d := mustBig(ds)
		if d == nil || d.Sign() <= 0 {
			return fmt.Errorf("bad prime-count invariant factor %q", ds)
		}
		if prev != nil && new(big.Int).Mod(d, prev).Sign() != 0 {
			return fmt.Errorf("prime-count invariant factors not divisibility ordered")
		}
		prev = d
		index.Mul(index, d)
	}
	domain := mustBig(pc.DomainSize)
	codomain := mustBig(pc.CodomainSize)
	kernel := mustBig(pc.KernelSize)
	if domain == nil || codomain == nil || kernel == nil || codomain.Sign() == 0 {
		return fmt.Errorf("bad prime-count sizes")
	}
	if !pc.Consistent {
		if kernel.Sign() != 0 {
			return fmt.Errorf("inconsistent prime-count has nonzero kernel")
		}
		return nil
	}
	num := new(big.Int).Mul(domain, index)
	q, rem := new(big.Int).QuoRem(num, codomain, new(big.Int))
	if rem.Sign() != 0 || q.Cmp(kernel) != 0 {
		return fmt.Errorf("prime-count kernel does not reconstruct")
	}
	return nil
}

func mustBig(s string) *big.Int {
	if s == "" {
		return nil
	}
	z, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil
	}
	return z
}
