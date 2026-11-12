package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// MatchType selects how a source identity is compared with Rule.Pattern.
type MatchType string

const (
	MatchExact    MatchType = "EXACT"
	MatchWildcard MatchType = "WILDCARD"
	MatchRegex    MatchType = "REGEX"
)

// Action is what a matching rule does to a source.
type Action string

const (
	ActionAllow   Action = "ALLOW"
	ActionDeny    Action = "DENY"
	ActionConvert Action = "CONVERT"
)

// SourceKind identifies the kind of source operation in a build graph.
type SourceKind string

const (
	KindImage SourceKind = "image"
	KindGit   SourceKind = "git"
	KindHTTP  SourceKind = "http"
	KindLocal SourceKind = "local"
)

// Rule is one entry in the ordered source policy.
//
// Rules are evaluated from first to last. The first matching rule decides the
// result. A CONVERT rule replaces the source with ConvertTo; all rules after
// the CONVERT rule are then checked against the converted value, and any
// subsequent matching DENY rule rejects it.
//
// Kind is optional. When set, the rule applies only to that source kind.
type Rule struct {
	ID        string     `json:"id,omitempty"`
	MatchType MatchType  `json:"matchType"`
	Pattern   string     `json:"pattern"`
	Action    Action     `json:"action"`
	ConvertTo string     `json:"convertTo,omitempty"`
	Kind      SourceKind `json:"kind,omitempty"`
}

