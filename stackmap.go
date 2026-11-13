package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// LocationKind distinguishes physical register locations from stack slots.
type LocationKind uint8

const (
	LocationRegister LocationKind = iota + 1
	LocationSlot
)

func (k LocationKind) String() string {
	switch k {
	case LocationRegister:
		return "reg"
	case LocationSlot:
		return "slot"
	default:
		return fmt.Sprintf("location(%d)", k)
	}
}

// Location is a register or stack slot. Registers and slots have separate
// numbering spaces.
type Location struct {
	Kind  LocationKind
	Index int
}

// Register names a register location.
func Register(index int) Location {
	return Location{Kind: LocationRegister, Index: index}
}

// Slot names a stack slot location.
func Slot(index int) Location {
	return Location{Kind: LocationSlot, Index: index}
}

func (l Location) String() string {
	return fmt.Sprintf("%s%d", l.Kind, l.Index)
}

// StackMapEntry maps one location to a live SSA value. DerivedBase, when set,
// names the SSA value that carries the base object identity for an interior
// pointer. The offset lives in the referenced value's Ptr type; a bare address
// is never accepted as a substitute for (value, base, offset).
type StackMapEntry struct {
	Location    Location
	Value       ValueID
	DerivedBase ValueID
}

// MapEntry is a short alias for StackMapEntry.
type MapEntry = StackMapEntry

// StackMap is the candidate map submitted at one Safepoint instruction.
type StackMap struct {
	At      ValueID
	Entries []StackMapEntry
}

// CandidateStackMap is an alias used when describing submitted maps.
type CandidateStackMap = StackMap

// CandidateMap is a short alias for StackMap.
type CandidateMap = StackMap

// MapIssueKind classifies a stack-map verification failure.
type MapIssueKind uint8

const (
	IssueInvalidIR MapIssueKind = iota + 1
	IssueMissingMap
	IssueDuplicateMap
	IssueUnknownSafepoint
	IssueMissingLivePointer
	IssueNonPointer
	IssueDuplicateLocation
	IssueInvalidLocation
	IssueInvalidValue
	IssueInvalidOffset
	IssueMissingDerivedBase
	IssueInvalidDerivedBase
)

func (k MapIssueKind) String() string {
	switch k {
	case IssueInvalidIR:
		return "invalid-ir"
	case IssueMissingMap:
		return "missing-map"
	case IssueDuplicateMap:
		return "duplicate-map"
	case IssueUnknownSafepoint:
		return "unknown-safepoint"
	case IssueMissingLivePointer:
		return "missing-live-pointer"
	case IssueNonPointer:
		return "non-pointer-mapped"
	case IssueDuplicateLocation:
		return "duplicate-location"
	case IssueInvalidLocation:
		return "invalid-location"
	case IssueInvalidValue:
		return "invalid-value"
	case IssueInvalidOffset:
		return "invalid-offset"
	case IssueMissingDerivedBase:
		return "missing-derived-base"
	case IssueInvalidDerivedBase:
		return "invalid-derived-base"
	default:
		return fmt.Sprintf("issue(%d)", k)
	}
}

// MapIssue is one concrete reason a candidate map was rejected.
type MapIssue struct {
	Kind     MapIssueKind
	At       ValueID
	Location Location
	Value    ValueID
	Detail   string
}

func (i MapIssue) String() string {
	var parts []string
	if i.At != NoValue {
		parts = append(parts, fmt.Sprintf("safepoint %d", i.At))
	}
	if i.Location.Kind != 0 {
		parts = append(parts, i.Location.String())
	}
	if i.Value != NoValue {
		parts = append(parts, fmt.Sprintf("value %d", i.Value))
	}
	prefix := i.Kind.String()
	if len(parts) > 0 {
		return prefix + " (" + strings.Join(parts, ", ") + "): " + i.Detail
	}
	return prefix + ": " + i.Detail
}

// StackMapReport is the verifier result. Valid implies every Safepoint has a
// map, every live Ptr is covered, no NonPtr is mapped, every location is
// unique, and all offsets and derived-base relations are sound.
type StackMapReport struct {
	Valid    bool
	Issues   []MapIssue
	Analysis *Analysis
}

func (r *StackMapReport) add(kind MapIssueKind, at ValueID, loc Location, value ValueID, format string, args ...any) {
	r.Issues = append(r.Issues, MapIssue{
		Kind: kind, At: at, Location: loc, Value: value, Detail: fmt.Sprintf(format, args...),
	})
}

