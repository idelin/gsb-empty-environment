package eventlog

import "fmt"

// ValidationError describes why a CompressedLog was rejected.
type ValidationError struct {
	Stage  string
	Detail string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("eventlog: validation failed at %s: %s", e.Stage, e.Detail)
}

// Validate replays a compressed log and reports whether it is a faithful
// compression of some original log. Both the per-segment original event
// summaries and the merged events are replayed from the initial summary;
// the log is accepted only if both replays agree with each other, reproduce
// every recorded observation (position, tag and value), and reproduce the
// final summary exactly.
func Validate(log CompressedLog) error {
	if !log.Initial.Valid() {
		return &ValidationError{"initial", "summary hash does not match its fields"}
	}
	if !log.Final.Valid() {
		return &ValidationError{"final", "summary hash does not match its fields"}
	}
	if len(log.Segments) != len(log.Observations)+1 {
		return &ValidationError{"structure", fmt.Sprintf(
			"%d segments for %d observations, want %d",
			len(log.Segments), len(log.Observations), len(log.Observations)+1)}
	}

	orig := NewMachine(log.Initial.Value)
	merged := NewMachine(log.Initial.Value)

	for i, seg := range log.Segments {
		nonObserve := 0
		for j, s := range seg.Originals {
			ev := s.Event()
			if ev.Kind == OpObserve {
				if i >= len(log.Observations) {
					return &ValidationError{"segment", fmt.Sprintf(
						"segment %d: observe event after the last recorded observation", i)}
				}
				if j != len(seg.Originals)-1 {
					return &ValidationError{"segment", fmt.Sprintf(
						"segment %d: observe event buried at original %d, must close the segment", i, j)}
				}
			} else {
				nonObserve++
			}
			orig.Apply(ev)
		}

		covered := 0
		for _, ev := range seg.Merged {
			if ev.Kind == OpObserve {
				return &ValidationError{"segment", fmt.Sprintf(
					"segment %d: observation leaked into merged events", i)}
			}
			merged.Apply(ev)
			covered += ev.count()
		}
		if covered != nonObserve {
			return &ValidationError{"segment", fmt.Sprintf(
				"segment %d: merged events cover %d original events, originals list %d",
				i, covered, nonObserve)}
		}
		os, ms := orig.Summary(), merged.Summary()
		if os.Value != ms.Value || os.Applied != ms.Applied {
			return &ValidationError{"segment", fmt.Sprintf(
				"segment %d: merged replay diverges from originals (value %d vs %d)",
				i, ms.Value, os.Value)}
		}

		if i < len(log.Observations) {
			recorded := orig.Observations()
			if len(recorded) != i+1 {
				return &ValidationError{"observation", fmt.Sprintf(
					"segment %d: originals replayed %d observations, want %d",
					i, len(recorded), i+1)}
			}
			if recorded[i] != log.Observations[i] {
				return &ValidationError{"observation", fmt.Sprintf(
					"observation %d recorded as %+v, originals replayed %+v",
					i, log.Observations[i], recorded[i])}
			}
		}
	}

	if recorded := orig.Observations(); len(recorded) != len(log.Observations) {
		return &ValidationError{"observation", fmt.Sprintf(
			"originals replayed %d observations, log records %d",
			len(recorded), len(log.Observations))}
	}

	os := orig.Summary()
	want := NewSummary(
		os.Value,
		log.Initial.Applied+os.Applied,
		log.Initial.Observed+os.Observed,
	)
	if log.Final != want {
		return &ValidationError{"final", fmt.Sprintf(
			"final summary %+v does not match replay %+v", log.Final, want)}
	}
	return nil
}
