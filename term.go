// Package netsec implements a Dolev-Yao style knowledge closure over a
// message algebra of names, nonces, symmetric keys, pairs and symmetric
// encryption. Every fact carries the sequence number of the event at which
// the attacker first obtains it. Closure rules take the maximum of the
// availability times of their premises, so a key learned only after a
// session ends cannot be used to explain messages observed earlier.
package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind enumerates the term constructors understood by the closure engine.
type Kind int

const (
	// Name is a public agent or principal identifier, e.g. a.
	Name Kind = iota
	// Nonce is an unpredictable fresh value, e.g. n.
	Nonce
	// Key is a symmetric key, e.g. k.
	Key
	// Pair is the binary tuple Pair(First, Second).
	Pair
	// Enc is symmetric encryption Enc(Plaintext, Key).
	Enc
)

func (k Kind) String() string {
	switch k {
	case Name:
		return "Name"
	case Nonce:
		return "Nonce"
	case Key:
		return "Key"
	case Pair:
		return "Pair"
	case Enc:
		return "Enc"
	default:
		return "Kind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Term is the external, value representation of a message term.
// Children are empty for Name, Nonce and Key; Pair and Enc carry exactly
// two children (for Enc the second child is the key term).
type Term struct {
	Kind     Kind
	Name     string
	Children []Term
}

// String renders the term in its canonical textual syntax:
//
//	a | n | k | pair(t1,t2) | enc(t,k)
func (t Term) String() string {
	switch t.Kind {
	case Name, Nonce, Key:
		return t.Name
	case Pair:
		return "pair(" + t.Children[0].String() + "," + t.Children[1].String() + ")"
	case Enc:
		return "enc(" + t.Children[0].String() + "," + t.Children[1].String() + ")"
	default:
		panic("netsec: unknown term kind")
	}
}

// MarshalText implements encoding.TextMarshaler so terms serialize as their
// canonical syntax inside JSON structures.
func (t Term) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

// UnmarshalText parses the canonical syntax, enabling JSON round trips of
// terms embedded in host-facing reports.
func (t *Term) UnmarshalText(data []byte) error {
	parsed, err := ParseTerm(string(data))
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// Equal reports whether two terms have identical structure.
func (t Term) Equal(other Term) bool {
	if t.Kind != other.Kind || t.Name != other.Name || len(t.Children) != len(other.Children) {
		return false
	}
	for i := range t.Children {
		if !t.Children[i].Equal(other.Children[i]) {
			return false
		}
	}
	return true
}

// termRec is the internal hash-consed representation of a term.
type termRec struct {
	term      Term
	canonical string
}

// parser implements a small recursive-descent parser for the canonical
// grammar:
//
//	term := atom | "pair" "(" term "," term ")" | "enc" "(" term "," term ")"
//	atom := [A-Za-z_][A-Za-z0-9_]*
type parser struct {
	input string
	pos   int
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.input) {
		switch p.input[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) readIdent() (string, error) {
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(p.pos > start && c >= '0' && c <= '9') {
			p.pos++
			continue
		}
		break
	}
	if start == p.pos {
		return "", fmt.Errorf("netsec: expected identifier at position %d of %q", p.pos, p.input)
	}
	return p.input[start:p.pos], nil
}

func (p *parser) expect(c byte) error {
	p.skipSpaces()
	if p.pos >= len(p.input) || p.input[p.pos] != c {
		return fmt.Errorf("netsec: expected %q at position %d of %q", c, p.pos, p.input)
	}
	p.pos++
	return nil
}

func (p *parser) parseTerm() (Term, error) {
	p.skipSpaces()
	ident, err := p.readIdent()
	if err != nil {
		return Term{}, err
	}
	switch ident {
	case "pair", "enc":
		if err := p.expect('('); err != nil {
			return Term{}, err
		}
		first, err := p.parseTerm()
		if err != nil {
			return Term{}, err
		}
		if err := p.expect(','); err != nil {
			return Term{}, err
		}
		second, err := p.parseTerm()
		if err != nil {
			return Term{}, err
		}
		if err := p.expect(')'); err != nil {
			return Term{}, err
		}
		kind := Pair
		if ident == "enc" {
			kind = Enc
		}
		return Term{Kind: kind, Children: []Term{first, second}}, nil
	}

	kind, err := atomKind(ident)
	if err != nil {
		return Term{}, err
	}
	return Term{Kind: kind, Name: ident}, nil
}

func atomKind(ident string) (Kind, error) {
	switch {
	case strings.HasPrefix(ident, "n"):
		return Nonce, nil
	case strings.HasPrefix(ident, "k"):
		return Key, nil
	case strings.HasPrefix(ident, "a"):
		return Name, nil
	default:
		return 0, fmt.Errorf("netsec: atom %q must start with a (name), n (nonce) or k (key)", ident)
	}
}

// ParseTerm parses a term from its canonical textual syntax. Atom names
// must start with a (agent name), n (nonce) or k (symmetric key), followed
// by letters, digits or underscores.
func ParseTerm(s string) (Term, error) {
	p := &parser{input: s}
	t, err := p.parseTerm()
	if err != nil {
		return Term{}, err
	}
	p.skipSpaces()
	if p.pos != len(s) {
		return Term{}, fmt.Errorf("netsec: trailing input at position %d of %q", p.pos, s)
	}
	if err := validateTerm(t); err != nil {
		return Term{}, err
	}
	return t, nil
}

func validateTerm(t Term) error {
	switch t.Kind {
	case Name, Nonce, Key:
		if t.Name == "" {
			return errors.New("netsec: atom term requires a name")
		}
		if _, err := atomKind(t.Name); err != nil {
			return err
		}
		if len(t.Children) != 0 {
			return fmt.Errorf("netsec: atom %s must have no children", t.Name)
		}
	case Pair, Enc:
		if len(t.Children) != 2 {
			return fmt.Errorf("netsec: %s requires exactly two children", t.Kind)
		}
		if t.Name != "" {
			return fmt.Errorf("netsec: %s must carry no atom name", t.Kind)
		}
		for _, child := range t.Children {
			if err := validateTerm(child); err != nil {
				return err
			}
		}
		if t.Kind == Enc && t.Children[1].Kind != Key {
			return fmt.Errorf("netsec: enc second child %s must be a key term", t.Children[1].String())
		}
	default:
		return fmt.Errorf("netsec: invalid term kind %d", t.Kind)
	}
	return nil
}
