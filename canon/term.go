// Package canon canonicalizes symbolic authentication-model states.
//
// Freshly generated nonces ("fresh atoms") enlarge the state space on every
// random draw. Two concrete states that differ only in the concrete names of
// those nonces represent the same symbolic state. Canonicalize removes that
// representational noise:
//
//   - A fresh atom is identified solely by the session that generated it and
//     by its equality relation within that session (the order in which the
//     distinct atoms of that session are observed).
//   - Fixed public constants and function names are never renamed.
//   - The rewrite covers the whole state at once: message queue, role
//     variables and attacker knowledge.
//
// The returned Certificate records the bijection between original and
// canonical atom names and can independently reproduce the canonical state.
package canon

// Term is a symbolic message term.
type Term interface {
	term()
}

// Const is a fixed public constant (agent name, public constant, tag, ...).
// Constants are never renamed during canonicalization.
type Const struct {
	Name string
}

// Fresh is a freshly generated atom (nonce, key material, ...).
//
// Session identifies the protocol session that generated the atom. UID is the
// concrete, run-specific atom identifier; it is only used to tell distinct
// atoms apart and is replaced by a stable canonical name.
type Fresh struct {
	Session string
	UID     string
}

// Tuple is an ordered concatenation of terms.
type Tuple struct {
	Items []Term
}

// SymEnc is a symmetric encryption: Ciphertext = SymEnc(Plaintext, Key).
type SymEnc struct {
	Plaintext Term
	Key       Term
}

// PKEEnc is an asymmetric encryption: Ciphertext = PKEEnc(Plaintext, PubKey).
type PKEEnc struct {
	Plaintext Term
	PubKey    Term
}

// FuncApp is an application of a named public function (hash, mac, key
// derivation, ...). Function names are public and never renamed.
type FuncApp struct {
	Func string
	Args []Term
}

func (Const) term()   {}
func (Fresh) term()   {}
func (Tuple) term()   {}
func (SymEnc) term()  {}
func (PKEEnc) term()  {}
func (FuncApp) term() {}

// Equal reports whether two terms are structurally identical.
func Equal(a, b Term) bool {
	return termsEqual(a, b)
}

func termsEqual(a, b Term) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case Const:
		y, ok := b.(Const)
		return ok && x.Name == y.Name
	case Fresh:
		y, ok := b.(Fresh)
		return ok && x.Session == y.Session && x.UID == y.UID
	case Tuple:
		y, ok := b.(Tuple)
		if !ok || len(x.Items) != len(y.Items) {
			return false
		}
		for i := range x.Items {
			if !termsEqual(x.Items[i], y.Items[i]) {
				return false
			}
		}
		return true
	case SymEnc:
		y, ok := b.(SymEnc)
		return ok && termsEqual(x.Plaintext, y.Plaintext) && termsEqual(x.Key, y.Key)
	case PKEEnc:
		y, ok := b.(PKEEnc)
		return ok && termsEqual(x.Plaintext, y.Plaintext) && termsEqual(x.PubKey, y.PubKey)
	case FuncApp:
		y, ok := b.(FuncApp)
		if !ok || x.Func != y.Func || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !termsEqual(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// walk visits every term in preorder (parent before children, children in
// declared order), including the root. Nil terms are skipped.
func walk(t Term, visit func(Term)) {
	if t == nil {
		return
	}
	visit(t)
	switch x := t.(type) {
	case Tuple:
		for _, item := range x.Items {
			walk(item, visit)
		}
	case SymEnc:
		walk(x.Plaintext, visit)
		walk(x.Key, visit)
	case PKEEnc:
		walk(x.Plaintext, visit)
		walk(x.PubKey, visit)
	case FuncApp:
		for _, arg := range x.Args {
			walk(arg, visit)
		}
	}
}
