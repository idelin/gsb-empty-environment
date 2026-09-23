// Package shrink reduces a failing message to a 1-minimal one while
// preserving the failure. The caller-supplied predicate is responsible for
// accepting a candidate only if it still fails with the same error class,
// which keeps the required constraint dependencies intact. Because reduction
// happens on the structured message and re-encoding is canonical, all
// derived artifacts (length prefixes, checksum) stay consistent.
package shrink

import (
	"fmt"

	"pkv/format"
)

type candidate struct {
	msg  *format.Message
	desc string
}

func clone(m *format.Message) *format.Message {
	recs := make([]format.Record, len(m.Records))
	copy(recs, m.Records)
	return &format.Message{Records: recs}
}

// candidates enumerates single simplification steps: drop one record,
// empty/halve one value, shorten one key to its first byte.
func candidates(m *format.Message) []candidate {
	var out []candidate
	for i := range m.Records {
		c := clone(m)
		c.Records = append(c.Records[:i], c.Records[i+1:]...)
		out = append(out, candidate{c, fmt.Sprintf("remove record %d", i)})
	}
	for i, r := range m.Records {
		if r.Value != "" {
			c := clone(m)
			c.Records[i].Value = ""
			out = append(out, candidate{c, fmt.Sprintf("empty value of record %d", i)})
		}
		if len(r.Value) > 1 {
			c := clone(m)
			c.Records[i].Value = r.Value[:len(r.Value)/2]
			out = append(out, candidate{c, fmt.Sprintf("halve value of record %d", i)})
		}
		if len(r.Key) > 1 {
			c := clone(m)
			c.Records[i].Key = r.Key[:1]
			out = append(out, candidate{c, fmt.Sprintf("shorten key of record %d", i)})
		}
	}
	return out
}

// Shrink greedily reduces m while fails keeps returning true, then verifies
// 1-minimality: every remaining single simplification step must make the
// failure disappear. The returned log records every accepted reduction and
// every 1-minimality check so the run is auditable and replayable.
func Shrink(m *format.Message, fails func(*format.Message) bool) (*format.Message, []string) {
	cur := clone(m)
	log := []string{fmt.Sprintf("shrink start: %d records", len(cur.Records))}
	changed := true
	for changed {
		changed = false
		for _, c := range candidates(cur) {
			if fails(c.msg) {
				log = append(log, fmt.Sprintf("shrink: %s (still failing)", c.desc))
				cur = c.msg
				changed = true
				break
			}
		}
	}
	for _, c := range candidates(cur) {
		if fails(c.msg) {
			// Cannot happen after the fixpoint above; record defensively.
			log = append(log, fmt.Sprintf("1-minimal VIOLATED: %s still fails", c.desc))
		} else {
			log = append(log, fmt.Sprintf("1-minimal check: %s -> failure gone", c.desc))
		}
	}
	log = append(log, fmt.Sprintf("1-minimal confirmed: %d records remain", len(cur.Records)))
	return cur, log
}
