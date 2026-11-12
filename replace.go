package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const replaceRefPrefix = "refs/replace/"

// Ref is a Git reference as observed by the admission service. Target is the
// object ID to which the reference points. For the pseudo-reference
// "info/grafts", Target contains the raw grafts file.
type Ref struct {
	Name   string
	Target string
}

// Reference is an alias for Ref.
type Reference = Ref

// ReplaceRef is an alias for Ref.
type ReplaceRef = Ref

// Graft is one effective record from info/grafts. It rewrites Commit's parent
// list to Parents.
type Graft struct {
	Commit  string
	Parents []string
}

// Input is the typed form of the admission check.
//
// Graph contains each commit's true parent commits. Signed contains only
// commits for which signature verification succeeded. Grafts contains parsed
// info/grafts records; GraftContents contains the raw file. Ref entries whose
// name is "info/grafts" are also interpreted as raw grafts content.
type Input struct {
	Refs          []Ref
	Graph         map[string][]string
	Signed        map[string]bool
	Grafts        []Graft
	GraftContents string
	AllowReplace  bool
}

// ReplacementKind identifies the source of a replacement.
type ReplacementKind string

const (
	// KindReplaceRef is a refs/replace/<original> reference.
	KindReplaceRef ReplacementKind = "replace-ref"
	// KindGraft is an effective info/grafts record.
	KindGraft ReplacementKind = "graft"
)

// Replacement records one replacement discovered by the checker.
type Replacement struct {
	Kind           ReplacementKind
	Source         string
	Original       string
	Target         string
	TargetVerified bool
	Allowed        bool
}

// StringSet is a set of object IDs.
type StringSet map[string]bool

// Set is an alias for StringSet.
type Set = StringSet

// Contains reports whether id is in the set.
func (s StringSet) Contains(id string) bool {
	return s[id]
}

