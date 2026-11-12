package netsec

import (
	"slices"
	"time"
)

// validate verifies the internal consistency of a conclusion.
func (c *Conclusion) validate() error {
	if c.Key.ContentDigest == "" || c.Key.RulesVersion == "" {
		return ErrInvalidKey
	}
	for _, result := range c.Results {
		if result.Check == "" {
			return ErrInvalidCheck
		}
		switch result.Verdict {
		case VerdictNegative, VerdictPositive:
		default:
			return ErrInvalidVerdict
		}
	}
	for _, check := range c.Pending {
		if check == "" {
			return ErrInvalidCheck
		}
	}
	return nil
}

// Contains reports whether check has actually been executed in this conclusion.
func (c *Conclusion) Contains(check CheckID) bool {
	for _, result := range c.Results {
		if result.Check == check {
			return true
		}
	}
	return false
}

// Covers reports whether every requested check has been executed. Reuse of a
// cached conclusion is only permitted when Covers is true: an uncovered check
// means the requested analysis set is not fully represented and the missing
// work must not be treated as passing.
func (c *Conclusion) Covers(requested []CheckID) bool {
	for _, check := range requested {
		if !c.Contains(check) {
			return false
		}
	}
	return true
}

// Missing returns the requested checks that have not been executed. The result
// is sorted and deduplicated and never includes an executed check.
func (c *Conclusion) Missing(requested []CheckID) []CheckID {
	seen := make(map[CheckID]struct{}, len(requested))
	var missing []CheckID
	for _, check := range requested {
		if _, ok := seen[check]; ok || c.Contains(check) {
			continue
		}
		seen[check] = struct{}{}
		missing = append(missing, check)
	}
	slices.Sort(missing)
	return missing
}

// Status interprets the conclusion on its own. It never converts outstanding
// work into a pass: any pending check yields StatusIncomplete, and a positive
// result always wins over negatives.
//
// To evaluate the conclusion against a specific requested check set use
// Decision, which also marks the conclusion incomplete when requested checks
// are absent even if Pending does not name them.
func (c *Conclusion) Status() Status {
	if len(c.Pending) > 0 {
		return StatusIncomplete
	}
	for _, result := range c.Results {
		if result.Verdict == VerdictPositive {
			return StatusThreat
		}
	}
	if len(c.Results) == 0 {
		return StatusIncomplete
	}
	return StatusClean
}

// Decision reports the status of this conclusion with respect to the requested
// check set. Missing checks always produce StatusIncomplete, so a partial quick
// screen can never be read as a clean full analysis.
func (c *Conclusion) Decision(requested []CheckID) Status {
	if !c.Covers(requested) {
		return StatusIncomplete
	}
	return c.Status()
}

// clone returns a deep copy so callers cannot mutate cached state through
// returned slices.
func (c *Conclusion) clone() *Conclusion {
	if c == nil {
		return nil
	}
	copyOf := &Conclusion{
		Key:       c.Key,
		Results:   make([]CheckResult, len(c.Results)),
		Pending:   append([]CheckID(nil), c.Pending...),
		UpdatedAt: c.UpdatedAt,
	}
	for i, result := range c.Results {
		copyOf.Results[i] = CheckResult{
			Check:    result.Check,
			Verdict:  result.Verdict,
			Evidence: append([]string(nil), result.Evidence...),
			RanAt:    result.RanAt,
		}
	}
	return copyOf
}

// Merge combines two conclusions for the same content and rule version.
//
// Both conclusions must carry the same Key; merging across content digests or
// rule versions returns ErrKeyMismatch so stale data can never pollute a
// newer rule set. Merge is idempotent, associative and commutative:
//
//   - The union of executed checks is kept. When a check appears in both
//     inputs, a positive verdict always dominates a negative one, and all
//     positive evidence is retained. A negative result can therefore never
//     erase an existing finding.
//   - The pending set is the union of outstanding checks minus every check
//     that has actually executed; nothing that merely did not run is reported
//     as a pass.
//   - UpdatedAt is the most recent execution time.
func Merge(a, b *Conclusion) (*Conclusion, error) {
	if a == nil || b == nil {
		return nil, ErrInvalidKey
	}
	if a.Key != b.Key {
		return nil, ErrKeyMismatch
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	if err := b.validate(); err != nil {
		return nil, err
	}

	merged := &Conclusion{Key: a.Key, UpdatedAt: latest(a.UpdatedAt, b.UpdatedAt)}
	results := make(map[CheckID]CheckResult, len(a.Results)+len(b.Results))
	order := make([]CheckID, 0, len(a.Results)+len(b.Results))

	incorporate := func(result CheckResult) {
		existing, ok := results[result.Check]
		if !ok {
			results[result.Check] = cloneResult(result)
			order = append(order, result.Check)
			return
		}
		results[result.Check] = reconcile(existing, result)
	}

	for _, result := range a.Results {
		incorporate(result)
	}
	for _, result := range b.Results {
		incorporate(result)
	}

	merged.Results = make([]CheckResult, 0, len(order))
	for _, check := range order {
		merged.Results = append(merged.Results, results[check])
	}

	pending := make(map[CheckID]struct{})
	for _, check := range append(append([]CheckID(nil), a.Pending...), b.Pending...) {
		pending[check] = struct{}{}
	}
	for _, result := range merged.Results {
		delete(pending, result.Check)
	}
	merged.Pending = make([]CheckID, 0, len(pending))
	for check := range pending {
		merged.Pending = append(merged.Pending, check)
	}
	slices.Sort(merged.Pending)

	if err := merged.validate(); err != nil {
		return nil, err
	}
	return merged, nil
}

// reconcile combines two results for the same check. Positive evidence is
// accumulated and a negative verdict can never overwrite a positive one.
func reconcile(existing, incoming CheckResult) CheckResult {
	out := cloneResult(existing)
	if incoming.RanAt.After(out.RanAt) {
		out.RanAt = incoming.RanAt
	}
	if existing.Verdict == VerdictPositive || incoming.Verdict == VerdictPositive {
		out.Verdict = VerdictPositive
		out.Evidence = unionEvidence(existing.Evidence, incoming.Evidence)
	}
	return out
}

func unionEvidence(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, item := range append(append([]string(nil), a...), b...) {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func cloneResult(result CheckResult) CheckResult {
	result.Evidence = append([]string(nil), result.Evidence...)
	return result
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
