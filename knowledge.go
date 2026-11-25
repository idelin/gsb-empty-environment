package symexec

import (
	"errors"
	"fmt"
	"sort"
)

// DefaultCompositionDepth is the default bound on newly composed attacker
// messages: an atom has depth zero and every pair/encryption constructor adds
// one. The default is three, as required by the protocol platform.
const DefaultCompositionDepth = 3

// DefaultTermBudget bounds the size of the attacker term universe. Search
// reports StatusUnknown when it is exhausted.
const DefaultTermBudget = 250000

var (
	// ErrTermBudget is returned when attacker-side term enumeration exceeds
	// the configured budget.
	ErrTermBudget = errors.New("attacker term budget exhausted")
	// ErrInvalidTerm is returned for malformed terms.
	ErrInvalidTerm = errors.New("invalid term")
)

// Rule identifies one inference in a replayable attacker derivation.
type Rule string

const (
	RuleInitial  Rule = "initial"  // leaf: term is initial knowledge
	RuleObserved Rule = "observed" // leaf: term was sent on the trace
	RuleFst      Rule = "fst"      // pair projection, left component
	RuleSnd      Rule = "snd"      // pair projection, right component
	RuleDecrypt  Rule = "decrypt"  // decrypt with a known symmetric key
	RulePair     Rule = "pair"     // compose a pair
	RuleEncrypt  Rule = "encrypt"  // compose a symmetric encryption
)

// Derivation is a replayable certificate for an attacker-known term. Leaves
// are RuleInitial or RuleObserved; every internal node states one inference and
// its premises. The tree contains no cycles.
type Derivation struct {
	Term     Term          `json:"term"`
	Rule     Rule          `json:"rule"`
	Children []*Derivation `json:"children,omitempty"`
	// Source is advisory metadata for observed leaves: the trace step at
	// which the term was first observed. A verifier should still check the
	// observed prefix it is given.
	Source int `json:"source,omitempty"`
}

// Certificate is an alias for Derivation, matching the terminology used in
// protocol-verification APIs.
type Certificate = Derivation

// Proof is another convenient alias.
type Proof = Derivation

// Limits controls attacker construction.
type Limits struct {
	// MaxComposeDepth is the maximum depth of a newly composed term. Atoms
	// have depth zero. A non-positive value selects DefaultCompositionDepth.
	MaxComposeDepth int `json:"maxComposeDepth,omitempty"`
	// MaxTerms bounds the number of distinct terms enumerated. Zero selects
	// DefaultTermBudget; a negative value means unbounded and is intended
	// only for small teaching examples.
	MaxTerms int `json:"maxTerms,omitempty"`
}

// Normalize fills in defaults.
func (l Limits) Normalize() Limits {
	if l.MaxComposeDepth <= 0 {
		l.MaxComposeDepth = DefaultCompositionDepth
	}
	if l.MaxTerms == 0 {
		l.MaxTerms = DefaultTermBudget
	}
	return l
}

func (l Limits) maxTerms() int {
	if l.MaxTerms < 0 {
		return 0
	}
	if l.MaxTerms == 0 {
		return DefaultTermBudget
	}
	return l.MaxTerms
}

// Candidate is one attacker-constructible message that matches a receive
// pattern, together with the pattern bindings and a replayable proof.
type Candidate struct {
	Term       Term
	Binding    map[string]Term
	Derivation *Derivation
	// Replay is true when the message is already known without a new
	// composition. Replay candidates are explored first because protocol
	// attacks usually proceed through observed messages.
	Replay bool
}

// Knowledge is the closure used by the attacker for one trace prefix. It is
// derived from initial knowledge plus messages already sent by honest roles.
// It never invents atoms: only atoms present in those sources can appear.
type Knowledge struct {
	initial       []Term
	observed      []Term
	limits        Limits
	decomp        map[string]*Derivation
	decompOrder   []*Derivation
	universes     map[int][]Candidate
	termBudgetHit bool
}

