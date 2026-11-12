// Package evidence implements the mail host release-rule evidence engine.
//
// An atomic fact is a named authentication claim produced by a mail access
// node. Every fact is bound to three pieces of evidence:
//   - the digest of the message it authenticates,
//   - an ed25519 signature from the access node over a canonical encoding of
//     the fact, and
//   - the authentication method that produced the claim.
//
// A fact evaluates to True or False only when its node is configured as
// trusted, its signature verifies, and its bound message digest matches the
// message under evaluation. Facts from untrusted nodes, facts with invalid
// signatures, malformed facts, and facts spliced from a different message all
// evaluate to Unknown. External declarations that reuse a trusted fact's name
// never participate in evaluation.
//
// Rules are evaluated with strong Kleene three-valued logic. A message is
// released only when the rule evaluates to True; Unknown never satisfies a
// negation the way False would. When the rule holds, the engine returns the
// minimum-cardinality sets of authenticated facts that make it hold.
package evidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// DefaultMaxFacts is the maximum number of authenticated facts an engine
// considers, matching the at-most-18-facts deployment limit.
const DefaultMaxFacts = 18

const payloadVersion = "mailevidence-fact/v1"

// Value is a strong Kleene truth value.
type Value uint8

const (
	// Unknown means no trusted, correctly signed fact binds the name.
	Unknown Value = iota
	// True means a trusted authenticated fact asserts the claim.
	True
	// False means a trusted authenticated fact denies the claim.
	False
)

// String returns "unknown", "true" or "false".
func (v Value) String() string {
	switch v {
	case True:
		return "true"
	case False:
		return "false"
	default:
		return "unknown"
	}
}

// Not is the strong Kleene negation: Not(Unknown) stays Unknown.
func Not(v Value) Value {
	switch v {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

// And is the strong Kleene conjunction: it is False if any operand is False,
// otherwise Unknown if any operand is Unknown, otherwise True.
func And(values ...Value) Value {
	result := True
	for _, v := range values {
		if v == False {
			return False
		}
		if v == Unknown {
			result = Unknown
		}
	}
	return result
}

// Or is the strong Kleene disjunction: it is True if any operand is True,
// otherwise Unknown if any operand is Unknown, otherwise False.
func Or(values ...Value) Value {
	result := False
	for _, v := range values {
		if v == True {
			return True
		}
		if v == Unknown {
			result = Unknown
		}
	}
	return result
}

// Fact is an atomic authentication claim bound to a message.
type Fact struct {
	// Name is the rule-level identifier the fact binds, e.g. "spf.pass".
	Name string
	// NodeID identifies the access node that produced and signed the fact.
	NodeID string
	// Method is the authentication mechanism that produced the claim, e.g.
	// "spf", "dkim" or "mtasts". It is covered by the signature.
	Method string
	// Message is the raw message the fact authenticates. Its SHA-256 digest
	// is covered by the signature.
	Message []byte
	// Claim is the authenticated boolean assertion.
	Claim bool
	// Signature is the node's ed25519 signature over the canonical payload.
	Signature []byte
}

// MessageDigest returns the SHA-256 digest bound by a fact.
func MessageDigest(message []byte) []byte {
	sum := sha256.Sum256(message)
	return sum[:]
}

func (f *Fact) validate() error {
	if err := f.validateFields(); err != nil {
		return err
	}
	if len(f.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("evidence: fact %q has signature length %d, want %d",
			f.Name, len(f.Signature), ed25519.SignatureSize)
	}
	return nil
}

func (f *Fact) validateFields() error {
	switch {
	case f == nil:
		return errors.New("evidence: nil fact")
	case f.Name == "":
		return errors.New("evidence: fact has empty Name")
	case f.NodeID == "":
		return errors.New("evidence: fact has empty NodeID")
	case f.Method == "":
		return errors.New("evidence: fact has empty Method")
	case len(f.Message) == 0:
		return errors.New("evidence: fact has empty Message")
	default:
		return nil
	}
}

func appendLengthPrefixed(dst, b []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(b)))
	dst = append(dst, length[:]...)
	return append(dst, b...)
}