// Vertex is a source operation in a build graph.
//
// The source identity is held in Source. Attrs contains the source operation
// attributes, for example http.checksum or git.ref. Deps and Dependencies are
// both accepted as dependency edge lists; callers normally use one of them.
type Vertex struct {
	ID           string            `json:"id"`
	Source       string            `json:"source"`
	Kind         SourceKind        `json:"kind,omitempty"`
	Attrs        map[string]string `json:"attrs,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Deps         []string          `json:"deps,omitempty"`
	Dependencies []string          `json:"dependencies,omitempty"`
}

// Edge is an optional explicit dependency edge between two vertex IDs.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is the build definition presented to admission.
type Graph struct {
	Vertices []Vertex `json:"vertices"`
	Edges    []Edge   `json:"edges,omitempty"`
	Roots    []string `json:"roots,omitempty"`
}

// Policy is an ordered source policy. The zero value denies every unmatched
// source. Set DefaultAction to ActionAllow only when an implicit allow is
// explicitly intended.
type Policy struct {
	Rules         []Rule `json:"rules"`
	DefaultAction Action `json:"defaultAction,omitempty"`
}

// NewPolicy validates rules and returns a policy that can be reused safely by
// concurrent admission calls.
func NewPolicy(rules []Rule) (*Policy, error) {
	if _, err := prepareRules(rules); err != nil {
		return nil, err
	}
	return &Policy{Rules: append([]Rule(nil), rules...)}, nil
}

// Validate checks rules and the default action without evaluating a graph.
func (p Policy) Validate() error {
	if _, err := prepareRules(p.Rules); err != nil {
		return err
	}
	_, err := normalizeDefaultAction(p.DefaultAction)
	return err
}

// Decision records the result for one distinct graph vertex.
type Decision struct {
	VertexID  string     `json:"vertexId"`
	Kind      SourceKind `json:"kind"`
	Source    string     `json:"source"`
	Canonical string     `json:"canonical"`
	RuleID    string     `json:"ruleId,omitempty"`
	Action    Action     `json:"action,omitempty"`
	Converted bool       `json:"converted,omitempty"`
	Allowed   bool       `json:"allowed"`
	Reason    string     `json:"reason,omitempty"`
}

// Result is the complete admission decision for a graph.
type Result struct {
	Allowed   bool       `json:"allowed"`
	Decisions []Decision `json:"decisions"`
}

// FirstDenial returns the first source that caused the request to be rejected,
// or nil when the request was allowed.
func (r *Result) FirstDenial() *Decision {
	if r == nil {
		return nil
	}
	for i := range r.Decisions {
		if !r.Decisions[i].Allowed {
			return &r.Decisions[i]
		}
	}
	return nil
}

// AdmissionError is returned by the convenience admission methods.
type AdmissionError struct {
	VertexID string `json:"vertexId"`
	Source   string `json:"source"`
	RuleID   string `json:"ruleId,omitempty"`
	Reason   string `json:"reason"`
}

func (e *AdmissionError) Error() string {
	if e == nil {
		return "source policy denied the request"
	}
	where := e.VertexID
	if where == "" {
		where = e.Source
	}
	if e.RuleID != "" {
		return fmt.Sprintf("source policy denied %s (rule %s): %s", where, e.RuleID, e.Reason)
	}
	return fmt.Sprintf("source policy denied %s: %s", where, e.Reason)
}

// Err converts a rejected result into an AdmissionError.
func (r *Result) Err() error {
	if r == nil {
		return &AdmissionError{Reason: "missing admission result"}
	}
	if r.Allowed {
		return nil
	}
	denied := r.FirstDenial()
	if denied == nil {
		return &AdmissionError{Reason: "request was not allowed"}
	}
	return &AdmissionError{
		VertexID: denied.VertexID,
		Source:   denied.Source,
		RuleID:   denied.RuleID,
		Reason:   denied.Reason,
	}
}

// Evaluate applies rules to a graph. It returns an error only for malformed
// input (for example an invalid rule or duplicate vertex ID). A source that
// fails normalization or policy checks is reported through Result.Allowed and
// the corresponding Decision, not as an error.
func Evaluate(graph *Graph, rules []Rule) (*Result, error) {
	return Policy{Rules: rules}.Evaluate(graph)
}

// EvaluateRules is an explicit alias for Evaluate.
func EvaluateRules(graph *Graph, rules []Rule) (*Result, error) {
	return Evaluate(graph, rules)
}

// Evaluate applies this policy to a graph.
func (p Policy) Evaluate(graph *Graph) (*Result, error) {
	prepared, err := prepareRules(p.Rules)
	if err != nil {
		return nil, err
	}
	defaultAction, err := normalizeDefaultAction(p.DefaultAction)
	if err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, errors.New("nil build graph")
	}

	seen := make(map[string]Vertex, len(graph.Vertices))
	unique := make([]Vertex, 0, len(graph.Vertices))
	for _, vertex := range graph.Vertices {
		if vertex.ID == "" {
			return nil, errors.New("vertex with empty ID")
		}
		if previous, ok := seen[vertex.ID]; ok {
			// A repeated identical vertex is the same graph node referenced
			// twice. It is safe to collapse. Conflicting definitions under one
			// ID are ambiguous and must be rejected.
			if !reflect.DeepEqual(previous, vertex) {
				return nil, fmt.Errorf("duplicate vertex ID %q has conflicting definitions", vertex.ID)
			}
			continue
		}
		seen[vertex.ID] = vertex
		unique = append(unique, vertex)
	}
	for _, vertex := range unique {
		for _, dep := range vertexDependencies(vertex) {
			if dep == "" {
				return nil, fmt.Errorf("vertex %q has an empty dependency ID", vertex.ID)
			}
			if _, ok := seen[dep]; !ok {
				return nil, fmt.Errorf("vertex %q depends on unknown vertex %q", vertex.ID, dep)
			}
		}
	}
	for _, edge := range graph.Edges {
		if edge.From == "" || edge.To == "" {
			return nil, errors.New("dependency edge with empty vertex ID")
		}
		if _, ok := seen[edge.From]; !ok {
			return nil, fmt.Errorf("dependency edge references unknown vertex %q", edge.From)
		}
		if _, ok := seen[edge.To]; !ok {
			return nil, fmt.Errorf("dependency edge references unknown vertex %q", edge.To)
		}
	}
	for _, root := range graph.Roots {
		if _, ok := seen[root]; !ok {
			return nil, fmt.Errorf("unknown root vertex %q", root)
		}
	}

	evaluated := unique
	if len(graph.Roots) > 0 {
		adjacency := make(map[string][]string, len(unique))
		for _, vertex := range unique {
			adjacency[vertex.ID] = append(adjacency[vertex.ID], vertexDependencies(vertex)...)
		}
		for _, edge := range graph.Edges {
			adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		}
		reachable := make(map[string]struct{}, len(unique))
		queue := append([]string(nil), graph.Roots...)
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if _, ok := reachable[id]; ok {
				continue
			}
			reachable[id] = struct{}{}
			queue = append(queue, adjacency[id]...)
		}
		evaluated = make([]Vertex, 0, len(unique))
		for _, vertex := range unique {
			if _, ok := reachable[vertex.ID]; ok {
				evaluated = append(evaluated, vertex)
			}
		}
	}

	result := &Result{
		Allowed:   true,
		Decisions: make([]Decision, 0, len(evaluated)),
	}
	// The graph can reference the same vertex from several parents. Keeping
	// this evaluation keyed by vertex ID makes such a shared subgraph part of
	// admission exactly once, independent of the number of incoming edges.
	for _, vertex := range evaluated {
		decision := evaluateVertex(vertex, prepared, defaultAction)
		result.Decisions = append(result.Decisions, decision)
		if !decision.Allowed {
			result.Allowed = false
		}
	}
	return result, nil
}

// Authorize is the admission-facing form of Evaluate: it returns nil only
// when every source in the graph is allowed.
func (p Policy) Authorize(graph *Graph) error {
	result, err := p.Evaluate(graph)
	if err != nil {
		return err
	}
	return result.Err()
}

// Admit is an alias for Authorize.
func (p Policy) Admit(graph *Graph) error {
	return p.Authorize(graph)
}

// Admit applies rules to a graph and reports nil only when the request is
// allowed.
func Admit(graph *Graph, rules []Rule) error {
	return Policy{Rules: rules}.Authorize(graph)
}

type preparedRule struct {
	rule           Rule
	exact          string
	exactCanonical string
	re             *regexp.Regexp
}

func prepareRules(rules []Rule) ([]preparedRule, error) {
	prepared := make([]preparedRule, 0, len(rules))
	for i, rule := range rules {
		if rule.MatchType != MatchExact && rule.MatchType != MatchWildcard && rule.MatchType != MatchRegex {
			return nil, fmt.Errorf("rule %d (%q): invalid match type %q", i, rule.ID, rule.MatchType)
		}
		if rule.Action != ActionAllow && rule.Action != ActionDeny && rule.Action != ActionConvert {
			return nil, fmt.Errorf("rule %d (%q): invalid action %q", i, rule.ID, rule.Action)
		}
		if rule.Kind != "" && !rule.Kind.valid() {
			return nil, fmt.Errorf("rule %d (%q): invalid source kind %q", i, rule.ID, rule.Kind)
		}
		if rule.Pattern == "" {
			return nil, fmt.Errorf("rule %d (%q): empty pattern", i, rule.ID)
		}
		if rule.Action == ActionConvert && strings.TrimSpace(rule.ConvertTo) == "" {
			return nil, fmt.Errorf("rule %d (%q): CONVERT requires ConvertTo", i, rule.ID)
		}

		item := preparedRule{rule: rule}
		switch rule.MatchType {
		case MatchExact:
			item.exact = rule.Pattern
			if rule.Kind == KindImage || (rule.Kind == "" && !strings.Contains(rule.Pattern, "://")) {
				if canonical, err := canonicalImageReference(rule.Pattern); err == nil {
					item.exactCanonical = canonical
				}
			}
		case MatchWildcard:
			re, err := compileWildcard(rule.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule %d (%q): %w", i, rule.ID, err)
			}
			item.re = re
		case MatchRegex:
			re, err := regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule %d (%q): invalid regexp: %w", i, rule.ID, err)
			}
			item.re = re
		}
		prepared = append(prepared, item)
	}
	return prepared, nil
}

func compileWildcard(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	escaped := false
	for _, r := range pattern {
		if escaped {
			b.WriteString(regexp.QuoteMeta(string(r)))
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	if escaped {
		b.WriteString(regexp.QuoteMeta("\\"))
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func normalizeDefaultAction(action Action) (Action, error) {
	switch action {
	case "":
		return ActionDeny, nil
	case ActionAllow, ActionDeny:
		return action, nil
	default:
		return "", fmt.Errorf("invalid default action %q", action)
	}
}

func (k SourceKind) valid() bool {
	switch k {
	case KindImage, KindGit, KindHTTP, KindLocal:
		return true
	default:
		return false
	}
}

func evaluateVertex(vertex Vertex, rules []preparedRule, defaultAction Action) Decision {
	decision := Decision{
		VertexID: vertex.ID,
		Source:   vertex.Source,
	}

	attrs, err := vertexAttributes(vertex)
	if err != nil {
		decision.Reason = err.Error()
		return decision
	}
	kind, canonical, err := canonicalizeSource(vertex.Kind, vertex.Source, attrs)
	decision.Kind = kind
	if err != nil {
		decision.Reason = err.Error()
		return decision
	}
	decision.Canonical = canonical

	first := firstMatch(rules, 0, kind, canonical)
	if first < 0 {
		if defaultAction == ActionAllow {
			decision.Allowed = true
			decision.Action = ActionAllow
			decision.Reason = "allowed by default action"
			return decision
		}
		decision.Action = ActionDeny
		decision.Reason = "no matching source policy rule"
		return decision
	}

	decision.RuleID = rules[first].rule.ID
	decision.Action = rules[first].rule.Action
	switch rules[first].rule.Action {
	case ActionAllow:
		decision.Allowed = true
	case ActionDeny:
		decision.Reason = "denied by matching source policy rule"
	case ActionConvert:
		decision.Converted = true
		canonical, err = canonicalizeAsKind(kind, rules[first].rule.ConvertTo, attrs)
		if err != nil {
			decision.Reason = "invalid CONVERT target: " + err.Error()
			return decision
		}
		decision.Canonical = canonical

		// A CONVERT does not win outright. The converted identity must run
		// the gauntlet of every later rule; ALLOW entries are intentionally
		// skipped so a later DENY cannot be hidden behind one.
		for i := first + 1; i < len(rules); i++ {
			if !rules[i].matches(kind, canonical) {
				continue
			}
			switch rules[i].rule.Action {
			case ActionDeny:
				decision.RuleID = rules[i].rule.ID
				decision.Action = ActionDeny
				decision.Reason = "converted source denied by a later rule"
				return decision
			case ActionConvert:
				decision.RuleID = rules[i].rule.ID
				decision.Action = ActionConvert
				canonical, err = canonicalizeAsKind(kind, rules[i].rule.ConvertTo, attrs)
				if err != nil {
					decision.Reason = "invalid CONVERT target: " + err.Error()
					return decision
				}
				decision.Canonical = canonical
			case ActionAllow:
				// Deliberately continue scanning for later DENY rules.
			}
		}
		decision.Allowed = true
	}
	return decision
}

func firstMatch(rules []preparedRule, start int, kind SourceKind, value string) int {
	for i := start; i < len(rules); i++ {
		if rules[i].matches(kind, value) {
			return i
		}
	}
	return -1
}

func (r preparedRule) matches(kind SourceKind, value string) bool {
	if r.rule.Kind != "" && r.rule.Kind != kind {
		return false
	}
	switch r.rule.MatchType {
	case MatchExact:
		return value == r.exact || (kind == KindImage && r.exactCanonical != "" && value == r.exactCanonical)
	case MatchWildcard, MatchRegex:
		return r.re.MatchString(value)
	default:
		return false
	}
}

func vertexAttributes(vertex Vertex) (map[string]string, error) {
	if len(vertex.Attributes) == 0 {
		return vertex.Attrs, nil
	}
	if len(vertex.Attrs) == 0 {
		return vertex.Attributes, nil
	}
	attrs := make(map[string]string, len(vertex.Attributes)+len(vertex.Attrs))
	for k, v := range vertex.Attributes {
		attrs[k] = v
	}
	for k, v := range vertex.Attrs {
		if existing, ok := attrs[k]; ok && existing != v {
			return nil, fmt.Errorf("conflicting values for source attribute %q", k)
		}
		attrs[k] = v
	}
	return attrs, nil
}

func vertexDependencies(vertex Vertex) []string {
	if len(vertex.Dependencies) == 0 {
		return vertex.Deps
	}
	if len(vertex.Deps) == 0 {
		return vertex.Dependencies
	}
	deps := make([]string, 0, len(vertex.Dependencies)+len(vertex.Deps))
	deps = append(deps, vertex.Dependencies...)
	deps = append(deps, vertex.Deps...)
	return deps
}

// SortedDecisionIDs returns evaluated vertex IDs in lexical order. It is useful
// for diagnostic output that must not depend on request ordering.
func (r *Result) SortedDecisionIDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.Decisions))
	for i := range r.Decisions {
		ids = append(ids, r.Decisions[i].VertexID)
	}
	sort.Strings(ids)
	return ids
}
