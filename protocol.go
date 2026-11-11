package netsec

import "fmt"

// MaxSteps bounds the length of a role script.
const MaxSteps = 12

// StepKind identifies a role-script operation.
type StepKind uint8

const (
	SendStep  StepKind = iota // send a message (ground pattern)
	RecvStep                  // receive a message matching a pattern
	FreshStep                 // generate a fresh atom and bind it to a variable
	BeginStep                 // authentication begin claim
	EndStep                   // authentication end claim
)

func (k StepKind) String() string {
	switch k {
	case SendStep:
		return "send"
	case RecvStep:
		return "recv"
	case FreshStep:
		return "fresh"
	case BeginStep:
		return "begin"
	case EndStep:
		return "end"
	}
	return "?"
}

// Step is one operation of a role script.
//
//	FreshStep: Var names the variable receiving the new atom.
//	SendStep / RecvStep: Pat is the message pattern.
//	BeginStep / EndStep: Args are the claim arguments (e.g. subject, nonce).
type Step struct {
	Kind StepKind
	Var  string
	Pat  *Term
	Args []*Term
}

// SendMsg builds a send step for the given message pattern.
func SendMsg(pat *Term) Step { return Step{Kind: SendStep, Pat: pat} }

// RecvMsg builds a receive step for the given message pattern.
func RecvMsg(pat *Term) Step { return Step{Kind: RecvStep, Pat: pat} }

// FreshAtom builds a step generating a fresh atom bound to variable v.
func FreshAtom(v string) Step { return Step{Kind: FreshStep, Var: v} }

// BeginEv builds a begin claim over the given argument patterns.
func BeginEv(args ...*Term) Step { return Step{Kind: BeginStep, Args: args} }

// EndEv builds an end claim over the given argument patterns.
func EndEv(args ...*Term) Step { return Step{Kind: EndStep, Args: args} }

// Role is a named script of at most MaxSteps steps.
type Role struct {
	Name  string
	Steps []Step
}

// Protocol is a set of roles the verification host can instantiate.
type Protocol struct {
	Roles []Role
}

// SessionSpec instantiates a role for one session. Params pre-binds
// pattern variables to atom names (identities, pre-shared keys, ...).
type SessionSpec struct {
	Role   string
	Params map[string]string
}

func (p *Protocol) role(name string) (*Role, error) {
	for i := range p.Roles {
		if p.Roles[i].Name == name {
			return &p.Roles[i], nil
		}
	}
	return nil, fmt.Errorf("netsec: unknown role %q", name)
}

// Validate checks the static well-formedness of the protocol.
func (p *Protocol) Validate() error {
	seen := map[string]bool{}
	for i := range p.Roles {
		r := &p.Roles[i]
		if !validName(r.Name) {
			return fmt.Errorf("netsec: invalid role name %q", r.Name)
		}
		if seen[r.Name] {
			return fmt.Errorf("netsec: duplicate role %q", r.Name)
		}
		seen[r.Name] = true
		if len(r.Steps) == 0 {
			return fmt.Errorf("netsec: role %q has no steps", r.Name)
		}
		if len(r.Steps) > MaxSteps {
			return fmt.Errorf("netsec: role %q has %d steps, max is %d", r.Name, len(r.Steps), MaxSteps)
		}
		for j, s := range r.Steps {
			if err := s.validate(); err != nil {
				return fmt.Errorf("netsec: role %q step %d: %w", r.Name, j, err)
			}
		}
	}
	return nil
}

func (s Step) validate() error {
	switch s.Kind {
	case FreshStep:
		if !validName(s.Var) {
			return fmt.Errorf("invalid fresh variable %q", s.Var)
		}
	case SendStep, RecvStep:
		if err := checkPattern(s.Pat); err != nil {
			return err
		}
	case BeginStep, EndStep:
		if len(s.Args) == 0 {
			return fmt.Errorf("%s event needs at least one argument", s.Kind)
		}
		for _, a := range s.Args {
			if err := checkPattern(a); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown step kind %d", s.Kind)
	}
	return nil
}

func checkPattern(t *Term) error {
	if t == nil {
		return fmt.Errorf("nil pattern")
	}
	switch t.K {
	case Atom, Var:
		if !validName(t.Name) {
			return fmt.Errorf("invalid name %q", t.Name)
		}
		return nil
	case Pair, Enc:
		if err := checkPattern(t.A); err != nil {
			return err
		}
		return checkPattern(t.B)
	}
	return fmt.Errorf("invalid term kind %d", t.K)
}
