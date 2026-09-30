package spaneval

import "math/rand"

// SynthOptions perturbs synthesized traces to simulate real telemetry
// failure modes. All synthesis is local; no monitoring backend involved.
type SynthOptions struct {
	Seed      int64
	Drop      map[string]bool // skip subtrees rooted at these names (missing)
	Duplicate map[string]bool // emit one extra copy of these spans
	LeaveOpen map[string]bool // never emit span_end (unfinished)
}

// Synth walks the rule graph and synthesizes a plausible event log for the
// given trace attributes. The output can be replayed by Replay/Evaluate or
// stored as a fixture.
func Synth(root *Rule, traceID string, attrs map[string]string, opts SynthOptions) []Event {
	r := rand.New(rand.NewSource(opts.Seed))
	events := []Event{{Type: "trace_attrs", TraceID: traceID, TS: 0, Attrs: attrs}}
	var clock int64 = 1
	var seq int
	nextID := func() string { seq++; return traceID + "-s" + itoa(seq) }

	var walk func(rule *Rule, parentID string)
	walk = func(rule *Rule, parentID string) {
		if !condOK(rule.When, attrs) || opts.Drop[rule.Name] {
			return
		}
		if rule.Kind == "choice" {
			var branches []*Rule
			for _, b := range rule.Children {
				if condOK(b.When, attrs) && !opts.Drop[b.Name] {
					branches = append(branches, b)
				}
			}
			if len(branches) == 0 {
				return
			}
			walk(branches[r.Intn(len(branches))], parentID)
			return
		}
		count := rule.MinN()
		if span := rule.MaxN() - rule.MinN(); span > 0 {
			count += r.Intn(span + 1)
		}
		if opts.Duplicate[rule.Name] {
			count++
		}
		for i := 0; i < count; i++ {
			id := nextID()
			start := clock
			clock++
			events = append(events, Event{Type: "span_start", TraceID: traceID, SpanID: id, ParentID: parentID, Name: rule.Name, TS: start})
			for _, c := range rule.Children {
				walk(c, id)
			}
			clock++
			if !opts.LeaveOpen[rule.Name] {
				events = append(events, Event{Type: "span_end", TraceID: traceID, SpanID: id, TS: clock})
			}
		}
	}
	walk(root, "")
	return events
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
