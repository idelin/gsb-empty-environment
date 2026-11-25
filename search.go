package symexec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status is the verdict of an interleaving search.
type Status string

const (
	// Unknown means the configured budget was exhausted before a verdict.
	Unknown Status = "unknown"
	// Attack means a shortest violating interleaving was found.
	Attack Status = "attack"
	// Safe means the complete search space was explored without a violation.
	Safe Status = "safe"
)

func (s Status) String() string { return string(s) }

// Budget bounds the breadth-first interleaving search. Zero-valued fields are
// replaced by DefaultBudget.
type Budget struct {
	MaxStates int           `json:"maxStates,omitempty"`
	MaxSteps  int           `json:"maxSteps,omitempty"`
	MaxTime   time.Duration `json:"maxTime,omitempty"`
}

// DefaultBudget returns the default search budget.
func DefaultBudget() Budget {
	return Budget{
		MaxStates: 200000,
		MaxSteps:  64,
	}
}

func (b Budget) normalize() Budget {
	def := DefaultBudget()
	if b.MaxStates <= 0 {
		b.MaxStates = def.MaxStates
	}
	if b.MaxSteps <= 0 {
		b.MaxSteps = def.MaxSteps
	}
	if b.MaxTime < 0 {
		b.MaxTime = 0
	}
	return b
}

// Options configures analysis.
type Options struct {
	Budget Budget `json:"budget,omitempty"`
	Limits Limits `json:"limits,omitempty"`
	// MaxComposeDepth is a convenience override for Limits.MaxComposeDepth.
	MaxComposeDepth int `json:"maxComposeDepth,omitempty"`
}

func (o Options) normalized(scenario Scenario) Options {
	limits := scenario.Limits
	if o.Limits.MaxComposeDepth != 0 {
		limits.MaxComposeDepth = o.Limits.MaxComposeDepth
	}
	if o.Limits.MaxTerms != 0 {
		limits.MaxTerms = o.Limits.MaxTerms
	}
	if o.MaxComposeDepth != 0 {
		limits.MaxComposeDepth = o.MaxComposeDepth
	}
	o.Limits = limits.Normalize()
	o.Budget = o.Budget.normalize()
	return o
}

// Stats records how much of the search space was explored.
type Stats struct {
	States        int           `json:"states"`
	Transitions   int           `json:"transitions"`
	MaxDepth      int           `json:"maxDepth"`
	Elapsed       time.Duration `json:"elapsed"`
	ComposeDepth  int           `json:"composeDepth"`
	TermBudgetHit bool          `json:"termBudgetHit,omitempty"`
}

// TraceStep is one concrete role operation in an interleaving.
type TraceStep struct {
	Index       int             `json:"index"`
	Session     string          `json:"session"`
	Role        string          `json:"role"`
	StepIndex   int             `json:"stepIndex"`
	Kind        StepKind        `json:"kind"`
	Message     *Term           `json:"message,omitempty"`
	Pattern     *Term           `json:"pattern,omitempty"`
	Certificate *Certificate    `json:"certificate,omitempty"`
	Atoms       []Term          `json:"atoms,omitempty"`
	Event       *Event          `json:"event,omitempty"`
	Bindings    map[string]Term `json:"bindings,omitempty"`
}

// Event is a begin or end event observed on a trace.
type Event struct {
	Kind      StepKind `json:"kind"`
	Session   string   `json:"session"`
	Role      string   `json:"role"`
	StepIndex int      `json:"stepIndex"`
	Index     int      `json:"index"`
	Principal Term     `json:"principal"`
	Nonce     Term     `json:"nonce"`
}

// EventKey identifies the authentication agreement key: the event principal
// and nonce.
type EventKey struct {
	Principal Term
	Nonce     Term
}

// Key returns the canonical key.
func (e EventKey) Key() string { return e.Principal.Key() + "|" + e.Nonce.Key() }

func eventKeyOf(event Event) string {
	return EventKey{Principal: event.Principal, Nonce: event.Nonce}.Key()
}

// AttackTrace describes a shortest authentication mismatch.
type AttackTrace struct {
	End          Event          `json:"end"`
	Trace        []TraceStep    `json:"trace"`
	Certificates []*Certificate `json:"certificates,omitempty"`
	Length       int            `json:"length"`
	Limits       Limits         `json:"limits"`
}

