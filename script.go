package netsec

import (
	"fmt"
	"strconv"
)

// Node is an abstract statement inside a function or role template body.
type Node interface{ node() }

// NewKey declares a fresh local key with an owner, purpose and session epoch.
// For role templates Epoch names the integer epoch parameter that is re-bound
// on every recursion, so local keys are typed per session generation.
type NewKey struct {
	LocalID string
	Owner   string
	Purpose Purpose
	Epoch   string
}

// Encrypt constructs an encryption message under Key. Payload terms are
// recorded for traceability but do not themselves carry key constraints.
type Encrypt struct {
	Key     Term
	Payload []Term
}

// Sign constructs an authentication message verified with Key.
type Sign struct {
	Key     Term
	Payload []Term
}

// Derive derives a child key from Key.
type Derive struct {
	Key     Term
	Payload []Term
}

// BindKey asserts that Target denotes the same key atom as Source (an alias).
type BindKey struct{ Target, Source Term }

// Call invokes a polymorphic helper function with concrete key arguments.
type Call struct {
	Func string
	Args []Term
}

// Recurse unrolls one more frame of the enclosing role template, advancing the
// named epoch parameter. It is only legal inside a role body.
type Recurse struct{ Epoch string }

func (NewKey) node()  {}
func (Encrypt) node() {}
func (Sign) node()    {}
func (Derive) node()  {}
func (BindKey) node() {}
func (Call) node()    {}
func (Recurse) node() {}

// Func is a polymorphic helper template. Each call instantiates the body with
// fresh unification variables for the key parameters, so the same helper can
// be reused for encryption, verification and derivation keys without
// identifying their types across call sites.
type Func struct {
	Name      string
	KeyParams []string
	// EpochParams are integer parameters (typically role epochs) used only to
	// fill NewKey.Epoch fields. They are passed by name substitution.
	EpochParams []string
	Body        []Node
}

// Role is a recursive role template. It behaves like a function but its
// Recurse nodes are unrolled by the solver up to a fixed depth, re-binding the
// epoch on every frame.
type Role struct {
	Func
	Epoch string
}

// Script is the verifier host's builder for protocol scripts.
type Script struct {
	TopLevel []Operation
	Funcs    map[string]*Func
	Roles    map[string]*Role

	seenOps map[string]struct{}
}

// NewScript creates an empty script builder.
func NewScript() *Script {
	return &Script{
		Funcs:   make(map[string]*Func),
		Roles:   make(map[string]*Role),
		seenOps: make(map[string]struct{}),
	}
}

func (s *Script) addTop(op Operation) error {
	if op.ID == "" {
		return fmt.Errorf("netsec: operation id is required")
	}
	if _, ok := s.seenOps[op.ID]; ok {
		return fmt.Errorf("netsec: duplicate operation id %q", op.ID)
	}
	if op.Key == nil {
		return fmt.Errorf("netsec: operation %q is missing a key term", op.ID)
	}
	s.seenOps[op.ID] = struct{}{}
	s.TopLevel = append(s.TopLevel, op)
	return nil
}

// AddNewKey records a freshly generated global key with its declared type.
func (s *Script) AddNewKey(id string, key Term, owner string, purpose Purpose, session string) error {
	return s.addTop(Operation{
		ID: id, Kind: KindNewKey, Key: key,
		Owner: owner, Purpose: purpose, Session: session,
	})
}

// AddEncrypt records an encryption constructor.
func (s *Script) AddEncrypt(id string, key Term, payload ...Term) error {
	return s.addTop(Operation{ID: id, Kind: KindEncrypt, Key: key, Payload: payload})
}

// AddSign records a signature verification constructor.
func (s *Script) AddSign(id string, key Term, payload ...Term) error {
	return s.addTop(Operation{ID: id, Kind: KindSign, Key: key, Payload: payload})
}

// AddDerive records a key-derivation constructor.
func (s *Script) AddDerive(id string, key Term, payload ...Term) error {
	return s.addTop(Operation{ID: id, Kind: KindDerive, Key: key, Payload: payload})
}

// AddBindKey records a same-key alias between two key terms.
func (s *Script) AddBindKey(id string, target, source Term) error {
	return s.addTop(Operation{ID: id, Kind: KindBindKey, Key: target, Payload: []Term{source}})
}

// DeclareFunc registers a polymorphic helper template.
func (s *Script) DeclareFunc(f *Func) error {
	if f == nil || f.Name == "" {
		return fmt.Errorf("netsec: function requires a name")
	}
	if _, ok := s.Funcs[f.Name]; ok {
		return fmt.Errorf("netsec: duplicate function %q", f.Name)
	}
	if _, ok := s.Roles[f.Name]; ok {
		return fmt.Errorf("netsec: %q is already declared as a role", f.Name)
	}
	if err := validateBody(f.Name, f.KeyParams, f.EpochParams, f.Body); err != nil {
		return err
	}
	s.Funcs[f.Name] = f
	return nil
}

