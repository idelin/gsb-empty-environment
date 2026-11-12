package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Predicate is one concrete, independently sufficient fact that lets an
// attacker tell the two observations apart. A report is a minimal set of
// these predicates: observing any one of them is enough to distinguish.
type Predicate struct {
	// Field is the machine-readable selector of the differing field. It
	// matches the path namespace used by FieldSelector (Field* constants and
	// the SelectHeader* / SelectStage constructors).
	Field string
	// Detail is a human-readable description of the mismatch.
	Detail string
}

// Report is the outcome of comparing two observations.
type Report struct {
	Left           string
	Right          string
	Distinguishers []Predicate
	Ignored        []Predicate
	Errors         []string
}

// Indistinguishable reports that every externally observable dimension
// matches after applying the allowlist.
func (r Report) Indistinguishable() bool {
	return len(r.Distinguishers) == 0
}

// MinimalCondition returns the minimal distinguishing condition: a
// disjunction of field-level predicates, any one of which separates the two
// observations. It is empty when the observations are indistinguishable.
func (r Report) MinimalCondition() []Predicate {
	return append([]Predicate(nil), r.Distinguishers...)
}

// Compare checks two observations for externally visible differences.
//
// Every dimension is compared independently: status code, headers (with
// multiplicity and order), body template, raw and compressed length bands,
// and per-stage timing tiers. A difference whose Field is covered by allowed
// is recorded in Ignored instead of Distinguishers; every other difference
// becomes a minimal distinguishing predicate.
func Compare(left, right Observation, allowed []FieldSelector) Report {
	report := Report{
		Left:  labelOr(left.Label, "left"),
		Right: labelOr(right.Label, "right"),
	}
	allow := newAllowlist(allowed)

	if left.Status != right.Status {
		report.add(allow, FieldStatus, fmt.Sprintf(
			"status code %d (%s) != %d (%s)",
			left.Status, report.Left, right.Status, report.Right))
	}

	report.compareHeaders(allow, left.Headers, right.Headers)
	report.compareBody(allow, left.Body, right.Body)
	report.compareTiming(allow, left.Timing, right.Timing)

	return report
}

func labelOr(label, fallback string) string {
	if strings.TrimSpace(label) == "" {
		return fallback
	}
	return label
}

type allowlist struct {
	exact  map[string]bool
	prefix []string
}

func newAllowlist(selectors []FieldSelector) allowlist {
	al := allowlist{exact: make(map[string]bool)}
	for _, selector := range selectors {
		path := selector.path
		switch path {
		case FieldHeaders, FieldTiming:
			al.prefix = append(al.prefix, path+".")
		case FieldBody:
			al.prefix = append(al.prefix, path)
		default:
			al.exact[path] = true
		}
	}
	sort.Strings(al.prefix)
	return al
}