// Result is the result of an analysis.
type Result struct {
	Status Status `json:"status"`
	// Found is true exactly when Status is Attack.
	Found  bool         `json:"found"`
	Trace  []TraceStep  `json:"trace,omitempty"`
	Events []Event      `json:"events,omitempty"`
	Attack *AttackTrace `json:"attack,omitempty"`
	Reason string       `json:"reason,omitempty"`
	Stats  Stats        `json:"stats"`
	Limits Limits       `json:"limits"`
}

// IsAttack reports whether a mismatch was found.
func (r Result) IsAttack() bool { return r.Status == Attack }

// IsSafe reports whether the complete search space was safe.
func (r Result) IsSafe() bool { return r.Status == Safe }

// IsUnknown reports whether the budget was exhausted.
func (r Result) IsUnknown() bool { return r.Status == Unknown }

// Analyze explores all interleavings in breadth-first order and returns the
// shortest trace whose end event has no matching prior begin event.
func Analyze(scenario Scenario, settings ...any) (Result, error) {
	if err := scenario.Validate(); err != nil {
		return Result{Status: Unknown, Reason: err.Error()}, err
	}
	options := optionsFrom(settings)
	options = options.normalized(scenario)
	initial := scenario.initialTerms()

	startBindings := make([]map[string]Term, len(scenario.Sessions))
	relevant := make([][]map[string]struct{}, len(scenario.Sessions))
	for i, session := range scenario.Sessions {
		startBindings[i] = cloneBindings(session.Bindings)
		relevant[i] = futureVariableSets(session.Role.Steps)
	}
	start := &searchNode{
		pos:      make([]int, len(scenario.Sessions)),
		bindings: startBindings,
		begins:   make(map[string]Event),
	}
	start.key = stateKey(relevant, start)
	visited := map[string]struct{}{start.key: {}}
	queue := []*searchNode{start}
	cache := newKnowledgeCache(initial, options.Limits)
	begin := time.Now()
	stats := Stats{ComposeDepth: options.Limits.MaxComposeDepth}
	truncated := false
	var budgetReason string

	for head := 0; head < len(queue); head++ {
		node := queue[head]
		stats.States++
		if options.Budget.MaxTime > 0 && time.Since(begin) > options.Budget.MaxTime {
			budgetReason = "time budget exhausted"
			break
		}
		if node.depth >= options.Budget.MaxSteps {
			if hasRemainingSteps(scenario, node) {
				truncated = true
			}
			continue
		}
		for sessionIndex := range scenario.Sessions {
			session := scenario.Sessions[sessionIndex]
			if node.pos[sessionIndex] >= len(session.Role.Steps) {
				continue
			}
			stepIndex := node.pos[sessionIndex]
			step := session.Role.Steps[stepIndex]
			var knowledge *Knowledge
			if step.Kind == StepReceive {
				var err error
				knowledge, err = cache.get(node.observed)
				if err != nil {
					if err == ErrTermBudget {
						stats.TermBudgetHit = true
						budgetReason = "attacker term budget exhausted"
						break
					}
					return Result{Status: Unknown, Reason: err.Error(), Stats: stats}, err
				}
			}
			children, err := expandStep(scenario, node, sessionIndex, stepIndex, step, knowledge, relevant, options)
			if err != nil {
				if err == ErrTermBudget {
					stats.TermBudgetHit = true
					budgetReason = "attacker term budget exhausted"
					break
				}
				return Result{Status: Unknown, Reason: err.Error(), Stats: stats}, err
			}
			for _, child := range children {
				stats.Transitions++
				if child.attackEnd != nil {
					trace := buildTrace(child)
					events := eventsFromTrace(trace)
					attack := &AttackTrace{
						End:          *child.attackEnd,
						Trace:        trace,
						Length:       len(trace),
						Certificates: certificatesFromTrace(trace),
						Limits:       options.Limits,
					}
					stats.MaxDepth = child.depth
					stats.Elapsed = time.Since(begin)
					return Result{
						Status: Attack,
						Found:  true,
						Trace:  trace,
						Events: events,
						Attack: attack,
						Reason: fmt.Sprintf("end(%s,%s) has no corresponding prior begin",
							child.attackEnd.Principal, child.attackEnd.Nonce),
						Stats:  stats,
						Limits: options.Limits,
					}, nil
				}
				if _, ok := visited[child.key]; ok {
					continue
				}
				if len(visited) >= options.Budget.MaxStates {
					budgetReason = "state budget exhausted"
					break
				}
				visited[child.key] = struct{}{}
				if child.depth > stats.MaxDepth {
					stats.MaxDepth = child.depth
				}
				queue = append(queue, child)
			}
			if budgetReason != "" {
				break
			}
		}
		if budgetReason != "" {
			break
		}
	}
	stats.Elapsed = time.Since(begin)
	if budgetReason != "" || truncated {
		reason := budgetReason
		if reason == "" {
			reason = "step budget exhausted"
		}
		return Result{Status: Unknown, Reason: reason, Stats: stats}, nil
	}
	return Result{
		Status: Safe,
		Reason: "complete interleaving space explored; every end has a prior matching begin",
		Stats:  stats,
	}, nil
}