// DeclareRole registers a recursive role template.
func (s *Script) DeclareRole(r *Role) error {
	if r == nil || r.Name == "" {
		return fmt.Errorf("netsec: role requires a name")
	}
	if _, ok := s.Roles[r.Name]; ok {
		return fmt.Errorf("netsec: duplicate role %q", r.Name)
	}
	if _, ok := s.Funcs[r.Name]; ok {
		return fmt.Errorf("netsec: %q is already declared as a function", r.Name)
	}
	if r.Epoch == "" {
		return fmt.Errorf("netsec: role %q requires an epoch parameter", r.Name)
	}
	epochParams := append([]string{r.Epoch}, r.EpochParams...)
	if err := validateBody(r.Name, r.KeyParams, epochParams, r.Body); err != nil {
		return err
	}
	s.Roles[r.Name] = r
	return nil
}

// CallFunc records a top-level call to a polymorphic helper.
func (s *Script) CallFunc(id, name string, args []Term) error {
	if _, ok := s.Funcs[name]; !ok {
		return fmt.Errorf("netsec: unknown function %q", name)
	}
	if err := checkArity(s.Funcs[name], args); err != nil {
		return err
	}
	if _, ok := s.seenOps[id]; ok {
		return fmt.Errorf("netsec: duplicate operation id %q", id)
	}
	s.seenOps[id] = struct{}{}
	s.TopLevel = append(s.TopLevel, Operation{ID: id, Kind: kindCall, Key: Literal{Key: name}, Payload: args})
	return nil
}

// CallRole records a bounded unrolling of a recursive role template.
func (s *Script) CallRole(id, name string, depth int, args []Term) error {
	if depth < 1 {
		return fmt.Errorf("netsec: role %q call depth must be >= 1", name)
	}
	r, ok := s.Roles[name]
	if !ok {
		return fmt.Errorf("netsec: unknown role %q", name)
	}
	if err := checkArity(&r.Func, args); err != nil {
		return err
	}
	if _, ok := s.seenOps[id]; ok {
		return fmt.Errorf("netsec: duplicate operation id %q", id)
	}
	s.seenOps[id] = struct{}{}
	s.TopLevel = append(s.TopLevel, Operation{
		ID: id, Kind: kindRole, Key: Literal{Key: name},
		Payload: args, Session: strconv.Itoa(depth),
	})
	return nil
}

func checkArity(f *Func, args []Term) error {
	if len(args) != len(f.KeyParams) {
		return fmt.Errorf("netsec: %s expects %d key arguments, got %d", f.Name, len(f.KeyParams), len(args))
	}
	for _, a := range args {
		if a == nil {
			return fmt.Errorf("netsec: %s received a nil key argument", f.Name)
		}
	}
	return nil
}

func validateBody(name string, keyParams, epochParams []string, body []Node) error {
	keys := make(map[string]struct{}, len(keyParams))
	for _, p := range keyParams {
		keys[p] = struct{}{}
	}
	epochs := make(map[string]struct{}, len(epochParams))
	for _, e := range epochParams {
		epochs[e] = struct{}{}
	}
	locals := make(map[string]struct{})
	validKey := func(t Term) bool {
		switch v := t.(type) {
		case Param:
			_, ok := keys[v.Name]
			return ok
		case Local:
			_, ok := locals[v.ID]
			return ok
		default:
			return true
		}
	}
	for i, n := range body {
		at := fmt.Sprintf("node %d of %s", i, name)
		switch v := n.(type) {
		case NewKey:
			if v.LocalID == "" {
				return fmt.Errorf("netsec: %s: NewKey requires a local id", at)
			}
			if _, ok := locals[v.LocalID]; ok {
				return fmt.Errorf("netsec: %s: duplicate local key %q", at, v.LocalID)
			}
			if v.Epoch != "" {
				if _, ok := epochs[v.Epoch]; !ok {
					return fmt.Errorf("netsec: %s: unknown epoch parameter %q", at, v.Epoch)
				}
			}
			locals[v.LocalID] = struct{}{}
		case Encrypt, Sign, Derive:
			var key Term
			switch c := n.(type) {
			case Encrypt:
				key = c.Key
			case Sign:
				key = c.Key
			default:
				key = n.(Derive).Key
			}
			if !validKey(key) {
				return fmt.Errorf("netsec: %s: unknown key term %v", at, key)
			}
		case BindKey:
			if !validKey(v.Target) || !validKey(v.Source) {
				return fmt.Errorf("netsec: %s: unknown key term in bind", at)
			}
		case Call:
			if v.Func == name {
				return fmt.Errorf("netsec: %s: direct recursion must use Recurse", at)
			}
		case Recurse:
			if _, ok := epochs[v.Epoch]; !ok {
				return fmt.Errorf("netsec: %s: Recurse uses unknown epoch %q", at, v.Epoch)
			}
		default:
			return fmt.Errorf("netsec: %s: unsupported node %T", at, n)
		}
	}
	return nil
}

// Internal operation kinds used to mark pending template invocations.
const (
	kindCall Kind = "call"
	kindRole Kind = "role"
)