func canonicalPayload(name, nodeID, method string, digest []byte, claim bool) []byte {
	out := append([]byte(nil), payloadVersion+"\n"...)
	out = appendLengthPrefixed(out, []byte(name))
	out = appendLengthPrefixed(out, []byte(nodeID))
	out = appendLengthPrefixed(out, []byte(method))
	out = appendLengthPrefixed(out, digest)
	if claim {
		out = append(out, 1)
	} else {
		out = append(out, 0)
	}
	return out
}

func (f *Fact) signedPayload() []byte {
	return canonicalPayload(f.Name, f.NodeID, f.Method, MessageDigest(f.Message), f.Claim)
}

// NewFact produces a signed fact. The signature covers the fact name, node id,
// authentication method, the digest of message, and the claim, so altering any
// of them invalidates the signature.
func NewFact(name, nodeID, method string, message []byte, claim bool, key ed25519.PrivateKey) (*Fact, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("evidence: invalid ed25519 private key")
	}
	fact := &Fact{
		Name:    name,
		NodeID:  nodeID,
		Method:  method,
		Message: append([]byte(nil), message...),
		Claim:   claim,
	}
	if err := fact.validateFields(); err != nil {
		return nil, err
	}
	fact.Signature = ed25519.Sign(key, fact.signedPayload())
	return fact, nil
}

// TrustedNodes maps access node ids to their ed25519 public verification keys.
// Only nodes present in this map can produce True or False facts.
type TrustedNodes map[string]ed25519.PublicKey

// Engine evaluates release rules against authenticated evidence.
type Engine struct {
	// Trusted is the set of access nodes whose signed facts may take a
	// defined (True or False) value.
	Trusted TrustedNodes
	// MaxFacts caps the number of authenticated facts; zero means
	// DefaultMaxFacts.
	MaxFacts int
}

func (e *Engine) maxFacts() int {
	if e.MaxFacts > 0 {
		return e.MaxFacts
	}
	return DefaultMaxFacts
}