// IDs returns the members in lexical order.
func (s StringSet) IDs() []string {
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Result is the outcome of a replace check.
type Result struct {
	// Affected is every replacement original and all of its descendants,
	// including replacements that were explicitly allowed.
	Affected StringSet
	// Blocked is every replacement original and descendant that remains
	// untrusted after applying allowReplace.
	Blocked StringSet
	// Passed contains signed commits that are not blocked.
	Passed StringSet
	// Replacements lists every effective refs/replace or graft record.
	Replacements []Replacement
	// Allowed is true when no commit remains blocked.
	Allowed bool
	// Err reports malformed input. Invalid input fails closed.
	Err error
}

// Report is an alias for Result.
type Report = Result

// OK reports whether the check completed without error and no commit is
// blocked.
func (r Result) OK() bool {
	return r.Err == nil && r.Allowed
}

// AffectedIDs returns affected object IDs in lexical order.
func (r Result) AffectedIDs() []string {
	return r.Affected.IDs()
}

// BlockedIDs returns blocked object IDs in lexical order.
func (r Result) BlockedIDs() []string {
	return r.Blocked.IDs()
}

// PassedIDs returns signed and unblocked object IDs in lexical order.
func (r Result) PassedIDs() []string {
	return r.Passed.IDs()
}

// Inspect runs the replace check with explicit inputs. It never follows a
// replacement target while computing descendants.
func Inspect(in Input) Result {
	signed := normalizeSigned(in.Signed)
	var malformed []string
	grafts := append([]Graft(nil), in.Grafts...)
	if in.GraftContents != "" {
		parsed, err := ParseGrafts(in.GraftContents)
		if err != nil {
			malformed = append(malformed, "info/grafts")
		} else {
			grafts = append(grafts, parsed...)
		}
	}

	replacements := collectRefReplacements(in.Refs, signed, in.AllowReplace, &malformed)

	for _, ref := range in.Refs {
		if isGraftsPseudoRef(ref.Name) {
			parsed, err := ParseGrafts(ref.Target)
			if err != nil {
				malformed = append(malformed, ref.Name)
			} else {
				grafts = append(grafts, parsed...)
			}
			continue
		}
		if original, ok := graftRefOriginal(ref.Name); ok {
			grafts = append(grafts, Graft{
				Commit:  original,
				Parents: splitIDs(ref.Target),
			})
		}
	}

	replacements = append(replacements, graftReplacements(grafts)...)

	roots := make([]string, 0, len(replacements))
	blockedRoots := make([]string, 0, len(replacements))
	for _, replacement := range replacements {
		if replacement.Original == "" {
			continue
		}
		roots = append(roots, replacement.Original)
		if !replacement.Allowed {
			blockedRoots = append(blockedRoots, replacement.Original)
		}
	}

	affected := descendants(in.Graph, roots)
	blocked := descendants(in.Graph, blockedRoots)

	passed := make(StringSet)
	for id, ok := range signed {
		if ok && !blocked[id] {
			passed[id] = true
		}
	}

	var resultErr error
	if len(malformed) > 0 {
		// A malformed refs/replace path cannot name a trustworthy original.
		// Fail closed over every object visible to the checker.
		passed = make(StringSet)
		for id := range allObjects(in.Graph) {
			affected[id] = true
			blocked[id] = true
		}
		sort.Strings(malformed)
		resultErr = fmt.Errorf("malformed replacement input: %s", strings.Join(malformed, ", "))
	}

	return Result{
		Affected:     affected,
		Blocked:      blocked,
		Passed:       passed,
		Replacements: replacements,
		Allowed:      resultErr == nil && len(blocked) == 0,
		Err:          resultErr,
	}
}

// Analyze accepts the admission inputs in any order. Supported argument types
// are []Ref (or map[string]string), a commit graph, a signed set, info/grafts
// records or contents, a bool for allowReplace, and an Input value. This is
// the convenient entry point for callers assembled from different pipeline
// components.
//
// The usual call is:
//
//	Analyze(refs, graph, signed, allowReplace)
//
// A grafts file can be added as a string or []Graft argument.
func Analyze(args ...any) Result {
	in, err := parseInput(args)
	if err != nil {
		return failedResult(err)
	}
	return Inspect(in)
}

// Check is an alias for Analyze.
func Check(args ...any) Result {
	return Analyze(args...)
}

// Scan is an alias for Analyze.
func Scan(args ...any) Result {
	return Analyze(args...)
}

// Verify reports whether the repository passes the replace check.
func Verify(args ...any) bool {
	return Analyze(args...).OK()
}

// AffectedCommits returns the affected set without the rest of the report.
func AffectedCommits(args ...any) StringSet {
	return Analyze(args...).Affected
}

// FindAffectedCommits returns the affected set without the rest of the report.
func FindAffectedCommits(args ...any) StringSet {
	return AffectedCommits(args...)
}

// BlockedCommits returns the blocked set without the rest of the report.
func BlockedCommits(args ...any) StringSet {
	return Analyze(args...).Blocked
}

// FindBlockedCommits returns the blocked set without the rest of the report.
func FindBlockedCommits(args ...any) StringSet {
	return BlockedCommits(args...)
}

// AffectedIDs returns affected object IDs in lexical order.
func AffectedIDs(args ...any) []string {
	return Analyze(args...).AffectedIDs()
}

// BlockedIDs returns blocked object IDs in lexical order.
func BlockedIDs(args ...any) []string {
	return Analyze(args...).BlockedIDs()
}

// ParseGrafts parses info/grafts content. Blank lines and lines beginning with
// '#' are ignored. A line with only an object ID describes a root commit.
func ParseGrafts(contents string) ([]Graft, error) {
	if strings.IndexByte(contents, 0) >= 0 {
		return nil, errors.New("info/grafts contains a NUL byte")
	}
	return parseGrafts(contents), nil
}

// GraftsFrom is ParseGrafts without the error return. It is suitable for
// display and diagnostics; admission checks should use Inspect so that malformed
// graft input can fail closed.
func GraftsFrom(contents string) []Graft {
	return ParseGraftsBestEffort(contents)
}

// ParseGraftsBestEffort parses grafts and returns every identifiable record.
func ParseGraftsBestEffort(contents string) []Graft {
	grafts, _ := ParseGrafts(contents)
	return grafts
}

func parseGrafts(contents string) []Graft {
	var grafts []Graft
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		graft := Graft{Commit: fields[0]}
		if len(fields) > 1 {
			graft.Parents = append([]string(nil), fields[1:]...)
		}
		grafts = append(grafts, graft)
	}
	return grafts
}

func collectRefReplacements(refs []Ref, signed map[string]bool, allow bool, malformed *[]string) []Replacement {
	var replacements []Replacement
	for _, ref := range refs {
		if !strings.HasPrefix(ref.Name, replaceRefPrefix) {
			continue
		}
		original := strings.TrimPrefix(ref.Name, replaceRefPrefix)
		target := strings.TrimSpace(ref.Target)
		if original == "" {
			*malformed = append(*malformed, ref.Name)
			replacements = append(replacements, Replacement{
				Kind:     KindReplaceRef,
				Source:   ref.Name,
				Target:   target,
				Allowed:  false,
				Original: "",
			})
			continue
		}
		targetVerified := target != "" && signed[target]
		replacements = append(replacements, Replacement{
			Kind:           KindReplaceRef,
			Source:         ref.Name,
			Original:       original,
			Target:         target,
			TargetVerified: targetVerified,
			Allowed:        allow && targetVerified,
		})
	}
	return replacements
}