// CheckStackMaps validates candidate maps against control-flow liveness.
func CheckStackMaps(fn *Function, maps []StackMap) *StackMapReport {
	report := &StackMapReport{}
	analysis, err := Analyze(fn)
	if err != nil {
		report.add(IssueInvalidIR, NoValue, Location{}, NoValue, "%v", err)
		return report
	}
	report.Analysis = analysis
	layout := effectiveLayouts(fn)
	safepoints := make(map[ValueID]SafepointInfo, len(analysis.Safepoints))
	for _, sp := range analysis.Safepoints {
		safepoints[sp.At] = sp
	}
	provided := make(map[ValueID]bool)
	for _, m := range maps {
		if provided[m.At] {
			report.add(IssueDuplicateMap, m.At, Location{}, NoValue, "more than one candidate map at this safepoint")
			continue
		}
		provided[m.At] = true
		sp, ok := safepoints[m.At]
		if !ok {
			report.add(IssueUnknownSafepoint, m.At, Location{}, NoValue, "map is not attached to a Safepoint instruction")
			continue
		}
		seenLoc := make(map[Location]bool)
		covered := make(ValueSet)
		for _, entry := range m.Entries {
			if entry.Location.Kind != LocationRegister && entry.Location.Kind != LocationSlot {
				report.add(IssueInvalidLocation, m.At, entry.Location, entry.Value, "unknown location kind")
				continue
			}
			if entry.Location.Index < 0 {
				report.add(IssueInvalidLocation, m.At, entry.Location, entry.Value, "negative location index")
				continue
			}
			if seenLoc[entry.Location] {
				report.add(IssueDuplicateLocation, m.At, entry.Location, entry.Value, "location is listed more than once")
				continue
			}
			seenLoc[entry.Location] = true

			typ, ok := analysis.types[entry.Value]
			if !ok {
				report.add(IssueInvalidValue, m.At, entry.Location, entry.Value, "value is not a defined SSA value")
				continue
			}
			if !typ.IsPtr() {
				report.add(IssueNonPointer, m.At, entry.Location, entry.Value, "mapped value has type %s", typ)
				continue
			}
			covered.Add(entry.Value)
			validOffsets := true
			for _, object := range typ.Objects {
				size, ok := layout[object]
				if !ok {
					report.add(IssueInvalidOffset, m.At, entry.Location, entry.Value, "object site %q has no size", object)
					validOffsets = false
					continue
				}
				if !typ.Offset.Within(size) {
					report.add(
						IssueInvalidOffset, m.At, entry.Location, entry.Value,
						"offset %s is outside object %q of size %d", typ.Offset, object, size,
					)
					validOffsets = false
				}
			}

			interior := typ.Offset.Start != 0 || typ.Offset.End > 1
			if interior && entry.DerivedBase == NoValue {
				report.add(IssueMissingDerivedBase, m.At, entry.Location, entry.Value,
					"interior pointer must name its base object value and offset")
				continue
			}
			if entry.DerivedBase != NoValue {
				base, ok := analysis.types[entry.DerivedBase]
				if !ok || !base.IsPtr() {
					report.add(IssueInvalidDerivedBase, m.At, entry.Location, entry.Value,
						"derived base %d is not a defined Ptr", entry.DerivedBase)
					continue
				}
				if entry.DerivedBase == entry.Value {
					report.add(IssueInvalidDerivedBase, m.At, entry.Location, entry.Value,
						"pointer cannot be its own derived base")
					continue
				}
				if !base.Objects.Equal(typ.Objects) {
					report.add(IssueInvalidDerivedBase, m.At, entry.Location, entry.Value,
						"derived base object set %s does not match %s", base.Objects, typ.Objects)
					continue
				}
				if base.Offset.Start > typ.Offset.Start || base.Offset.End > typ.Offset.End {
					report.add(IssueInvalidDerivedBase, m.At, entry.Location, entry.Value,
						"derived base offset %s is not below pointer offset %s", base.Offset, typ.Offset)
				}
			}
			if !validOffsets {
				continue
			}
		}
		for _, live := range sp.LivePtrs.Sorted() {
			if !covered.Contains(live) {
				report.add(IssueMissingLivePointer, m.At, Location{}, live,
					"live pointer %s is not present in the candidate map", analysis.types[live])
			}
		}
	}
	for _, sp := range analysis.Safepoints {
		if !provided[sp.At] {
			report.add(IssueMissingMap, sp.At, Location{}, NoValue, "no candidate stack map was supplied")
		}
	}
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if report.Issues[i].At != report.Issues[j].At {
			return report.Issues[i].At < report.Issues[j].At
		}
		return report.Issues[i].Kind < report.Issues[j].Kind
	})
	report.Valid = len(report.Issues) == 0
	return report
}

// VerifyStackMaps is the error-returning form of CheckStackMaps.
func VerifyStackMaps(fn *Function, maps []StackMap) error {
	report := CheckStackMaps(fn, maps)
	if report.Valid {
		return nil
	}
	lines := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		lines = append(lines, issue.String())
	}
	return fmt.Errorf("netsec: invalid stack map:\n%s", strings.Join(lines, "\n"))
}

// Verify is a short alias for VerifyStackMaps.
func Verify(fn *Function, maps []StackMap) error {
	return VerifyStackMaps(fn, maps)
}

func effectiveLayouts(fn *Function) map[ObjectID]int {
	out := make(map[ObjectID]int)
	if fn == nil {
		return out
	}
	for id, size := range fn.ObjectSizes {
		out[id] = size
	}
	for _, b := range fn.Blocks {
		for _, in := range b.Instrs {
			if in.Op == OpAlloc {
				out[in.Object] = in.Size
			}
			if in.Op == OpCall && in.Summary.Return.Kind == RetFresh {
				out[in.Summary.Return.Object] = in.Summary.Return.Size
			}
		}
	}
	return out
}

// MustStackMap is a small helper for tests and examples. It panics if the
// candidate is invalid.
func MustStackMap(fn *Function, maps ...StackMap) []StackMap {
	if err := VerifyStackMaps(fn, maps); err != nil {
		panic(err)
	}
	return maps
}
