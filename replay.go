package main

import "sort"

type partialSpan struct {
	span     Span
	hasStart bool
	hasEnd   bool
}

// Replay consumes locally synthesized telemetry events in delivery order.
// Start/end pairing is a stream error; invalid timestamps and causal semantics
// are reported by Analyze after a complete trace has been assembled.
func Replay(events []Event) (map[string][]Span, []error) {
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := order[a], order[b]
		if events[x].At != events[y].At {
			return events[x].At < events[y].At
		}
		return x < y
	})

	builders := make(map[string]map[string]*partialSpan)
	traceOrder := make([]string, 0)
	var errs []error

	addError := func(idx int, traceID, msg string) {
		errs = append(errs, ReplayError{Index: idx, TraceID: traceID, Message: msg})
	}
	builder := func(event Event) *partialSpan {
		spans := builders[event.TraceID]
		if spans == nil {
			spans = make(map[string]*partialSpan)
			builders[event.TraceID] = spans
			traceOrder = append(traceOrder, event.TraceID)
		}
		b := spans[event.SpanID]
		if b == nil {
			b = &partialSpan{span: Span{TraceID: event.TraceID, ID: event.SpanID}}
			spans[event.SpanID] = b
		}
		return b
	}

	for _, idx := range order {
		event := events[idx]
		if event.TraceID == "" {
			addError(idx, "", "missing trace id")
			continue
		}
		if event.SpanID == "" {
			addError(idx, event.TraceID, "missing span id")
			continue
		}

		switch event.Kind {
		case EventStart:
			b := builder(event)
			if b.hasStart {
				addError(idx, event.TraceID, "duplicate start for span "+event.SpanID)
				continue
			}
			b.hasStart = true
			b.span.Start = event.Start
		case EventEnd:
			b := builder(event)
			if !b.hasStart {
				addError(idx, event.TraceID, "end without start for span "+event.SpanID)
				continue
			}
			if b.hasEnd {
				addError(idx, event.TraceID, "duplicate end for span "+event.SpanID)
				continue
			}
			b.hasEnd = true
			b.span.End = event.End
		case EventReference:
			if event.RefType != RefChildOf && event.RefType != RefFollowsFrom {
				addError(idx, event.TraceID, "unknown reference type "+event.RefType+" for span "+event.SpanID)
				continue
			}
			if event.ParentSpanID == "" {
				addError(idx, event.TraceID, "reference for span "+event.SpanID+" has no parent span")
				continue
			}
			b := builder(event)
			b.span.References = append(b.span.References, Reference{
				Type:   event.RefType,
				Parent: event.ParentSpanID,
			})
		default:
			addError(idx, event.TraceID, "unknown event kind "+event.Kind)
		}
	}

	traces := make(map[string][]Span, len(traceOrder))
	for _, traceID := range traceOrder {
		var spans []Span
		for _, b := range builders[traceID] {
			if !b.hasStart || !b.hasEnd {
				addError(-1, traceID, "incomplete span "+b.span.ID)
				continue
			}
			spans = append(spans, b.span)
		}
		sort.SliceStable(spans, func(a, b int) bool {
			if spans[a].Start != spans[b].Start {
				return spans[a].Start < spans[b].Start
			}
			return spans[a].ID < spans[b].ID
		})
		traces[traceID] = spans
	}

	return traces, errs
}