// Search is an alias for Analyze.
func Search(scenario Scenario, settings ...any) (Result, error) {
	return Analyze(scenario, settings...)
}

// Run is an alias for Analyze.
func Run(scenario Scenario, settings ...any) (Result, error) {
	return Analyze(scenario, settings...)
}

// Check is an alias for Analyze.
func Check(scenario Scenario, settings ...any) (Result, error) {
	return Analyze(scenario, settings...)
}

// Solve is a convenience wrapper that folds validation and budget failures
// into an Unknown result.
func Solve(scenario Scenario, settings ...any) Result {
	result, _ := Analyze(scenario, settings...)
	return result
}

// Verify is an alias for Analyze.
func Verify(scenario Scenario, settings ...any) (Result, error) {
	return Analyze(scenario, settings...)
}

func optionsFrom(settings []any) Options {
	var options Options
	for _, setting := range settings {
		switch value := setting.(type) {
		case Options:
			options = value
		case *Options:
			if value != nil {
				options = *value
			}
		case Budget:
			options.Budget = value
		case *Budget:
			if value != nil {
				options.Budget = *value
			}
		case Limits:
			options.Limits = value
		case *Limits:
			if value != nil {
				options.Limits = *value
			}
		}
	}
	return options
}

type searchNode struct {
	parent     *searchNode
	transition *TraceStep
	depth      int
	pos        []int
	bindings   []map[string]Term
	observed   []Term
	begins     map[string]Event
	attackEnd  *Event
	key        string
}