func graftReplacements(grafts []Graft) []Replacement {
	replacements := make([]Replacement, 0, len(grafts))
	for _, graft := range grafts {
		commit := strings.TrimSpace(graft.Commit)
		if commit == "" {
			continue
		}
		replacements = append(replacements, Replacement{
			Kind:     KindGraft,
			Source:   "info/grafts",
			Original: commit,
			Allowed:  false,
		})
	}
	return replacements
}

func descendants(graph map[string][]string, roots []string) StringSet {
	if len(roots) == 0 {
		return make(StringSet)
	}
	reverse := make(map[string][]string, len(graph))
	for child, parents := range graph {
		for _, parent := range parents {
			if parent == "" {
				continue
			}
			reverse[parent] = append(reverse[parent], child)
		}
	}

	out := make(StringSet)
	queue := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" || out[root] {
			continue
		}
		out[root] = true
		queue = append(queue, root)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, child := range reverse[id] {
			if out[child] {
				continue
			}
			out[child] = true
			queue = append(queue, child)
		}
	}
	return out
}

func allObjects(graph map[string][]string) StringSet {
	out := make(StringSet)
	for id, parents := range graph {
		out[id] = true
		for _, parent := range parents {
			if parent != "" {
				out[parent] = true
			}
		}
	}
	return out
}

func isGraftsPseudoRef(name string) bool {
	if strings.HasPrefix(name, "refs/") {
		return false
	}
	return name == "info/grafts" ||
		name == "grafts" ||
		strings.HasSuffix(name, "/info/grafts")
}

func graftRefOriginal(name string) (string, bool) {
	const prefix = "info/grafts/"
	if strings.HasPrefix(name, prefix) {
		original := strings.TrimPrefix(name, prefix)
		return original, original != ""
	}
	return "", false
}

func splitIDs(value string) []string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func normalizeSigned(signed map[string]bool) map[string]bool {
	out := make(map[string]bool, len(signed))
	for id, ok := range signed {
		if ok {
			out[id] = true
		}
	}
	return out
}

func failedResult(err error) Result {
	return Result{
		Affected: make(StringSet),
		Blocked:  make(StringSet),
		Passed:   make(StringSet),
		Allowed:  false,
		Err:      err,
	}
}

type inputParser struct {
	in       Input
	sawRefs  bool
	sawGraph bool
	sawSign  bool
	sawAllow bool
	errs     []string
}

func parseInput(args []any) (Input, error) {
	var parser inputParser
	for _, arg := range args {
		parser.add(arg)
	}
	if len(parser.errs) > 0 {
		return Input{}, errors.New(strings.Join(parser.errs, "; "))
	}
	return parser.in, nil
}

