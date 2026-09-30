package spaneval

// Replay folds an event log into spans and trace attributes. Span identity
// is stable: a repeated span_start for an existing span ID is ignored, so
// replaying the same span never improves completeness.
func Replay(events []Event) (spans map[string]*Span, order []*Span, attrs map[string]string, anomalies int) {
	spans = map[string]*Span{}
	attrs = map[string]string{}
	for _, ev := range events {
		switch ev.Type {
		case "trace_attrs":
			for k, v := range ev.Attrs {
				attrs[k] = v
			}
		case "span_start":
			if ev.SpanID == "" {
				anomalies++
				continue
			}
			if _, dup := spans[ev.SpanID]; dup {
				anomalies++ // same span emitted twice: counted once
				continue
			}
			sp := &Span{
				TraceID:  ev.TraceID,
				SpanID:   ev.SpanID,
				ParentID: ev.ParentID,
				Name:     ev.Name,
				Start:    ev.TS,
				Attrs:    ev.Attrs,
			}
			spans[sp.SpanID] = sp
			order = append(order, sp)
		case "span_end":
			sp, ok := spans[ev.SpanID]
			if !ok {
				anomalies++
				continue
			}
			sp.Ended = true
			sp.End = ev.TS
		default:
			anomalies++
		}
	}
	return spans, order, attrs, anomalies
}
