package envguard

import (
	"errors"
	"fmt"
)

// Origin identifies where a character or control dependency comes from.
//
// Every input binding and every literal string gets a unique Origin. A
// binding's origin carries the owning tenant identity and trust domain; a
// literal is trusted by construction because the host authored it.
type Origin struct {
	// ID is the dense index assigned by the registry; zero is reserved.
	ID int
	// Tenant names the principal that supplied a binding. It is empty for
	// host-authored literal origins.
	Tenant string
	// Domain is the trust domain label used by the host's policy.
	Domain string
	// Name is the variable name within the tenant namespace.
	Name string
	// Literal marks host-authored constant content.
	Literal bool
	trusted bool
}

// Trusted reports whether the host policy marks this origin as trusted.
func (o *Origin) Trusted() bool { return o.trusted }

// Input is one tenant-supplied variable binding.
//
// Value is the bound string. Lang is the regular-language automaton that the
// host requires the value to satisfy; Bind rejects values outside Lang so the
// rest of the graph can rely on the declared character language.
type Input struct {
	Tenant  string
	Domain  string
	Name    string
	Value   string
	Lang    *DFA
	Trusted bool
}

type registry struct {
	origins     []*Origin
	index       map[originKey]int
	trustedMask []uint64
	inputs      map[originKey]inputRec
	sanitizers  map[string]*Sanitizer
}

type originKey struct {
	tenant string
	name   string
}

type inputRec struct {
	origin int
	dfa    *DFA
	value  string
}

func newRegistry() *registry {
	r := &registry{
		origins:    []*Origin{nil},
		index:      map[originKey]int{},
		inputs:     map[originKey]inputRec{},
		sanitizers: map[string]*Sanitizer{},
	}
	return r
}

func (r *registry) addTrustedMask(id int, trusted bool) {
	word := id >> 6
	for len(r.trustedMask) <= word {
		r.trustedMask = append(r.trustedMask, 0)
	}
	if trusted {
		r.trustedMask[word] |= 1 << uint(id&63)
	}
}

// bind registers an input binding, validates it against the declared
// language, and returns its origin.
func (r *registry) bind(in Input) (*Origin, error) {
	if in.Tenant == "" {
		return nil, errors.New("envguard: input tenant must be non-empty")
	}
	if in.Name == "" {
		return nil, errors.New("envguard: input name must be non-empty")
	}
	if in.Lang == nil {
		return nil, fmt.Errorf("envguard: input %s/%s needs a character language DFA", in.Tenant, in.Name)
	}
	key := originKey{in.Tenant, in.Name}
	if _, exists := r.index[key]; exists {
		return nil, fmt.Errorf("envguard: duplicate binding %s/%s", in.Tenant, in.Name)
	}
	if !in.Lang.AcceptsString(in.Value) {
		return nil, fmt.Errorf("envguard: value for %s/%s violates its declared language", in.Tenant, in.Name)
	}
	id := len(r.origins)
	o := &Origin{
		ID:      id,
		Tenant:  in.Tenant,
		Domain:  in.Domain,
		Name:    in.Name,
		trusted: in.Trusted,
	}
	r.origins = append(r.origins, o)
	r.index[key] = id
	r.inputs[key] = inputRec{origin: id, dfa: in.Lang, value: in.Value}
	r.addTrustedMask(id, in.Trusted)
	return o, nil
}

func (r *registry) valueOf(originID int) string {
	o := r.origins[originID]
	return r.inputs[originKey{o.Tenant, o.Name}].value
}

func (r *registry) literal(value string) *Origin {
	id := len(r.origins)
	o := &Origin{
		ID:      id,
		Name:    "literal",
		Literal: true,
		trusted: true,
	}
	r.origins = append(r.origins, o)
	r.addTrustedMask(id, true)
	return o
}

func (r *registry) lookup(tenant, name string) (*Origin, error) {
	id, ok := r.index[originKey{tenant, name}]
	if !ok {
		return nil, fmt.Errorf("envguard: unbound variable %s/%s", tenant, name)
	}
	return r.origins[id], nil
}

func (r *registry) registerSanitizer(s *Sanitizer) error {
	if s == nil || s.Name == "" {
		return errors.New("envguard: sanitizer needs a name")
	}
	if _, exists := r.sanitizers[s.Name]; exists {
		return fmt.Errorf("envguard: duplicate sanitizer %q", s.Name)
	}
	if err := s.validate(); err != nil {
		return err
	}
	r.sanitizers[s.Name] = s
	return nil
}

func (r *registry) sanitizer(name string) (*Sanitizer, error) {
	s, ok := r.sanitizers[name]
	if !ok {
		return nil, fmt.Errorf("envguard: unknown sanitizer %q", name)
	}
	return s, nil
}

// Describe renders an origin for the character-by-character explanation.
func (o *Origin) Describe() string {
	switch {
	case o.Literal:
		return "literal(trusted)"
	case o.trusted:
		return fmt.Sprintf("var %s/%s[%s](trusted)", o.Tenant, o.Name, o.Domain)
	default:
		return fmt.Sprintf("var %s/%s[%s](untrusted)", o.Tenant, o.Name, o.Domain)
	}
}
