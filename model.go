// Package netsec implements a small symbolic "can the adversary decrypt this
// yet?" model for teaching protocol analysis.
//
// The model follows Dolev–Yao style attacker knowledge over a timeline:
//
//   - Terms are built from names (atomic secrets and keys), pairs, symmetric
//     encryptions and one-way derivations.
//   - A pair yields its components and vice versa.
//   - An encryption {t}_k is opened exactly when the term t is already
//     available and the matching key term k is already available.
//   - A derivation D(t1,...,tn) is computable only after every input ti is
//     known; a derivation never exposes its inputs (one-way).
//   - Send(t, at) puts t on the wire at time at, so every subterm that can be
//     stripped without a key (the term itself, pair components, nested pair
//     structure and the plaintext-protecting ciphertext shape) becomes known
//     no later than at.
//   - Compromise(k, at) leaks key term k at time at, but only when k has not
//     been destroyed before or at at.
//   - Destroy(k, at) records the earliest destruction time of k. Destruction
//     only prevents later compromise of k; it never removes knowledge the
//     attacker already held, and it does not affect messages already sent.
//
// Attacker knowledge is monotone in time. Analyze returns, for every
// historical secret handed in, the earliest time at which the attacker can
// derive it together with a proof tree naming the rule used at each step.
//
// A Model is not safe for concurrent use; an analysis host builds one model
// per scenario and then calls Analyze.
package netsec

// Kind enumerates the constructors a Term may use.
type Kind int

const (
	// Name is an atomic term: a nonce, a payload or a key.
	Name Kind = iota
	// Pair is the ordered pair of two terms.
	Pair
	// Enc is symmetric encryption Enc(key, plaintext).
	Enc
	// Derive is the one-way derivation Derive(inputs...).
	Derive
)

// Rule names the closure rule recorded in a Proof node.
type Rule string

const (
	// RuleSend marks knowledge obtained from a message on the wire.
	RuleSend Rule = "send"
	// RuleCompromise marks a key learned from a compromise event.
	RuleCompromise Rule = "compromise"
	// RulePair marks a term learned by splitting a known pair.
	RulePair Rule = "pair"
	// RulePairBuild marks a pair assembled from two known components.
	RulePairBuild Rule = "pair_build"
	// RuleDecrypt marks a plaintext learned by decrypting with a known key.
	RuleDecrypt Rule = "decrypt"
	// RuleEncrypt marks a ciphertext assembled from a known key and plaintext.
	RuleEncrypt Rule = "encrypt"
	// RuleDerive marks a derivation computed once every input is known.
	RuleDerive Rule = "derive"
)

// Term is an interned symbolic term. Identity is given by ID: terms created
// via the same constructor arguments share an ID, while terms created by
// different Model values are unrelated.
type Term struct {
	id    int
	kind  Kind
	label string
	args  []Term
}

// Kind reports the constructor of the term.
func (t Term) Kind() Kind { return t.kind }

// ID returns the model-unique interned identifier of the term. Two terms with
// the same ID within one Model are the same term.
func (t Term) ID() int { return t.id }

// Label returns the label of a Name term; it returns "" for composite terms.
func (t Term) Label() string { return t.label }

// Args returns the immediate subterms:
//
//   - Pair: [left, right]
//   - Enc:  [key, plaintext]
//   - Derive: the ordered derivation inputs
//   - Name: nil
func (t Term) Args() []Term { return t.args }

// Event identifies a timeline entry by its 1-based insertion index.
type Event struct {
	Index int
	Kind  string // "send", "compromise" or "destroy"
	Time  int
	Term  Term
}

// Model holds interned terms and the timeline of a single scenario.
type Model struct {
	terms   []Term
	intern  map[string]Term
	users   map[int][]int
	events  []Event
	destroy map[int]int
}

// NewModel returns an empty scenario model.
func NewModel() *Model {
	return &Model{
		intern:  make(map[string]Term),
		users:   make(map[int][]int),
		destroy: make(map[int]int),
	}
}

// Name interns and returns the atomic term with the given label. Repeated
// calls with the same label return the same Term.
func (m *Model) Name(label string) Term {
	return m.internTerm(Name, label, nil)
}

// Pair interns and returns the ordered pair of left and right.
func (m *Model) Pair(left, right Term) Term {
	return m.internTerm(Pair, "", []Term{left, right})
}

// Enc interns and returns symmetric encryption Enc(key, plaintext).
func (m *Model) Enc(key, plaintext Term) Term {
	return m.internTerm(Enc, "", []Term{key, plaintext})
}

// Derive interns and returns the one-way derivation of the ordered inputs.
// All inputs must become known before the derivation can be computed.
func (m *Model) Derive(inputs ...Term) Term {
	if len(inputs) == 0 {
		panic("netsec: Derive needs at least one input")
	}
	args := append([]Term(nil), inputs...)
	return m.internTerm(Derive, "", args)
}

func (m *Model) internTerm(kind Kind, label string, args []Term) Term {
	for _, a := range args {
		if a.id < 0 || a.id >= len(m.terms) || m.terms[a.id].id != a.id {
			panic("netsec: term was not created by this Model")
		}
	}
	key := termKey(kind, label, args)
	if t, ok := m.intern[key]; ok {
		return t
	}
	t := Term{id: len(m.terms), kind: kind, label: label, args: args}
	m.terms = append(m.terms, t)
	m.intern[key] = t
	for _, sub := range args {
		m.users[sub.id] = append(m.users[sub.id], t.id)
	}
	return t
}

func termKey(kind Kind, label string, args []Term) string {
	b := make([]byte, 0, 32)
	b = append(b, byte(kind))
	switch kind {
	case Name:
		b = append(b, label...)
	case Pair:
		b = append(b, 'p', byte(args[0].id), byte(args[1].id))
	case Enc:
		b = append(b, 'e', byte(args[0].id), byte(args[1].id))
	case Derive:
		b = append(b, 'd')
		for _, a := range args {
			b = append(b, byte(a.id))
			b = append(b, ',')
		}
	}
	return string(b)
}

// Send records that msg appears on the public wire at time at.
func (m *Model) Send(msg Term, at int) Event {
	return m.addEvent("send", msg, at)
}

// Compromise records that key term k leaks at time at. The leak is ignored by
// Analyze when k has a destruction time no later than at.
func (m *Model) Compromise(k Term, at int) Event {
	return m.addEvent("compromise", k, at)
}

// Destroy records that key term k is destroyed at time at. The earliest
// destruction time wins when the same key is destroyed repeatedly.
func (m *Model) Destroy(k Term, at int) Event {
	ev := m.addEvent("destroy", k, at)
	if prev, ok := m.destroy[k.id]; !ok || at < prev {
		m.destroy[k.id] = at
	}
	return ev
}

func (m *Model) addEvent(kind string, t Term, at int) Event {
	ev := Event{Index: len(m.events) + 1, Kind: kind, Time: at, Term: t}
	m.events = append(m.events, ev)
	return ev
}

// Events returns the timeline in insertion order.
func (m *Model) Events() []Event {
	out := append([]Event(nil), m.events...)
	return out
}
