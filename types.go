package netsec

// Purpose is the intended usage of a key term. The teaching protocol's bug is
// treating every key as the same atom: an encryption key must not be accepted
// as an authentication key, and a derivation key must not be used either.
type Purpose string

const (
	// PurposeEncrypt marks keys used to encrypt (and decrypt) messages.
	PurposeEncrypt Purpose = "encrypt"
	// PurposeVerify marks keys used to verify signatures.
	PurposeVerify Purpose = "verify"
	// PurposeDerive marks keys fed to a key-derivation construct.
	PurposeDerive Purpose = "derive"
)

// Kind enumerates the message constructors recognized by the verifier. The
// three constructors are intentionally distinct so that a key flowing through
// the wrong one is detected instead of being silently accepted.
type Kind string

const (
	// KindNewKey binds a freshly generated key to its declared type.
	KindNewKey Kind = "newkey"
	// KindEncrypt is an encryption message constructor.
	KindEncrypt Kind = "encrypt"
	// KindSign is a signature/authentication message constructor.
	KindSign Kind = "sign"
	// KindDerive is a key-derivation constructor.
	KindDerive Kind = "derive"
	// KindBindKey asserts that two key terms denote the same key atom, for
	// example when unwrapping an envelope yields an inner key. It introduces
	// an aliasing (same-key) constraint between the two terms.
	KindBindKey Kind = "bindkey"
)

// KeyType is the type inferred for a key term. Every dimension is a finite
// domain. An empty field means the dimension is still unconstrained.
type KeyType struct {
	// Owner is the principal the key belongs to.
	Owner string
	// Purpose is the allowed usage of the key.
	Purpose Purpose
	// SessionID identifies the session generation (epoch) of the key.
	SessionID string
}

// Term is an abstract term of the small scripting language.
type Term interface{ term() }

// Param is a function parameter. It is type-polymorphic: its type is inferred
// from the constraints accumulated at each concrete call site.
type Param struct{ Name string }

// Local is a key bound by a NewKey node inside a function instantiation.
type Local struct{ ID string }

// Literal is a global long-term key atom referenced by name.
type Literal struct{ Key string }

func (Param) term()   {}
func (Local) term()   {}
func (Literal) term() {}

// P returns a key-typed function parameter term.
func P(name string) Param { return Param{Name: name} }

// L returns a reference to a local key declared inside a function body.
func L(id string) Local { return Local{ID: id} }

// K returns a reference to a global literal key atom.
func K(name string) Literal { return Literal{Key: name} }

// Operation is a fully instantiated, concrete script operation produced by
// expanding function calls and role templates. Only Literal and Local terms
// remain at this stage; Param terms must have been substituted away.
//
// Field usage by Kind:
//   - KindNewKey:   Owner, Purpose and Session carry the declared key type.
//   - KindEncrypt/Sign/KindDerive: Purpose is implied by the constructor.
//   - KindBindKey:  Payload holds the aliased source key; Key is the target.
type Operation struct {
	ID      string
	Kind    Kind
	Key     Term
	Payload []Term
	Owner   string
	Session string
	Purpose Purpose
}

// TermLabel renders the stable label of a concrete key term as used in
// Report.KeyTypes. Literal keys are prefixed so they can never collide with
// instantiated local identifiers.
func TermLabel(t Term) string {
	switch v := t.(type) {
	case Local:
		return v.ID
	case Literal:
		return "literal:" + v.Key
	default:
		return ""
	}
}
