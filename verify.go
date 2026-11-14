package netsec

import (
	"fmt"
	"math/big"
	"math/rand"
)

// sampleBound limits the absolute value of sampled integers.
const sampleBound = 9

// certify recomputes the linear combination symbolically and returns its
// secret part. It fails when the random part does not vanish identically
// or the secret part is empty — this is the elimination-certificate check.
func certify(p *Program, kind map[string]bool, c *Combination) (Expr, error) {
	combined, err := combine(p, c)
	if err != nil {
		return Expr{}, err
	}
	secret := Expr{Terms: map[string]*big.Rat{}}
	for id, k := range combined.Terms {
		if id == "" {
			continue // constants are observable and carry no randomness
		}
		isSecret, ok := kind[id]
		if !ok {
			return Expr{}, fmt.Errorf("randdep: certificate references undeclared variable %q", id)
		}
		if !isSecret {
			return Expr{}, fmt.Errorf("randdep: certificate invalid: random term %q survives with coefficient %s", id, k.RatString())
		}
		secret.Terms[id] = new(big.Rat).Set(k)
	}
	if secret.isZero() {
		return Expr{}, fmt.Errorf("randdep: certificate invalid: combination preserves no secret term")
	}
	return secret, nil
}

// combine evaluates the certified linear combination of output expressions.
func combine(p *Program, c *Combination) (Expr, error) {
	byName := make(map[string]Expr, len(p.Outputs))
	for _, o := range p.Outputs {
		byName[o.Name] = o.Expr
	}
	if len(c.Outputs) == 0 {
		return Expr{}, fmt.Errorf("randdep: combination selects no outputs")
	}
	seen := make(map[string]bool, len(c.Outputs))
	acc := Expr{Terms: map[string]*big.Rat{}}
	for _, name := range c.Outputs {
		if seen[name] {
			return Expr{}, fmt.Errorf("randdep: combination repeats output %q", name)
		}
		seen[name] = true
		coeff, ok := c.Coeffs[name]
		if !ok || coeff.Sign() == 0 {
			return Expr{}, fmt.Errorf("randdep: combination missing nonzero coefficient for output %q", name)
		}
		expr, ok := byName[name]
		if !ok {
			return Expr{}, fmt.Errorf("randdep: combination references unknown output %q", name)
		}
		acc = acc.Add(expr.Scale(coeff))
	}
	return acc, nil
}

// VerifyCertificate validates a combination against a program purely
// symbolically: the random part must cancel identically and at least one
// secret term must survive. It returns the preserved secret expression.
func VerifyCertificate(p *Program, c *Combination) (Expr, error) {
	kind, err := p.validate(DefaultMaxOutputs)
	if err != nil {
		return Expr{}, err
	}
	return certify(p, kind, c)
}

// SampleVerify cross-checks a combination numerically. Every variable
// identity — secret or random — receives an independent small integer per
// trial, and the combined expression must equal its secret part evaluated
// on the secret identities alone. Sampling is keyed strictly by identity:
// two distinct identities that draw the same value remain distinct random
// sources, so a combination that only "cancels" when two sources collide
// in value is still rejected (the symbolic certificate already fails for
// such combinations; sampling is a defense-in-depth check of the same
// invariant).
//
// If rng is nil, a deterministic generator is used so results are
// reproducible.
func SampleVerify(p *Program, c *Combination, trials int, rng *rand.Rand) error {
	if trials <= 0 {
		trials = DefaultSampleTrials
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(0x5eed))
	}
	secret, err := VerifyCertificate(p, c)
	if err != nil {
		return err
	}
	combined, err := combine(p, c)
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(p.Secrets)+len(p.Randoms))
	ids = append(ids, p.Secrets...)
	ids = append(ids, p.Randoms...)

	for t := 0; t < trials; t++ {
		// Independent draw per identity; collisions in value do not merge
		// identities because evaluation is keyed by identity below.
		value := make(map[string]*big.Rat, len(ids))
		for _, id := range ids {
			value[id] = big.NewRat(int64(rng.Intn(2*sampleBound+1))-sampleBound, 1)
		}
		got := eval(combined, value)
		want := eval(secret, value)
		if got.Cmp(want) != 0 {
			return fmt.Errorf("randdep: sample verification failed at trial %d: combined=%s secret=%s", t, got.RatString(), want.RatString())
		}
	}
	return nil
}

// eval evaluates an expression under a per-identity assignment. Identities
// missing from the assignment evaluate to zero.
func eval(e Expr, value map[string]*big.Rat) *big.Rat {
	acc := new(big.Rat)
	for id, k := range e.Terms {
		if id == "" {
			acc.Add(acc, k)
			continue
		}
		if v, ok := value[id]; ok {
			acc.Add(acc, new(big.Rat).Mul(k, v))
		}
	}
	return acc
}