func (al allowlist) permits(path string) bool {
	if al.exact[path] {
		return true
	}
	for _, prefix := range al.prefix {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// A whole-header selector (headers.Name) also covers indexed
	// occurrences such as headers.Name[0].
	if field, ok := splitHeaderIndex(path); ok {
		if al.exact[headerPrefix(field)] {
			return true
		}
	}
	return false
}

func splitHeaderIndex(path string) (string, bool) {
	bracket := strings.IndexByte(path, '[')
	if bracket <= 0 || !strings.HasPrefix(path, FieldHeaders+".") {
		return "", false
	}
	if len(path) < 2 || path[len(path)-1] != ']' {
		return "", false
	}
	if _, err := strconv.Atoi(path[bracket+1 : len(path)-1]); err != nil {
		return "", false
	}
	return path[len(FieldHeaders)+1 : bracket], true
}

func (r *Report) add(al allowlist, field, detail string) {
	predicate := Predicate{Field: field, Detail: detail}
	if al.permits(field) {
		r.Ignored = append(r.Ignored, predicate)
		return
	}
	r.Distinguishers = append(r.Distinguishers, predicate)
}

func (r *Report) compareHeaders(al allowlist, left, right []HeaderView) {
	leftViews := normalizeHeaders(left)
	rightViews := normalizeHeaders(right)

	leftByName := indexHeaders(leftViews)
	rightByName := indexHeaders(rightViews)

	names := make(map[string]bool, len(leftByName)+len(rightByName))
	for name := range leftByName {
		names[name] = true
	}
	for name := range rightByName {
		names[name] = true
	}

	for _, name := range sortedNames(names) {
		path := headerPrefix(name)
		lv, lok := leftByName[name]
		rv, rok := rightByName[name]
		switch {
		case lok && !rok:
			r.add(al, path, fmt.Sprintf(
				"header %q present in %s (%d value(s)) but absent in %s",
				name, r.Left, len(lv.Values), r.Right))
		case !lok && rok:
			r.add(al, path, fmt.Sprintf(
				"header %q present in %s (%d value(s)) but absent in %s",
				name, r.Right, len(rv.Values), r.Left))
		default:
			r.compareHeaderValues(al, name, lv.Values, rv.Values)
		}
	}
}

func (r *Report) compareHeaderValues(al allowlist, name string, left, right []string) {
	path := headerPrefix(name)
	if len(left) != len(right) {
		r.add(al, path, fmt.Sprintf(
			"header %q multiplicity %d (%s) != %d (%s)",
			name, len(left), r.Left, len(right), r.Right))
		return
	}
	for index := range left {
		if left[index] == right[index] {
			continue
		}
		r.add(al, headerIndexPath(name, index), fmt.Sprintf(
			"header %q value #%d: %q (%s) != %q (%s)",
			name, index, left[index], r.Left, right[index], r.Right))
	}
}

func (r *Report) compareBody(al allowlist, left, right BodyView) {
	if left.Template != right.Template {
		r.add(al, FieldBody, fmt.Sprintf(
			"body template %q (%s) != %q (%s)",
			left.Template, r.Left, right.Template, r.Right))
	}

	if err := checkInterval(FieldLength, left.Length, right.Length); err != "" {
		r.Errors = append(r.Errors, err)
	} else if !left.Length.Overlaps(right.Length) {
		r.add(al, FieldLength, fmt.Sprintf(
			"body length bands %s (%s) and %s (%s) never overlap",
			formatInt64Interval(left.Length), r.Left,
			formatInt64Interval(right.Length), r.Right))
	}

	r.compareCompressed(al, left.Compressed, right.Compressed)
}

func (r *Report) compareCompressed(al allowlist, left, right *Int64Interval) {
	switch {
	case left == nil && right == nil:
		return
	case left == nil || right == nil:
		r.add(al, FieldCompressed, fmt.Sprintf(
			"compressed length observed in only one side: %t (%s) vs %t (%s)",
			left != nil, r.Left, right != nil, r.Right))
		return
	}

	if err := checkInterval(FieldCompressed, *left, *right); err != "" {
		r.Errors = append(r.Errors, err)
		return
	}
	if !left.Overlaps(*right) {
		r.add(al, FieldCompressed, fmt.Sprintf(
			"compressed length bands %s (%s) and %s (%s) never overlap",
			formatInt64Interval(*left), r.Left,
			formatInt64Interval(*right), r.Right))
	}
}

func (r *Report) compareTiming(al allowlist, left, right TimingProfile) {
	stages := make(map[string]bool, len(left)+len(right))
	for stage := range left {
		stages[stage] = true
	}
	for stage := range right {
		stages[stage] = true
	}

	for _, stage := range sortedNames(stages) {
		path := FieldTiming + "." + stage
		lv, lok := left[stage]
		rv, rok := right[stage]
		switch {
		case lok && !rok:
			r.add(al, path, fmt.Sprintf(
				"timing stage %q measured in %s but missing in %s",
				stage, r.Left, r.Right))
		case !lok && rok:
			r.add(al, path, fmt.Sprintf(
				"timing stage %q measured in %s but missing in %s",
				stage, r.Right, r.Left))
		default:
			if detail, differs := r.timingDetail(stage, lv, rv); differs {
				r.add(al, path, detail)
			}
		}
	}
}

func (r *Report) timingDetail(stage string, left, right TimingTier) (string, bool) {
	if left.TimedOut != right.TimedOut {
		side := func(tier TimingTier) string {
			if tier.TimedOut {
				return "timeout"
			}
			return "completed within " + formatDurationRange(tier.Range)
		}
		return fmt.Sprintf("stage %q: %s (%s) vs %s (%s)",
			stage, side(left), r.Left, side(right), r.Right), true
	}
	if left.TimedOut && right.TimedOut {
		return "", false
	}
	if !left.Range.Overlaps(right.Range) {
		return fmt.Sprintf("stage %q: timing bands %s and %s never overlap",
			stage, formatDurationRange(left.Range),
			formatDurationRange(right.Range)), true
	}
	return "", false
}

func checkInterval(field string, intervals ...Int64Interval) string {
	for _, interval := range intervals {
		if !interval.Valid() {
			return fmt.Sprintf("invalid %s interval: min %d exceeds max %d",
				field, interval.Min, interval.Max)
		}
	}
	return ""
}

func normalizeHeaders(views []HeaderView) []HeaderView {
	normalized := make([]HeaderView, len(views))
	for index, view := range views {
		name := strings.TrimSpace(view.Name)
		values := append([]string(nil), view.Values...)
		normalized[index] = HeaderView{
			Name:   httpCanonical(name),
			Values: values,
		}
	}
	sortHeaders(normalized)
	return normalized
}

func indexHeaders(views []HeaderView) map[string]HeaderView {
	indexed := make(map[string]HeaderView, len(views))
	for _, view := range views {
		indexed[view.Name] = view
	}
	return indexed
}

func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortHeaders(views []HeaderView) {
	sort.SliceStable(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})
}

func formatInt64Interval(interval Int64Interval) string {
	if interval.Min == interval.Max {
		return strconv.FormatInt(interval.Min, 10)
	}
	return "[" + strconv.FormatInt(interval.Min, 10) +
		"," + strconv.FormatInt(interval.Max, 10) + "]"
}

func formatDurationRange(r DurationRange) string {
	if r.Min == r.Max {
		return r.Min.String()
	}
	return "[" + r.Min.String() + "," + r.Max.String() + "]"
}

func writeInt(b *strings.Builder, value int64) {
	b.WriteString(strconv.FormatInt(value, 10))
}
