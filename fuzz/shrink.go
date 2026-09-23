package fuzz

import "fmt"

// ShrinkResult holds the minimized failing input and a full check
// record, including the 1-minimality verification.
type ShrinkResult struct {
	Minimal []byte
	Checks  int
	Log     []string
}

// Shrink minimizes data while pred keeps returning true. The caller
// builds pred so that it only holds for the same error class and the
// required dependencies of the original failure, which keeps the
// shrunk sample in the same failure category. The result is verified
// 1-minimal in the delta-debugging sense: deleting any single byte no
// longer reproduces the failure. Every check is recorded in the log.
func Shrink(data []byte, pred func([]byte) bool) ShrinkResult {
	s := &shrinker{pred: pred}
	cur := append([]byte(nil), data...)
	if !s.try(cur, "initial input") {
		s.log = append(s.log, "predicate does not hold on the initial input; nothing to shrink")
		return ShrinkResult{Minimal: cur, Checks: s.checks, Log: s.log}
	}

	// Phases 1+2: chunk deletion (delta-debugging style) and byte
	// simplification, iterated to a fixpoint because simplification
	// can unlock further deletions.
	alphabet := []byte{0x00, 0x01, 0x20, 0x30, 0x41, 0x7e, 0xff}
	changed := true
	for changed {
		changed = false
		for size := len(cur) / 2; size >= 1; size /= 2 {
			for i := 0; i+size <= len(cur); {
				cand := make([]byte, 0, len(cur)-size)
				cand = append(cand, cur[:i]...)
				cand = append(cand, cur[i+size:]...)
				if s.try(cand, fmt.Sprintf("delete [%d:%d]", i, i+size)) {
					cur = cand
					changed = true
				} else {
					i += size
				}
			}
		}
		for i := 0; i < len(cur); i++ {
			for _, v := range alphabet {
				// Only accept strictly smaller byte values so
				// simplification is monotonic and terminates.
				if v >= cur[i] {
					continue
				}
				cand := append([]byte(nil), cur...)
				cand[i] = v
				if s.try(cand, fmt.Sprintf("byte[%d]=0x%02x", i, v)) {
					cur = cand
					changed = true
					break
				}
			}
		}
	}

	// Phase 3: verify 1-minimality (single-byte deletions) and record
	// every check. Substitutions of don't-care bytes may still
	// reproduce, so they are logged informationally only.
	s.log = append(s.log, "1-minimality verification:")
	oneMinimal := true
	for i := 0; i < len(cur); i++ {
		cand := make([]byte, 0, len(cur)-1)
		cand = append(cand, cur[:i]...)
		cand = append(cand, cur[i+1:]...)
		if s.try(cand, fmt.Sprintf("1-min delete@%d", i)) {
			oneMinimal = false
		}
	}
	if oneMinimal {
		s.log = append(s.log, "result is 1-minimal")
	} else {
		s.log = append(s.log, "WARNING: result is NOT 1-minimal")
	}
	return ShrinkResult{Minimal: cur, Checks: s.checks, Log: s.log}
}

type shrinker struct {
	pred   func([]byte) bool
	checks int
	log    []string
}

func (s *shrinker) try(cand []byte, what string) bool {
	s.checks++
	ok := s.pred(cand)
	s.log = append(s.log, fmt.Sprintf("check #%04d %-22s reproduces=%v", s.checks, what, ok))
	return ok
}
