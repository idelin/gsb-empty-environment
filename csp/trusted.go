package csp

import (
	"html/template"
	"strings"
)

// TrustedNode is a node authorized to participate in script derivation.
// Only nodes reachable from the policy's trusted root through chains of
// trusted parents are eligible; nodes constructed outside the policy never
// receive a node id and are therefore rejected.
type TrustedNode interface {
	NodeID() string
	Policy() *Policy
}

// ScriptDeriver is implemented by trusted nodes that can create scripts.
// Deriving through a trusted parent is the only way for an inline script to
// inherit eligibility.
type ScriptDeriver interface {
	TrustedNode
	DeriveScript(body string) (*TrustedScript, error)
}

// rootNode is the single trusted entry point of a request.
type rootNode struct {
	policy *Policy
	id     string
}

func newRoot(p *Policy) *rootNode {
	return &rootNode{policy: p, id: "root"}
}

func (r *rootNode) NodeID() string  { return r.id }
func (r *rootNode) Policy() *Policy { return r.policy }

// DeriveScript approves a script for the current request and returns a node
// that is itself trusted, so scripts can form strict-dynamic chains.
func (r *rootNode) DeriveScript(body string) (*TrustedScript, error) {
	return deriveScript(r.policy, r, body)
}

// TrustedScript is an inline script whose eligibility was derived from a
// trusted parent. It renders as a nonce-bearing <script> element and can act
// as the trusted parent of further scripts.
type TrustedScript struct {
	policy *Policy
	id     string
	nonce  string
	body   string
}

func (s *TrustedScript) NodeID() string { return s.id }

// Policy returns the request policy this script belongs to.
func (s *TrustedScript) Policy() *Policy { return s.policy }

// DeriveScript creates a child script eligible because its parent was itself
// derived through a trusted chain.
func (s *TrustedScript) DeriveScript(body string) (*TrustedScript, error) {
	return deriveScript(s.policy, s, body)
}

// HTML renders the script with the request nonce. html/template treats the
// returned value as already-safe and emits it verbatim; the sanitizing writer
// independently re-checks the nonce before bytes leave the process.
func (s *TrustedScript) HTML() template.HTML {
	return template.HTML("<script nonce=\"" +
		template.HTMLEscapeString(s.nonce) +
		"\">" + s.body + "</script>")
}

func deriveScript(p *Policy, parent TrustedNode, body string) (*TrustedScript, error) {
	if strings.Contains(strings.ToLower(body), "</script") {
		return nil, ErrUnauthorized
	}

	id, err := p.registerNode(parent)
	if err != nil {
		return nil, err
	}

	script := &TrustedScript{
		policy: p,
		id:     id,
		nonce:  p.nonce,
		body:   body,
	}

	// Both the nonce-bearing tag and the body hash become eligible, matching
	// the two ways strict CSP can authorize inline execution.
	if err := p.approve(p.nonce); err != nil {
		return nil, err
	}
	if err := p.approve(hashScript(body)); err != nil {
		return nil, err
	}
	return script, nil
}

// untrustedNode models application code that builds a node outside the
// policy. It exists to make the rejection guarantee explicit in tests.
type untrustedNode struct{}

func (untrustedNode) NodeID() string  { return "foreign" }
func (untrustedNode) Policy() *Policy { return nil }