// NewKnowledge constructs the attacker closure for the given prefix.
func NewKnowledge(initial, observed []Term, settings ...Limits) (*Knowledge, error) {
	limits := Limits{}
	if len(settings) > 0 {
		limits = settings[0]
	}
	limits = limits.Normalize()
	k := &Knowledge{
		initial:   cloneTerms(initial),
		observed:  cloneTerms(observed),
		limits:    limits,
		decomp:    make(map[string]*Derivation),
		universes: make(map[int][]Candidate),
	}
	for _, term := range k.initial {
		if !term.Valid() || !term.Ground() {
			return nil, fmt.Errorf("%w: initial term %s", ErrInvalidTerm, term)
		}
		if err := k.addDecomp(&Derivation{Term: term, Rule: RuleInitial, Source: -1}); err != nil {
			return nil, err
		}
	}
	for i, term := range k.observed {
		if !term.Valid() || !term.Ground() {
			return nil, fmt.Errorf("%w: observed term %s", ErrInvalidTerm, term)
		}
		if err := k.addDecomp(&Derivation{Term: term, Rule: RuleObserved, Source: i}); err != nil {
			return nil, err
		}
	}
	if err := k.saturateDecomposition(); err != nil {
		return nil, err
	}
	return k, nil
}

// Derive is a convenience wrapper for a one-shot closure.
func Derive(initial, observed []Term, limits ...Limits) (*Knowledge, error) {
	return NewKnowledge(initial, observed, limits...)
}

func cloneTerms(in []Term) []Term {
	out := make([]Term, len(in))
	copy(out, in)
	return out
}

func (k *Knowledge) addDecomp(d *Derivation) error {
	key := d.Term.Key()
	if _, ok := k.decomp[key]; ok {
		return nil
	}
	if max := k.limits.maxTerms(); max > 0 && len(k.decomp) >= max {
		k.termBudgetHit = true
		return ErrTermBudget
	}
	k.decomp[key] = d
	k.decompOrder = append(k.decompOrder, d)
	return nil
}

func (k *Knowledge) saturateDecomposition() error {
	for {
		changed := false
		snapshot := append([]*Derivation(nil), k.decompOrder...)
		for _, d := range snapshot {
			switch d.Term.Kind {
			case KindPair:
				left := &Derivation{
					Term:     *d.Term.Left,
					Rule:     RuleFst,
					Children: []*Derivation{d},
					Source:   -1,
				}
				right := &Derivation{
					Term:     *d.Term.Right,
					Rule:     RuleSnd,
					Children: []*Derivation{d},
					Source:   -1,
				}
				before := len(k.decomp)
				if err := k.addDecomp(left); err != nil {
					return err
				}
				if err := k.addDecomp(right); err != nil {
					return err
				}
				changed = changed || len(k.decomp) != before
			case KindEnc:
				key := *d.Term.Left
				keyProof, ok := k.decomp[key.Key()]
				if !ok {
					continue
				}
				plaintext := &Derivation{
					Term:     *d.Term.Right,
					Rule:     RuleDecrypt,
					Children: []*Derivation{d, keyProof},
					Source:   -1,
				}
				before := len(k.decomp)
				if err := k.addDecomp(plaintext); err != nil {
					return err
				}
				changed = changed || len(k.decomp) != before
			}
		}
		if !changed {
			return nil
		}
		// Decryptions can expose new pairs, which can then expose keys
		// needed by other encryptions. Re-scan until stable.
	}
}

// Decompositions returns all terms the attacker knows without composing new
// messages: initial/observed terms and their freely decomposable parts.
func (k *Knowledge) Decompositions() []*Derivation {
	out := make([]*Derivation, len(k.decompOrder))
	copy(out, k.decompOrder)
	return out
}

// Knows reports whether the term is available without new composition.
func (k *Knowledge) Knows(term Term) bool {
	_, ok := k.decomp[term.Key()]
	return ok
}