func expandStep(scenario Scenario, node *searchNode, sessionIndex, stepIndex int, step Step, knowledge *Knowledge, relevant [][]map[string]struct{}, options Options) ([]*searchNode, error) {
	session := scenario.Sessions[sessionIndex]
	switch step.Kind {
	case StepSend:
		message, err := step.Term.Substitute(node.bindings[sessionIndex])
		if err != nil {
			return nil, nil
		}
		next := cloneNode(node, sessionIndex)
		next.observed = append(append([]Term(nil), node.observed...), message)
		next.transition = &TraceStep{
			Index:     node.depth + 1,
			Session:   session.ID,
			Role:      session.Role.Name,
			StepIndex: stepIndex,
			Kind:      StepSend,
			Message:   termPtr(message),
			Bindings:  cloneBindings(node.bindings[sessionIndex]),
		}
		next.depth = node.depth + 1
		next.key = stateKey(relevant, next)
		return []*searchNode{next}, nil

	case StepReceive:
		pattern := step.Term.SubstitutePartial(node.bindings[sessionIndex])
		candidates, err := knowledge.Match(pattern)
		if err != nil {
			return nil, err
		}
		out := make([]*searchNode, 0, len(candidates))
		for _, candidate := range candidates {
			binding, ok := mergeBindings(node.bindings[sessionIndex], candidate.Binding)
			if !ok {
				continue
			}
			matched, ok := Match(*step.Term, candidate.Term, binding)
			if !ok {
				continue
			}
			binding = matched
			if err := VerifyDerivation(candidate.Derivation, knowledge.initial, knowledge.observed, knowledge.limits); err != nil {
				return nil, fmt.Errorf("internal derivation error: %w", err)
			}
			next := cloneNode(node, sessionIndex)
			next.bindings[sessionIndex] = binding
			next.transition = &TraceStep{
				Index:       node.depth + 1,
				Session:     session.ID,
				Role:        session.Role.Name,
				StepIndex:   stepIndex,
				Kind:        StepReceive,
				Message:     termPtr(candidate.Term),
				Pattern:     termPtr(*step.Term),
				Certificate: candidate.Derivation,
				Bindings:    cloneBindings(binding),
			}
			next.depth = node.depth + 1
			next.key = stateKey(relevant, next)
			out = append(out, next)
		}
		return out, nil

	case StepFresh:
		binding := cloneBindings(node.bindings[sessionIndex])
		atoms := make([]Term, 0, len(step.Fresh))
		for _, name := range step.Fresh {
			if _, ok := binding[name]; ok {
				return nil, nil
			}
			value := generateFreshAtom(session.ID, stepIndex, name, scenario.initialTerms(), node.observed)
			binding[name] = value
			atoms = append(atoms, value)
		}
		next := cloneNode(node, sessionIndex)
		next.bindings[sessionIndex] = binding
		next.transition = &TraceStep{
			Index:     node.depth + 1,
			Session:   session.ID,
			Role:      session.Role.Name,
			StepIndex: stepIndex,
			Kind:      StepFresh,
			Atoms:     atoms,
			Bindings:  cloneBindings(binding),
		}
		next.depth = node.depth + 1
		next.key = stateKey(relevant, next)
		return []*searchNode{next}, nil

	case StepBegin, StepEnd:
		principal, err := step.Principal.Substitute(node.bindings[sessionIndex])
		if err != nil {
			return nil, nil
		}
		nonce, err := step.Nonce.Substitute(node.bindings[sessionIndex])
		if err != nil {
			return nil, nil
		}
		if !principal.Ground() || !nonce.Ground() {
			return nil, fmt.Errorf("event in session %q is not ground", session.ID)
		}
		event := Event{
			Kind:      step.Kind,
			Session:   session.ID,
			Role:      session.Role.Name,
			StepIndex: stepIndex,
			Index:     node.depth + 1,
			Principal: principal,
			Nonce:     nonce,
		}
		next := cloneNode(node, sessionIndex)
		next.transition = &TraceStep{
			Index:     node.depth + 1,
			Session:   session.ID,
			Role:      session.Role.Name,
			StepIndex: stepIndex,
			Kind:      step.Kind,
			Event:     &event,
			Bindings:  cloneBindings(node.bindings[sessionIndex]),
		}
		if step.Kind == StepBegin {
			key := eventKeyOf(event)
			if _, ok := node.begins[key]; !ok {
				next.begins = cloneBegins(node.begins)
				next.begins[key] = event
			}
		} else {
			if _, ok := node.begins[eventKeyOf(event)]; !ok {
				next.attackEnd = &event
			}
		}
		next.depth = node.depth + 1
		next.key = stateKey(relevant, next)
		return []*searchNode{next}, nil
	default:
		return nil, fmt.Errorf("unknown step kind %d", step.Kind)
	}
}

func termPtr(term Term) *Term {
	copy := term
	return &copy
}

func cloneNode(node *searchNode, sessionIndex int) *searchNode {
	next := &searchNode{
		parent:   node,
		depth:    node.depth,
		pos:      append([]int(nil), node.pos...),
		bindings: make([]map[string]Term, len(node.bindings)),
		observed: node.observed,
		begins:   node.begins,
	}
	copy(next.bindings, node.bindings)
	next.pos[sessionIndex]++
	return next
}

func cloneBegins(in map[string]Event) map[string]Event {
	out := make(map[string]Event, len(in)+1)
	for key, event := range in {
		out[key] = event
	}
	return out
}

func stateKey(relevant [][]map[string]struct{}, node *searchNode) string {
	var b strings.Builder
	b.WriteString("pos:")
	for _, p := range node.pos {
		b.WriteString(strconv.Itoa(p))
		b.WriteByte(',')
	}
	b.WriteString("|bind:")
	for i, binding := range node.bindings {
		names := make([]string, 0, len(relevant[i][node.pos[i]]))
		for name := range relevant[i][node.pos[i]] {
			if _, ok := binding[name]; ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			b.WriteString(name)
			b.WriteByte('=')
			b.WriteString(binding[name].Key())
			b.WriteByte(';')
		}
		b.WriteByte('|')
	}
	b.WriteString("obs:")
	keys := make([]string, 0, len(node.observed))
	for _, term := range node.observed {
		keys = append(keys, term.Key())
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte(';')
	}
	b.WriteString("|begin:")
	keys = keys[:0]
	for key := range node.begins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte(';')
	}
	return b.String()
}

