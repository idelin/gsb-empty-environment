package timeproof

import "fmt"

// validate checks every input invariant before any conclusion is produced.
func validate(q *Query) error {
	if q == nil {
		return fmt.Errorf("timeproof: nil query")
	}
	if q.Nonce == "" {
		return fmt.Errorf("timeproof: empty nonce")
	}
	if q.QueryMono == nil {
		return fmt.Errorf("timeproof: nil query monotonic time")
	}
	if q.MaxFaulty < 0 {
		return fmt.Errorf("timeproof: negative MaxFaulty")
	}
	if len(q.Receipts) > 64 {
		return fmt.Errorf("timeproof: at most 64 receipts, got %d", len(q.Receipts))
	}
	if err := validateClock(q.Clock); err != nil {
		return err
	}
	lo, hi := q.Clock.coverage()
	if !withinCoverage(q.QueryMono, lo, hi) {
		return fmt.Errorf("timeproof: query instant outside clock coverage")
	}

	domains := map[string]bool{}
	for i, r := range q.Receipts {
		if err := validateReceipt(q, i, r, lo, hi); err != nil {
			return err
		}
		domains[r.Domain] = true
		if len(domains) > 12 {
			return fmt.Errorf("timeproof: at most 12 trust domains")
		}
	}
	if q.MaxFaulty > len(domains) {
		return fmt.Errorf("timeproof: MaxFaulty %d exceeds domain count %d", q.MaxFaulty, len(domains))
	}
	return nil
}

func validateReceipt(q *Query, i int, r *Receipt, covLo, covHi *Rat) error {
	if r == nil {
		return fmt.Errorf("timeproof: receipt %d is nil", i)
	}
	if r.Nonce != q.Nonce {
		return fmt.Errorf("timeproof: receipt %d nonce does not bind the query (replay/foreign response)", i)
	}
	if r.ServerID == "" || r.Domain == "" {
		return fmt.Errorf("timeproof: receipt %d missing server identity or domain", i)
	}
	if r.SendMono == nil || r.RecvMono == nil || r.ProcessLo == nil ||
		r.ProcessHi == nil || r.SignedLo == nil || r.SignedHi == nil {
		return fmt.Errorf("timeproof: receipt %d has a nil required field", i)
	}
	if r.SendMono.Cmp(r.RecvMono) > 0 {
		return fmt.Errorf("timeproof: receipt %d receive precedes send", i)
	}
	if r.ProcessLo.Sign() < 0 || r.ProcessHi.Sign() < 0 {
		return fmt.Errorf("timeproof: receipt %d negative processing time", i)
	}
	if r.ProcessLo.Cmp(r.ProcessHi) > 0 {
		return fmt.Errorf("timeproof: receipt %d processing interval reversed", i)
	}
	if r.SignedLo.Cmp(r.SignedHi) > 0 {
		return fmt.Errorf("timeproof: receipt %d signed interval reversed", i)
	}
	if r.OneWayDelay != nil && r.OneWayDelay.Sign() < 0 {
		return fmt.Errorf("timeproof: receipt %d negative one-way delay", i)
	}
	if !withinCoverage(r.SendMono, covLo, covHi) || !withinCoverage(r.RecvMono, covLo, covHi) {
		return fmt.Errorf("timeproof: receipt %d event outside clock coverage", i)
	}
	return nil
}

// Classification is the left-closed/right-open certificate window verdict.
type Classification struct {
	Verdict Verdict
	// NotBefore/NotAfter define [NotBefore, NotAfter). NotAfter is excluded.
	NotBefore *Rat
	NotAfter  *Rat
}

// Classify maps a feasible union onto a certificate validity window
// [notBefore, notAfter). Conflict is reported separately and is never treated
// as clock expiry. Exact endpoint equality uses rational comparison.
func Classify(sol *Solution, notBefore, notAfter *Rat) Classification {
	c := Classification{Verdict: VerdictUncertain, NotBefore: notBefore, NotAfter: notAfter}
	if sol == nil {
		return c
	}
	if len(sol.Conflicts) > 0 && len(sol.Outcomes) == 0 {
		c.Verdict = VerdictConflict
		return c
	}
	if sol.Truncated {
		c.Verdict = VerdictUncertain
		return c
	}

	allValid := true
	allExpired := true
	for _, it := range sol.Union {
		valid, expired := classifyInterval(it, notBefore, notAfter)
		if !valid {
			allValid = false
		}
		if !expired {
			allExpired = false
		}
	}
	switch {
	case len(sol.Union) == 0:
		c.Verdict = VerdictConflict
	case allValid:
		c.Verdict = VerdictValid
	case allExpired:
		c.Verdict = VerdictExpired
	default:
		c.Verdict = VerdictUncertain
	}
	return c
}

// classifyInterval reports whether the whole closed interval is inside the
// window and whether it is entirely outside it.
func classifyInterval(it Interval, notBefore, notAfter *Rat) (valid, expired bool) {
	// Valid requires notBefore <= lo and hi < notAfter.
	afterOK := it.Lo != nil && it.Lo.Cmp(notBefore) >= 0
	beforeOK := it.Hi != nil && it.Hi.Cmp(notAfter) < 0
	valid = afterOK && beforeOK

	// Expired means entirely outside: hi < notBefore, or lo >= notAfter.
	beforeWindow := it.Hi != nil && it.Hi.Cmp(notBefore) < 0
	afterWindow := it.Lo != nil && it.Lo.Cmp(notAfter) >= 0
	expired = beforeWindow || afterWindow
	return valid, expired
}