// Match returns every message the attacker can construct that matches the
// receive pattern. Pattern variables are bound consistently. The returned
// candidates are deterministic and sorted by term key.
func (k *Knowledge) Match(pattern Term) ([]Candidate, error) {
	if !pattern.Valid() {
		return nil, fmt.Errorf("%w: pattern %s", ErrInvalidTerm, pattern)
	}
	candidates, err := k.synthesize(pattern, k.limits.MaxComposeDepth)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if candidates[i].Binding == nil {
			candidates[i].Binding = map[string]Term{}
		}
	}
	if max := k.limits.maxTerms(); max > 0 && len(candidates) > max {
		k.termBudgetHit = true
		return nil, ErrTermBudget
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Replay != candidates[j].Replay {
			return candidates[i].Replay
		}
		if candidates[i].Term.Depth() != candidates[j].Term.Depth() {
			return candidates[i].Term.Depth() < candidates[j].Term.Depth()
		}
		return candidates[i].Term.Key() < candidates[j].Term.Key()
	})
	return candidates, nil
}

// Candidates is an alias for Match.
func (k *Knowledge) Candidates(pattern Term) ([]Candidate, error) {
	return k.Match(pattern)
}

// Messages is a term-only convenience wrapper around Match.
func (k *Knowledge) Messages(pattern Term) ([]Term, error) {
	candidates, err := k.Match(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]Term, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Term)
	}
	return out, nil
}

func (k *Knowledge) synthesize(pattern Term, remaining int) ([]Candidate, error) {
	switch pattern.Kind {
	case KindVar:
		return k.universe(remaining)
	case KindAtom:
		return k.replay(pattern)
	case KindPair, KindEnc:
		replay, err := k.replay(pattern)
		if err != nil {
			return nil, err
		}
		if remaining < 1 {
			return replay, nil
		}
		var constructed []Candidate
		maxTerms := k.limits.maxTerms()
		if pattern.Kind == KindPair {
			lefts, err := k.synthesize(*pattern.Left, remaining-1)
			if err != nil {
				return nil, err
			}
			rights, err := k.synthesize(*pattern.Right, remaining-1)
			if err != nil {
				return nil, err
			}
			for _, left := range lefts {
				for _, right := range rights {
					binding, ok := mergeBindings(left.Binding, right.Binding)
					if !ok {
						continue
					}
					term := Pair(left.Term, right.Term)
					if term.Depth() > remaining {
						continue
					}
					constructed = append(constructed, Candidate{
						Term:    term,
						Binding: binding,
						Derivation: &Derivation{
							Term:     term,
							Rule:     RulePair,
							Children: []*Derivation{left.Derivation, right.Derivation},
							Source:   -1,
						},
					})
					if maxTerms > 0 && len(constructed) > maxTerms {
						k.termBudgetHit = true
						return nil, ErrTermBudget
					}
				}
			}
		} else {
			keys, err := k.synthesize(*pattern.Left, remaining-1)
			if err != nil {
				return nil, err
			}
			messages, err := k.synthesize(*pattern.Right, remaining-1)
			if err != nil {
				return nil, err
			}
			for _, key := range keys {
				if key.Term.Kind != KindAtom {
					continue
				}
				for _, msg := range messages {
					binding, ok := mergeBindings(key.Binding, msg.Binding)
					if !ok {
						continue
					}
					term := Enc(key.Term, msg.Term)
					if term.Depth() > remaining {
						continue
					}
					constructed = append(constructed, Candidate{
						Term:    term,
						Binding: binding,
						Derivation: &Derivation{
							Term:     term,
							Rule:     RuleEncrypt,
							Children: []*Derivation{key.Derivation, msg.Derivation},
							Source:   -1,
						},
					})
					if maxTerms > 0 && len(constructed) > maxTerms {
						k.termBudgetHit = true
						return nil, ErrTermBudget
					}
				}
			}
		}
		return dedupeCandidates(append(replay, constructed...)), nil
	default:
		return nil, nil
	}
}