func futureVariableSets(steps []Step) []map[string]struct{} {
	sets := make([]map[string]struct{}, len(steps)+1)
	current := make(map[string]struct{})
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		switch step.Kind {
		case StepSend, StepReceive:
			if step.Term != nil {
				for _, name := range step.Term.Vars() {
					current[name] = struct{}{}
				}
			}
		case StepFresh:
			for _, name := range step.Fresh {
				current[name] = struct{}{}
			}
		case StepBegin, StepEnd:
			if step.Principal != nil {
				for _, name := range step.Principal.Vars() {
					current[name] = struct{}{}
				}
			}
			if step.Nonce != nil {
				for _, name := range step.Nonce.Vars() {
					current[name] = struct{}{}
				}
			}
		}
		sets[i] = cloneStringSet(current)
	}
	sets[len(steps)] = map[string]struct{}{}
	return sets
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for name := range in {
		out[name] = struct{}{}
	}
	return out
}

func buildTrace(node *searchNode) []TraceStep {
	chain := make([]*searchNode, 0, node.depth)
	for current := node; current.transition != nil; current = current.parent {
		chain = append(chain, current)
	}
	out := make([]TraceStep, len(chain))
	for i := range chain {
		step := *chain[len(chain)-1-i].transition
		step.Index = i + 1
		if step.Event != nil {
			event := *step.Event
			event.Index = i + 1
			step.Event = &event
		}
		step.Bindings = cloneBindings(step.Bindings)
		out[i] = step
	}
	return out
}

func hasRemainingSteps(scenario Scenario, node *searchNode) bool {
	for i, session := range scenario.Sessions {
		if node.pos[i] < len(session.Role.Steps) {
			return true
		}
	}
	return false
}

func eventsFromTrace(trace []TraceStep) []Event {
	var out []Event
	for _, step := range trace {
		if step.Event != nil {
			out = append(out, *step.Event)
		}
	}
	return out
}

func certificatesFromTrace(trace []TraceStep) []*Certificate {
	var out []*Certificate
	for _, step := range trace {
		if step.Certificate != nil {
			out = append(out, step.Certificate)
		}
	}
	return out
}

type knowledgeCache struct {
	initial  []Term
	limits   Limits
	entries  map[string]*Knowledge
	failures map[string]error
}

func newKnowledgeCache(initial []Term, limits Limits) *knowledgeCache {
	return &knowledgeCache{
		initial:  initial,
		limits:   limits,
		entries:  make(map[string]*Knowledge),
		failures: make(map[string]error),
	}
}

func (c *knowledgeCache) get(observed []Term) (*Knowledge, error) {
	key := observedKey(observed)
	if err, ok := c.failures[key]; ok {
		return nil, err
	}
	if knowledge, ok := c.entries[key]; ok {
		return knowledge, nil
	}
	knowledge, err := NewKnowledge(c.initial, observed, c.limits)
	if err != nil {
		c.failures[key] = err
		return nil, err
	}
	c.entries[key] = knowledge
	return knowledge, nil
}

func observedKey(observed []Term) string {
	keys := make([]string, len(observed))
	for i, term := range observed {
		keys[i] = term.Key()
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte(';')
	}
	return b.String()
}

func generateFreshAtom(sessionID string, stepIndex int, name string, initial, observed []Term) Term {
	base := "!" + sessionID + ":" + strconv.Itoa(stepIndex) + ":" + name
	candidate := base
	for suffix := 1; freshNameUsed(candidate, initial, observed); suffix++ {
		candidate = base + "#" + strconv.Itoa(suffix)
	}
	return Atom(candidate)
}

func freshNameUsed(name string, initial, observed []Term) bool {
	for _, term := range initial {
		if termContainsAtom(term, name) {
			return true
		}
	}
	for _, term := range observed {
		if termContainsAtom(term, name) {
			return true
		}
	}
	return false
}

func termContainsAtom(term Term, name string) bool {
	switch term.Kind {
	case KindAtom:
		return term.Name == name
	case KindPair, KindEnc:
		return term.Left != nil && term.Right != nil &&
			(termContainsAtom(*term.Left, name) || termContainsAtom(*term.Right, name))
	default:
		return false
	}
}
