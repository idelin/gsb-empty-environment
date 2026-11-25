package symexec

import (
	"errors"
	"fmt"
)

// MaxRoleSteps is the platform limit on the length of a role script.
const MaxRoleSteps = 12

// StepKind is the kind of a role-script operation.
type StepKind uint8

const (
	StepSend StepKind = iota + 1
	StepReceive
	StepFresh
	StepBegin
	StepEnd
)

// String returns the conventional action name.
func (k StepKind) String() string {
	switch k {
	case StepSend:
		return "send"
	case StepReceive:
		return "receive"
	case StepFresh:
		return "fresh"
	case StepBegin:
		return "begin"
	case StepEnd:
		return "end"
	default:
		return "unknown"
	}
}

// Step is one role-script operation.
//
//   - Send uses Term as the message template.
//   - Receive uses Term as the message pattern.
//   - Fresh binds each name to a new, unguessable atom.
//   - Begin and End use Principal and Nonce.
type Step struct {
	Kind      StepKind `json:"kind"`
	Term      *Term    `json:"term,omitempty"`
	Fresh     []string `json:"fresh,omitempty"`
	Principal *Term    `json:"principal,omitempty"`
	Nonce     *Term    `json:"nonce,omitempty"`
	Label     string   `json:"label,omitempty"`
}

// Send constructs a send step.
func Send(message any) Step {
	term := asTerm(message)
	return Step{Kind: StepSend, Term: &term}
}

// Receive constructs a receive-by-pattern step.
func Receive(pattern any) Step {
	term := asTerm(pattern)
	return Step{Kind: StepReceive, Term: &term}
}

// Fresh constructs a step that generates one or more new atoms and binds them
// to the named role variables.
func Fresh(names ...string) Step {
	return Step{Kind: StepFresh, Fresh: append([]string(nil), names...)}
}

// Begin constructs a begin event step.
func Begin(principal, nonce any) Step {
	p, n := asTerm(principal), asTerm(nonce)
	return Step{Kind: StepBegin, Principal: &p, Nonce: &n}
}

// End constructs an end event step.
func End(principal, nonce any) Step {
	p, n := asTerm(principal), asTerm(nonce)
	return Step{Kind: StepEnd, Principal: &p, Nonce: &n}
}

// Role is a bounded role script. A role is not instantiated until it is placed
// in a Session.
type Role struct {
	Name  string `json:"name"`
	Steps []Step `json:"steps"`
}

// NewRole constructs a role. Validation of the twelve-step limit happens in
// Validate and during Search.
func NewRole(name string, steps ...Step) Role {
	return Role{Name: name, Steps: append([]Step(nil), steps...)}
}

// Validate checks the role script independent of a session binding.
func (r Role) Validate() error {
	if len(r.Steps) > MaxRoleSteps {
		return fmt.Errorf("role %q has %d steps, limit is %d", r.Name, len(r.Steps), MaxRoleSteps)
	}
	for i, step := range r.Steps {
		if err := step.validate(); err != nil {
			return fmt.Errorf("role %q step %d (%s): %w", r.Name, i, step.Kind, err)
		}
	}
	return nil
}

func (s Step) validate() error {
	switch s.Kind {
	case StepSend, StepReceive:
		if s.Term == nil {
			return errors.New("missing term")
		}
		if !s.Term.Valid() {
			return fmt.Errorf("%w: %s", ErrInvalidTerm, s.Term)
		}
	case StepFresh:
		if len(s.Fresh) == 0 {
			return errors.New("fresh step has no variable names")
		}
		seen := make(map[string]struct{}, len(s.Fresh))
		for _, name := range s.Fresh {
			if name == "" {
				return errors.New("fresh variable name is empty")
			}
			if _, ok := seen[name]; ok {
				return fmt.Errorf("fresh variable %q repeated", name)
			}
			seen[name] = struct{}{}
		}
	case StepBegin, StepEnd:
		if s.Principal == nil || s.Nonce == nil {
			return errors.New("event is missing principal or nonce")
		}
		if !s.Principal.Valid() || !s.Nonce.Valid() {
			return errors.New("event contains an invalid term")
		}
	default:
		return fmt.Errorf("unknown step kind %d", s.Kind)
	}
	return nil
}

// Session is one instantiated run of a role.
type Session struct {
	ID       string          `json:"id"`
	Role     Role            `json:"role"`
	Bindings map[string]Term `json:"bindings,omitempty"`
}

// NewSession constructs a session with parameter bindings. Bindings are copied
// so later changes by the caller cannot affect the search.
func NewSession(id string, role any, bindings any) Session {
	var roleValue Role
	switch typed := role.(type) {
	case Role:
		roleValue = typed
	case *Role:
		if typed != nil {
			roleValue = *typed
		}
	}
	return Session{ID: id, Role: roleValue, Bindings: bindingsFrom(bindings)}
}

func bindingsFrom(value any) map[string]Term {
	switch typed := value.(type) {
	case nil:
		return map[string]Term{}
	case map[string]Term:
		return cloneBindings(typed)
	case map[string]string:
		out := make(map[string]Term, len(typed))
		for name, atom := range typed {
			out[name] = Atom(atom)
		}
		return out
	default:
		return map[string]Term{}
	}
}

// Scenario is a protocol instance to analyse. Two sessions are the intended
// use case, but the engine also accepts other small session counts.
type Scenario struct {
	// Initial is the attacker's initial knowledge (public names, compromised
	// keys, protocol constants).
	Initial []Term `json:"initial,omitempty"`
	// InitialKnowledge is accepted as an alias for Initial for host
	// convenience.
	InitialKnowledge []Term    `json:"initialKnowledge,omitempty"`
	Sessions         []Session `json:"sessions"`
	Limits           Limits    `json:"limits,omitempty"`
}