// authenticate reports whether f is a usable authenticated fact for the
// message whose digest is currentDigest.
func (e *Engine) authenticate(f *Fact, currentDigest []byte) bool {
	if err := f.validate(); err != nil {
		return false
	}
	publicKey, ok := e.Trusted[f.NodeID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	if !bytes.Equal(MessageDigest(f.Message), currentDigest) {
		return false
	}
	return ed25519.Verify(publicKey, f.signedPayload(), f.Signature)
}

// Decision is the result of evaluating a release rule.
type Decision struct {
	// Value is the rule's strong Kleene value over every presented fact.
	Value Value
	// Release is true exactly when Value is True.
	Release bool
	// Values holds the resolved value of every presented fact name and every
	// rule-referenced name. External declarations can only appear as Unknown.
	Values map[string]Value
	// Authenticated lists the facts that qualified as trusted, correctly
	// signed evidence for the evaluated message, sorted by name.
	Authenticated []*Fact
	// MinimalSets lists every minimum-cardinality subset of Authenticated
	// whose rule evaluation is True. Sets and facts within a set are ordered
	// by name. It is empty when Release is false.
	MinimalSets [][]*Fact
}

// Evaluate runs rule over the evidence presented for message.
//
// Facts signed by trusted nodes over message bind True or False. Every other
// fact, including same-name external declarations and facts signed over a
// different message, binds Unknown and cannot influence the result. Two
// authenticated facts sharing a name are rejected because the rule binding
// would be ambiguous.
func (e *Engine) Evaluate(rule Expr, message []byte, facts []*Fact) (*Decision, error) {
	if rule == nil {
		return nil, errors.New("evidence: nil rule")
	}
	if len(message) == 0 {
		return nil, errors.New("evidence: empty message")
	}

	currentDigest := MessageDigest(message)
	bound := make(map[string]*Fact)
	values := make(map[string]Value)

	for _, fact := range facts {
		if fact == nil {
			continue
		}
		if e.authenticate(fact, currentDigest) {
			if other, duplicated := bound[fact.Name]; duplicated {
				return nil, fmt.Errorf(
					"evidence: duplicate authenticated fact %q from nodes %q and %q",
					fact.Name, other.NodeID, fact.NodeID)
			}
			bound[fact.Name] = fact
			if fact.Claim {
				values[fact.Name] = True
			} else {
				values[fact.Name] = False
			}
			continue
		}
		if _, defined := values[fact.Name]; !defined {
			values[fact.Name] = Unknown
		}
	}

	if len(bound) > e.maxFacts() {
		return nil, fmt.Errorf("evidence: %d authenticated facts exceed MaxFacts %d",
			len(bound), e.maxFacts())
	}

	for _, name := range Names(rule) {
		if _, defined := values[name]; !defined {
			values[name] = Unknown
		}
	}

	lookup := func(name string) Value {
		return values[name]
	}
	ruleValue := rule.Eval(lookup)

	authenticated := make([]*Fact, 0, len(bound))
	for _, fact := range bound {
		authenticated = append(authenticated, fact)
	}
	sort.Slice(authenticated, func(i, j int) bool {
		return authenticated[i].Name < authenticated[j].Name
	})

	decision := &Decision{
		Value:         ruleValue,
		Release:       ruleValue == True,
		Values:        values,
		Authenticated: authenticated,
	}
	if ruleValue == True {
		decision.MinimalSets = minimalReleasingSets(rule, authenticated)
	}
	return decision, nil
}

// minimalReleasingSets enumerates subsets in increasing cardinality and returns
// every subset of the smallest cardinality that evaluates to True. With at
// most DefaultMaxFacts authenticated facts the exhaustive enumeration is
// bounded by 2^18 rule evaluations.
func minimalReleasingSets(rule Expr, authenticated []*Fact) [][]*Fact {
	n := len(authenticated)
	indexByName := make(map[string]int, n)
	for i, fact := range authenticated {
		indexByName[fact.Name] = i
	}
	// Slot i holds the Kleene value of authenticated[i] when that fact is in
	// the candidate subset; slots outside the subset stay Unknown. Lookup by
	// name avoids rebuilding a map for every enumerated subset.
	slots := make([]Value, n)
	lookup := func(name string) Value {
		if index, ok := indexByName[name]; ok {
			return slots[index]
		}
		return Unknown
	}
	for size := 1; size <= n; size++ {
		var hits [][]*Fact
		eachCombination(n, size, func(indices []int) {
			for _, index := range indices {
				fact := authenticated[index]
				if fact.Claim {
					slots[index] = True
				} else {
					slots[index] = False
				}
			}
			holds := rule.Eval(lookup) == True
			for _, index := range indices {
				slots[index] = Unknown
			}
			if !holds {
				return
			}
			set := make([]*Fact, size)
			for j, index := range indices {
				set[j] = authenticated[index]
			}
			hits = append(hits, set)
		})
		if len(hits) > 0 {
			return hits
		}
	}
	return nil
}

// eachCombination visits every size-element combination of [0,n) in
// lexicographic order.
func eachCombination(n, size int, visit func([]int)) {
	if size <= 0 || size > n {
		return
	}
	indices := make([]int, size)
	for i := range indices {
		indices[i] = i
	}
	for {
		visit(indices)
		i := size - 1
		for i >= 0 && indices[i] == n-size+i {
			i--
		}
		if i < 0 {
			return
		}
		indices[i]++
		for j := i + 1; j < size; j++ {
			indices[j] = indices[j-1] + 1
		}
	}
}