func (k *Knowledge) replay(pattern Term) ([]Candidate, error) {
	var out []Candidate
	for _, d := range k.decompOrder {
		binding, ok := Match(pattern, d.Term, nil)
		if !ok {
			continue
		}
		out = append(out, Candidate{Term: d.Term, Binding: binding, Derivation: d, Replay: true})
		if k.limits.maxTerms() > 0 && len(out) > k.limits.maxTerms() {
			k.termBudgetHit = true
			return nil, ErrTermBudget
		}
	}
	return out, nil
}

func (k *Knowledge) universe(depth int) ([]Candidate, error) {
	if depth < 0 {
		depth = 0
	}
	if cached, ok := k.universes[depth]; ok {
		return cached, nil
	}
	all := make(map[string]*Derivation, len(k.decomp))
	for _, d := range k.decompOrder {
		all[d.Term.Key()] = d
	}
	maxTerms := k.limits.maxTerms()
	add := func(d *Derivation) error {
		if _, ok := all[d.Term.Key()]; ok {
			return nil
		}
		if maxTerms > 0 && len(all) >= maxTerms {
			k.termBudgetHit = true
			return ErrTermBudget
		}
		all[d.Term.Key()] = d
		return nil
	}
	for level := 1; level <= depth; level++ {
		leaves := make([]*Derivation, 0, len(all))
		for _, d := range all {
			if d.Term.Depth() <= level-1 {
				leaves = append(leaves, d)
			}
		}
		sort.Slice(leaves, func(i, j int) bool {
			return leaves[i].Term.Key() < leaves[j].Term.Key()
		})
		for _, left := range leaves {
			for _, right := range leaves {
				term := Pair(left.Term, right.Term)
				if term.Depth() > level {
					continue
				}
				if err := add(&Derivation{
					Term:     term,
					Rule:     RulePair,
					Children: []*Derivation{left, right},
					Source:   -1,
				}); err != nil {
					return nil, err
				}
			}
		}
		for _, key := range leaves {
			if key.Term.Kind != KindAtom {
				continue
			}
			for _, msg := range leaves {
				term := Enc(key.Term, msg.Term)
				if term.Depth() > level {
					continue
				}
				if err := add(&Derivation{
					Term:     term,
					Rule:     RuleEncrypt,
					Children: []*Derivation{key, msg},
					Source:   -1,
				}); err != nil {
					return nil, err
				}
			}
		}
	}
	out := make([]Candidate, 0, len(all))
	ordered := make([]*Derivation, 0, len(all))
	for _, d := range all {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Term.Key() < ordered[j].Term.Key()
	})
	for _, d := range ordered {
		out = append(out, Candidate{
			Term:       d.Term,
			Binding:    map[string]Term{},
			Derivation: d,
			Replay:     replayRule(d.Rule),
		})
	}
	k.universes[depth] = out
	return out, nil
}

func replayRule(rule Rule) bool {
	switch rule {
	case RuleInitial, RuleObserved, RuleFst, RuleSnd, RuleDecrypt:
		return true
	default:
		return false
	}
}