// NewScenario is a convenience constructor. Initial knowledge may be a
// []Term, []string, or a single Term/*Term value.
func NewScenario(initial any, sessions ...Session) Scenario {
	return Scenario{Initial: termsFrom(initial), Sessions: append([]Session(nil), sessions...)}
}

// ScenarioOf is a flexible constructor. Arguments may be an initial knowledge
// slice ([]Term or []string), single Term/*Term values, Session values, or a
// []Session slice.
func ScenarioOf(arguments ...any) Scenario {
	var scenario Scenario
	for _, argument := range arguments {
		switch value := argument.(type) {
		case nil:
		case []Term:
			scenario.Initial = append(scenario.Initial, cloneTerms(value)...)
		case []string:
			for _, name := range value {
				scenario.Initial = append(scenario.Initial, Atom(name))
			}
		case Term:
			scenario.Initial = append(scenario.Initial, value)
		case *Term:
			if value != nil {
				scenario.Initial = append(scenario.Initial, *value)
			}
		case Session:
			scenario.Sessions = append(scenario.Sessions, value)
		case []Session:
			scenario.Sessions = append(scenario.Sessions, value...)
		}
	}
	return scenario
}

// Known constructs an initial knowledge list from atoms or arbitrary terms.
func Known(terms ...Term) []Term {
	return append([]Term(nil), terms...)
}

// KnowledgeFrom is a flexible constructor accepting Term, *Term and strings.
func KnowledgeFrom(values ...any) []Term {
	out := make([]Term, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case []Term:
			out = append(out, cloneTerms(typed)...)
		case []string:
			for _, name := range typed {
				out = append(out, Atom(name))
			}
		default:
			out = append(out, asTerm(value))
		}
	}
	return out
}

func termsFrom(value any) []Term {
	switch typed := value.(type) {
	case nil:
		return nil
	case []Term:
		return cloneTerms(typed)
	case []string:
		out := make([]Term, 0, len(typed))
		for _, name := range typed {
			out = append(out, Atom(name))
		}
		return out
	case Term:
		return []Term{typed}
	case *Term:
		if typed == nil {
			return nil
		}
		return []Term{*typed}
	default:
		return nil
	}
}

// Public constructs initial knowledge containing the named public atoms.
func Public(names ...string) []Term {
	out := make([]Term, 0, len(names))
	for _, name := range names {
		out = append(out, Atom(name))
	}
	return out
}

func (s Scenario) initialTerms() []Term {
	if len(s.Initial) == 0 {
		return cloneTerms(s.InitialKnowledge)
	}
	out := append([]Term(nil), s.Initial...)
	out = append(out, s.InitialKnowledge...)
	return out
}

// Validate checks scenario limits and all static role invariants.
func (s Scenario) Validate() error {
	if len(s.Sessions) == 0 {
		return errors.New("scenario has no sessions")
	}
	ids := make(map[string]struct{}, len(s.Sessions))
	initial := s.initialTerms()
	for _, term := range initial {
		if !term.Valid() || !term.Ground() {
			return fmt.Errorf("%w: initial term %s", ErrInvalidTerm, term)
		}
	}
	for _, session := range s.Sessions {
		if session.ID == "" {
			return errors.New("session id is empty")
		}
		if _, ok := ids[session.ID]; ok {
			return fmt.Errorf("duplicate session id %q", session.ID)
		}
		ids[session.ID] = struct{}{}
		if err := session.Role.Validate(); err != nil {
			return err
		}
		if err := validateBinding(session); err != nil {
			return err
		}
	}
	return nil
}

func validateBinding(session Session) error {
	bound := make(map[string]struct{}, len(session.Bindings))
	for name, term := range session.Bindings {
		if name == "" {
			return fmt.Errorf("session %q has an empty binding name", session.ID)
		}
		if !term.Valid() || !term.Ground() {
			return fmt.Errorf("session %q binding %q is not a valid ground term", session.ID, name)
		}
		bound[name] = struct{}{}
	}
	for i, step := range session.Role.Steps {
		switch step.Kind {
		case StepFresh:
			for _, name := range step.Fresh {
				if _, ok := bound[name]; ok {
					return fmt.Errorf("session %q step %d rebinds variable %q with fresh", session.ID, i, name)
				}
				bound[name] = struct{}{}
			}
		case StepReceive:
			for _, name := range step.Term.Vars() {
				bound[name] = struct{}{}
			}
		case StepSend:
			if err := requireBound(session.ID, i, "send", *step.Term, bound); err != nil {
				return err
			}
		case StepBegin, StepEnd:
			for _, name := range step.Principal.Vars() {
				if _, ok := bound[name]; !ok {
					return fmt.Errorf("session %q step %d (%s) uses unbound variable %q", session.ID, i, step.Kind, name)
				}
			}
			for _, name := range step.Nonce.Vars() {
				if _, ok := bound[name]; !ok {
					return fmt.Errorf("session %q step %d (%s) uses unbound variable %q", session.ID, i, step.Kind, name)
				}
			}
		}
	}
	return nil
}

func requireBound(sessionID string, step int, action string, term Term, bound map[string]struct{}) error {
	for _, name := range term.Vars() {
		if _, ok := bound[name]; !ok {
			return fmt.Errorf("session %q step %d (%s) uses unbound variable %q", sessionID, step, action, name)
		}
	}
	return nil
}

func cloneBindings(in map[string]Term) map[string]Term {
	if in == nil {
		return map[string]Term{}
	}
	out := make(map[string]Term, len(in))
	for name, term := range in {
		out[name] = term
	}
	return out
}
