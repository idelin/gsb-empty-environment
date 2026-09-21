package bridge

import (
	"fmt"
	"sort"
	"strings"
)

// DiffKind classifies one difference-report entry.
type DiffKind int

const (
	DiffLost DiffKind = iota
	DiffChanged
	DiffLostUnknown
	DiffExtra
	DiffInfo
)

func (k DiffKind) String() string {
	switch k {
	case DiffLost:
		return "LOST"
	case DiffChanged:
		return "CHANGED"
	case DiffLostUnknown:
		return "LOST-UNKNOWN"
	case DiffExtra:
		return "EXTRA"
	case DiffInfo:
		return "INFO"
	}
	return "?"
}

// DiffEntry is one line of a difference report.
type DiffEntry struct {
	FieldID uint32
	Kind    DiffKind
	Detail  string
}

// DiffReport compares an original message with a round-tripped one.
type DiffReport struct {
	Entries []DiffEntry
}

func (r *DiffReport) add(id uint32, kind DiffKind, detail string) {
	r.Entries = append(r.Entries, DiffEntry{FieldID: id, Kind: kind, Detail: detail})
}

// Clean is true when nothing was lost or changed (INFO/EXTRA entries are
// informational and do not dirty the report).
func (r DiffReport) Clean() bool {
	for _, e := range r.Entries {
		if e.Kind == DiffLost || e.Kind == DiffChanged || e.Kind == DiffLostUnknown {
			return false
		}
	}
	return true
}

func (r DiffReport) String() string {
	if len(r.Entries) == 0 {
		return "no differences\n"
	}
	var b strings.Builder
	for _, e := range r.Entries {
		fmt.Fprintf(&b, "%s field %d: %s\n", e.Kind, e.FieldID, e.Detail)
	}
	return b.String()
}

// flattenUnknown returns the unknown fields of a message with extension
// blobs recursively unpacked, so round-trip comparison sees the logical
// unknown set rather than its transport packaging.
func flattenUnknown(m *Message) []RawField {
	var out []RawField
	var walk func(fs []RawField)
	walk = func(fs []RawField) {
		for _, f := range fs {
			if f.ID == ExtFieldID && f.Type == TyBytes {
				if inner, err := parseExtension(f.Data); err == nil {
					walk(inner)
					continue
				}
			}
			out = append(out, f)
		}
	}
	walk(m.Unknown)
	return out
}

func unknownKey(f RawField) string {
	return fmt.Sprintf("%d/%d/%x", f.ID, f.Type, f.Data)
}

// CompareMessages diffs orig against got and annotates the report with the
// conversion traces that produced got.
func CompareMessages(orig, got *Message, traces ...*Trace) DiffReport {
	var r DiffReport
	ids := make([]uint32, 0, len(orig.Values))
	for id := range orig.Values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		ov := orig.Values[id]
		gv, ok := got.Values[id]
		if !ok {
			r.add(id, DiffLost, fmt.Sprintf("value %s lost", ov))
			continue
		}
		if !ov.Equal(gv) {
			r.add(id, DiffChanged, fmt.Sprintf("%s -> %s", ov, gv))
		}
	}
	for id, gv := range got.Values {
		if _, ok := orig.Values[id]; !ok {
			r.add(id, DiffExtra, fmt.Sprintf("value %s not in original", gv))
		}
	}

	count := func(fs []RawField) map[string]int {
		m := map[string]int{}
		for _, f := range fs {
			m[unknownKey(f)]++
		}
		return m
	}
	origU, gotU := count(flattenUnknown(orig)), count(flattenUnknown(got))
	for key, n := range origU {
		if gotU[key] < n {
			var id uint32
			fmt.Sscanf(key, "%d/", &id)
			r.add(id, DiffLostUnknown, "unknown field bytes lost")
		}
	}
	for key, n := range gotU {
		if origU[key] < n {
			var id uint32
			fmt.Sscanf(key, "%d/", &id)
			r.add(id, DiffExtra, "carried in extension blob")
		}
	}

	for _, t := range traces {
		if t == nil {
			continue
		}
		for _, id := range t.ViaExtension {
			r.add(id, DiffInfo, "carried in extension blob")
		}
		for _, id := range t.Restored {
			r.add(id, DiffInfo, "restored from extension blob")
		}
		for _, id := range t.Defaulted {
			r.add(id, DiffInfo, "default injected by migration rule")
		}
	}

	sort.Slice(r.Entries, func(i, j int) bool {
		a, b := r.Entries[i], r.Entries[j]
		if a.FieldID != b.FieldID {
			return a.FieldID < b.FieldID
		}
		return a.Kind < b.Kind
	})
	return r
}

// RoundTrip converts m forth and back across a plan pair and reports the
// differences between the original and the result.
func RoundTrip(pair *PlanPair, dir Direction, m *Message) (*Message, DiffReport, error) {
	p1, p2 := pair.AB, pair.BA
	if dir == BtoA {
		p1, p2 = pair.BA, pair.AB
	}
	mid, t1, err := Convert(p1, m)
	if err != nil {
		return nil, DiffReport{}, err
	}
	back, t2, err := Convert(p2, mid)
	if err != nil {
		return nil, DiffReport{}, err
	}
	return back, CompareMessages(m, back, t1, t2), nil
}