func dedupeCandidates(in []Candidate) []Candidate {
	seen := make(map[string]struct{}, len(in))
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		key := c.Term.Key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

func mergeBindings(a, b map[string]Term) (map[string]Term, bool) {
	out := make(map[string]Term, len(a)+len(b))
	for name, value := range a {
		out[name] = value
	}
	for name, value := range b {
		if old, ok := out[name]; ok && !old.Equal(value) {
			return nil, false
		}
		out[name] = value
	}
	return out, true
}

// Verify checks a certificate against this knowledge snapshot. It is fully
// replayable: every leaf, inference and depth bound is re-checked.
func (k *Knowledge) Verify(cert *Derivation) error {
	if cert == nil {
		return errors.New("nil derivation certificate")
	}
	return VerifyDerivation(cert, k.initial, k.observed, k.limits)
}

// VerifyDerivation replays a derivation certificate. initial terms are always
// available; observed terms must belong to the supplied prefix. Composition
// rules are checked against the configured depth bound.
func VerifyDerivation(cert *Derivation, initial, observed []Term, settings ...Limits) error {
	limits := Limits{}
	if len(settings) > 0 {
		limits = settings[0]
	}
	limits = limits.Normalize()
	return verifyDerivation(cert, initial, observed, limits, make(map[*Derivation]bool))
}

// VerifyCertificate is an alias for VerifyDerivation.
func VerifyCertificate(cert *Derivation, initial, observed []Term, limits ...Limits) error {
	return VerifyDerivation(cert, initial, observed, limits...)
}

func verifyDerivation(cert *Derivation, initial, observed []Term, limits Limits, active map[*Derivation]bool) error {
	if cert == nil {
		return errors.New("nil derivation node")
	}
	if active[cert] {
		return errors.New("cyclic derivation certificate")
	}
	if !cert.Term.Valid() {
		return fmt.Errorf("%w in derivation", ErrInvalidTerm)
	}
	if !cert.Term.Ground() {
		return errors.New("derivation contains variables")
	}
	active[cert] = true
	defer delete(active, cert)

	switch cert.Rule {
	case RuleInitial:
		if !containsTerm(initial, cert.Term) {
			return fmt.Errorf("term %s is not in initial knowledge", cert.Term)
		}
	case RuleObserved:
		if !containsTerm(observed, cert.Term) {
			return fmt.Errorf("term %s was not observed in the trace prefix", cert.Term)
		}
	case RuleFst:
		if len(cert.Children) != 1 {
			return errors.New("fst rule needs one premise")
		}
		child := cert.Children[0]
		if child.Term.Kind != KindPair || child.Term.Left == nil ||
			!child.Term.Left.Equal(cert.Term) {
			return errors.New("fst premise is not a pair with the claimed component")
		}
	case RuleSnd:
		if len(cert.Children) != 1 {
			return errors.New("snd rule needs one premise")
		}
		child := cert.Children[0]
		if child.Term.Kind != KindPair || child.Term.Right == nil ||
			!child.Term.Right.Equal(cert.Term) {
			return errors.New("snd premise is not a pair with the claimed component")
		}
	case RulePair:
		if len(cert.Children) != 2 {
			return errors.New("pair rule needs two premises")
		}
		term := Pair(cert.Children[0].Term, cert.Children[1].Term)
		if !term.Equal(cert.Term) {
			return errors.New("pair rule does not produce the claimed term")
		}
		if term.Depth() > limits.MaxComposeDepth {
			return fmt.Errorf("pair depth %d exceeds composition bound %d", term.Depth(), limits.MaxComposeDepth)
		}
	case RuleEncrypt:
		if len(cert.Children) != 2 {
			return errors.New("encrypt rule needs two premises")
		}
		key, msg := cert.Children[0].Term, cert.Children[1].Term
		if key.Kind != KindAtom {
			return errors.New("symmetric encryption key must be an atom")
		}
		term := Enc(key, msg)
		if !term.Equal(cert.Term) {
			return errors.New("encrypt rule does not produce the claimed term")
		}
		if term.Depth() > limits.MaxComposeDepth {
			return fmt.Errorf("encryption depth %d exceeds composition bound %d", term.Depth(), limits.MaxComposeDepth)
		}
	case RuleDecrypt:
		if len(cert.Children) != 2 {
			return errors.New("decrypt rule needs ciphertext and key premises")
		}
		cipher, key := cert.Children[0].Term, cert.Children[1].Term
		if cipher.Kind != KindEnc || cipher.Left == nil || cipher.Right == nil ||
			!cipher.Left.Equal(key) || !cipher.Right.Equal(cert.Term) {
			return errors.New("decrypt rule does not produce the claimed plaintext")
		}
	default:
		return fmt.Errorf("unknown derivation rule %q", cert.Rule)
	}
	for _, child := range cert.Children {
		if err := verifyDerivation(child, initial, observed, limits, active); err != nil {
			return err
		}
	}
	return nil
}

func containsTerm(terms []Term, want Term) bool {
	key := want.Key()
	for _, term := range terms {
		if term.Key() == key {
			return true
		}
	}
	return false
}
