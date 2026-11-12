package obsmerge

import (
	"reflect"
	"sort"
)

// Fold replays observations from scratch into resolved records without keeping
// incremental engine state. It is deterministic: equal Seq values are ordered
// by observation ID. The returned records contain every field covered by at
// least one observation, each with its full source provenance.
func Fold[T any](priority PriorityFunc, observations []Observation[T]) (map[string]RecordView[T], error) {
	if priority == nil {
		priority = StaticPriority(0, nil)
	}

	ordered := append([]Observation[T](nil), observations...)
	for i := range ordered {
		if err := validate(ordered[i]); err != nil {
			return nil, err
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Seq != ordered[j].Seq {
			return ordered[i].Seq < ordered[j].Seq
		}
		return ordered[i].ID < ordered[j].ID
	})

	// key -> field -> source -> latest evidence from that source
	type slot struct {
		evidence map[string]Evidence[T]
	}
	states := make(map[string]map[string]*slot)

	for _, obs := range ordered {
		fields := states[obs.BusinessKey]
		if fields == nil {
			fields = make(map[string]*slot)
			states[obs.BusinessKey] = fields
		}
		for _, field := range obs.CoveredFields {
			sl := fields[field]
			if sl == nil {
				sl = &slot{evidence: make(map[string]Evidence[T])}
				fields[field] = sl
			}
			n := obs.Fields[field]
			// Same-source supersession: newest Seq wins within one source.
			sl.evidence[obs.SourceID] = Evidence[T]{
				SourceID:      obs.SourceID,
				SourceVersion: obs.SourceVersion,
				PageType:      obs.PageType,
				ObservationID: obs.ID,
				Seq:           obs.Seq,
				Priority:      priority(obs.SourceID, field),
				Null:          n.Null,
				Value:         n.Value,
			}
		}
	}

	out := make(map[string]RecordView[T], len(states))
	for key, fields := range states {
		rec := RecordView[T]{BusinessKey: key, Fields: make(map[string]FieldView[T])}
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fs := &fieldState[T]{evidence: mapsValues(fields[name].evidence)}
			rec.Fields[name] = resolveField[T](name, fs)
		}
		out[key] = rec
	}
	return out, nil
}

func mapsValues[T any](m map[string]Evidence[T]) []Evidence[T] {
	out := make([]Evidence[T], 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// FieldDiff describes one field whose incremental and replayed results differ.
type FieldDiff[T any] struct {
	BusinessKey string
	Field       string
	Incremental FieldView[T]
	Replayed    FieldView[T]
}

// VerifyReport is the outcome of replaying every retained observation.
type VerifyReport[T any] struct {
	ObservationCount int
	RecordCount      int
	Match            bool
	Diffs            []FieldDiff[T]
	// Records is the full replay output, including per-field provenance.
	Records map[string]RecordView[T]
}

// ReplayAndVerify recomputes every retained observation from scratch and
// compares the result with the engine's incremental state. When Match is true
// the two are identical. Every output field in Records carries its sources.
func (e *Engine[T]) ReplayAndVerify() (VerifyReport[T], error) {
	e.mu.RLock()
	all := make([]Observation[T], 0, len(e.obs))
	for _, obs := range e.obs {
		all = append(all, obs)
	}
	priority := e.priority
	e.mu.RUnlock()

	replayed, err := Fold(priority, all)
	if err != nil {
		return VerifyReport[T]{}, err
	}

	incremental := make(map[string]RecordView[T])
	for _, rec := range e.Snapshot() {
		incremental[rec.BusinessKey] = rec
	}

	report := VerifyReport[T]{
		ObservationCount: len(all),
		RecordCount:      len(replayed),
		Match:            true,
		Records:          replayed,
	}

	keys := make(map[string]struct{}, len(replayed)+len(incremental))
	for k := range replayed {
		keys[k] = struct{}{}
	}
	for k := range incremental {
		keys[k] = struct{}{}
	}

	keyList := make([]string, 0, len(keys))
	for k := range keys {
		keyList = append(keyList, k)
	}
	sort.Strings(keyList)

	for _, key := range keyList {
		incRec, incOK := incremental[key]
		repRec, repOK := replayed[key]

		fieldNames := make(map[string]struct{})
		if incOK {
			for f := range incRec.Fields {
				fieldNames[f] = struct{}{}
			}
		}
		if repOK {
			for f := range repRec.Fields {
				fieldNames[f] = struct{}{}
			}
		}

		names := make([]string, 0, len(fieldNames))
		for f := range fieldNames {
			names = append(names, f)
		}
		sort.Strings(names)

		for _, name := range names {
			var incView, repView FieldView[T]
			if incOK {
				incView = incRec.Fields[name]
			}
			if repOK {
				repView = repRec.Fields[name]
			}
			if incView.Status == "" {
				incView = FieldView[T]{Field: name, Status: StatusUnknown}
			}
			if repView.Status == "" {
				repView = FieldView[T]{Field: name, Status: StatusUnknown}
			}
			if !fieldViewsEqual(incView, repView) {
				report.Match = false
				report.Diffs = append(report.Diffs, FieldDiff[T]{
					BusinessKey: key,
					Field:       name,
					Incremental: incView,
					Replayed:    repView,
				})
			}
		}
	}

	sort.Slice(report.Diffs, func(i, j int) bool {
		if report.Diffs[i].BusinessKey != report.Diffs[j].BusinessKey {
			return report.Diffs[i].BusinessKey < report.Diffs[j].BusinessKey
		}
		return report.Diffs[i].Field < report.Diffs[j].Field
	})
	return report, nil
}

func fieldViewsEqual[T any](a, b FieldView[T]) bool {
	if a.Field != b.Field || a.Status != b.Status || !reflect.DeepEqual(a.Value, b.Value) {
		return false
	}
	return reflect.DeepEqual(a.Winners, b.Winners) &&
		reflect.DeepEqual(a.Sources, b.Sources) &&
		reflect.DeepEqual(a.Candidates, b.Candidates)
}