func (p *inputParser) add(arg any) {
	if arg == nil {
		return
	}
	switch value := arg.(type) {
	case bool:
		p.in.AllowReplace = value
		p.sawAllow = true
	case *bool:
		if value == nil {
			p.errs = append(p.errs, "nil allowReplace")
			return
		}
		p.in.AllowReplace = *value
		p.sawAllow = true
	case string:
		p.in.GraftContents = value
	case []Graft:
		p.in.Grafts = append(p.in.Grafts, value...)
	case Input:
		p.setInput(value)
	case *Input:
		if value == nil {
			p.errs = append(p.errs, "nil Input")
			return
		}
		p.setInput(*value)
	case []Ref:
		p.setRefs(value)
	case map[string]string:
		refs := make([]Ref, 0, len(value))
		for name, target := range value {
			refs = append(refs, Ref{Name: name, Target: target})
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
		p.setRefs(refs)
	case map[string][]string:
		p.setGraph(value)
	case map[string]bool:
		p.setSignedBool(value)
	case map[string]struct{}:
		p.setSignedStruct(value)
	case map[string]any:
		p.setSignedAny(value)
	case []string:
		p.setSignedIDs(value)
	default:
		if refs, ok := refsFromValue(value); ok {
			p.setRefs(refs)
			return
		}
		if graph, ok := graphFromValue(value); ok {
			p.setGraph(graph)
			return
		}
		if signed, ok := signedFromValue(value); ok {
			p.setSignedIDs(signed)
			return
		}
		p.errs = append(p.errs, fmt.Sprintf("unsupported input argument type %T", arg))
	}
}

func (p *inputParser) setInput(in Input) {
	if p.sawRefs || p.sawGraph || p.sawSign || p.sawAllow ||
		len(p.in.Grafts) > 0 || p.in.GraftContents != "" {
		p.errs = append(p.errs, "duplicate Input argument")
		return
	}
	p.in = in
	p.sawRefs = in.Refs != nil
	p.sawGraph = in.Graph != nil
	p.sawSign = in.Signed != nil
	p.sawAllow = in.AllowReplace
}

func (p *inputParser) setRefs(refs []Ref) {
	if p.sawRefs {
		p.errs = append(p.errs, "duplicate refs argument")
		return
	}
	p.sawRefs = true
	p.in.Refs = refs
}

func (p *inputParser) setGraph(graph map[string][]string) {
	if p.sawGraph {
		p.errs = append(p.errs, "duplicate commit graph argument")
		return
	}
	p.sawGraph = true
	p.in.Graph = graph
}

func (p *inputParser) setSignedBool(signed map[string]bool) {
	if p.sawSign {
		p.errs = append(p.errs, "duplicate signed set argument")
		return
	}
	p.sawSign = true
	p.in.Signed = signed
}

func (p *inputParser) setSignedStruct(signed map[string]struct{}) {
	if p.sawSign {
		p.errs = append(p.errs, "duplicate signed set argument")
		return
	}
	p.sawSign = true
	p.in.Signed = make(map[string]bool, len(signed))
	for id := range signed {
		p.in.Signed[id] = true
	}
}

func (p *inputParser) setSignedAny(signed map[string]any) {
	if p.sawSign {
		p.errs = append(p.errs, "duplicate signed set argument")
		return
	}
	p.sawSign = true
	p.in.Signed = make(map[string]bool, len(signed))
	for id, value := range signed {
		switch v := value.(type) {
		case bool:
			p.in.Signed[id] = v
		case struct{}:
			p.in.Signed[id] = true
		case string:
			p.in.Signed[id] = v != ""
		default:
			p.errs = append(p.errs, fmt.Sprintf("unsupported signed-set value type %T", value))
		}
	}
}

func (p *inputParser) setSignedIDs(ids []string) {
	if p.sawSign {
		p.errs = append(p.errs, "duplicate signed set argument")
		return
	}
	p.sawSign = true
	p.in.Signed = make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			p.in.Signed[id] = true
		}
	}
}

func refsFromValue(value any) ([]Ref, bool) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	refs := make([]Ref, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				continue
			}
			item = item.Elem()
		}
		if item.Kind() != reflect.Struct {
			return nil, false
		}
		name, ok := stringField(item, "Name", "RefName", "Ref")
		if !ok {
			return nil, false
		}
		target, ok := stringField(item, "Target", "TargetID", "ObjectID", "Object")
		if !ok {
			return nil, false
		}
		refs = append(refs, Ref{Name: name, Target: target})
	}
	return refs, true
}

func graphFromValue(value any) (map[string][]string, bool) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	graph := make(map[string][]string, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		key := iter.Key().String()
		parents, ok := stringsFromValue(iter.Value())
		if !ok {
			return nil, false
		}
		graph[key] = parents
	}
	return graph, true
}

func signedFromValue(value any) ([]string, bool) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	ids := make([]string, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		include := false
		switch iter.Value().Kind() {
		case reflect.Bool:
			include = iter.Value().Bool()
		case reflect.Struct:
			include = iter.Value().Type().NumField() == 0 || !iter.Value().IsZero()
		case reflect.Interface:
			include = !iter.Value().IsNil() && !iter.Value().IsZero()
		case reflect.String:
			include = iter.Value().String() != ""
		default:
			return nil, false
		}
		if include {
			ids = append(ids, iter.Key().String())
		}
	}
	return ids, true
}

func stringsFromValue(value reflect.Value) ([]string, bool) {
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]string, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		item := value.Index(i)
		if item.Kind() == reflect.Interface {
			item = item.Elem()
		}
		if item.Kind() != reflect.String {
			return nil, false
		}
		out = append(out, item.String())
	}
	return out, true
}

func stringField(value reflect.Value, names ...string) (string, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.String {
			return field.String(), true
		}
	}
	return "", false
}
