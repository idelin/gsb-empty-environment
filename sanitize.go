package netsec

import (
	"crypto/sha256"
	"net/mail"
	"strings"
)

// Reasons a header was not elevated to trusted.
const (
	ReasonNoProof         = "no-proof"                  // external header with no attestation
	ReasonProofConnection = "proof-connection-mismatch" // proof belongs to another connection
	ReasonProofTampered   = "proof-tampered"            // proof chain integrity check failed
	ReasonHeaderMismatch  = "header-mismatch"           // header bytes differ from attestation
	ReasonNodeMismatch    = "node-mismatch"             // authserv-id differs from attested node
	ReasonMethodMismatch  = "method-mismatch"           // methods differ from attestation
	ReasonDuplicateNode   = "duplicate-node"            // node already appeared in the chain
	ReasonBrokenChain     = "broken-chain"              // an earlier hop broke the chain
	ReasonParseError      = "parse-error"               // header is not well-formed
)

// TrustLevel is the trust elevation granted to an Authentication-Results
// header. Headers only ever move from LevelUntrusted to LevelVerified by
// matching an intact, per-connection proof in node order.
type TrustLevel int

const (
	LevelUntrusted TrustLevel = iota
	LevelVerified
)

// TrustedResult is a header verified against a hop proof. Trusted results
// are reported innermost hop first, matching top-to-bottom header order.
type TrustedResult struct {
	Hop    int // index of the hop in the chain, 0 = outermost
	NodeID string
	Parsed *AuthResult
	Level  TrustLevel
}

// Evidence is an Authentication-Results header that was not trusted. The raw
// header bytes are preserved verbatim for auditing.
type Evidence struct {
	Raw    string
	Reason string
}

// Result is the outcome of sanitizing one message on one connection.
type Result struct {
	Trusted   []TrustedResult
	Untrusted []Evidence
}

// MethodResult returns the result the innermost trusted hop reported for the
// given authentication method (e.g. "spf", "dkim", "dmarc").
func (r *Result) MethodResult(method string) (string, bool) {
	method = strings.ToLower(method)
	for _, tr := range r.Trusted {
		for _, m := range tr.Parsed.Methods {
			if m.Method == method {
				return m.Result, true
			}
		}
	}
	return "", false
}

// Sanitize separates trusted Authentication-Results results from forged or
// foreign ones. Trusted nodes prepend their headers, so headers are matched
// top-down against the proof chain innermost-hop-first. Matching stops at
// the first inconsistency; that header and every header below it are kept
// verbatim as untrusted evidence. connectionID must identify the current
// receiving connection; proofs recorded for any other connection never
// elevate trust.
func Sanitize(header mail.Header, chain *Chain, connectionID string) *Result {
	res := &Result{}
	raws := authResHeaders(header)
	if chain == nil || connectionID == "" {
		for _, raw := range raws {
			res.Untrusted = append(res.Untrusted, Evidence{Raw: raw, Reason: ReasonNoProof})
		}
		return res
	}
	proofs := chain.Proofs()
	valid, failReason := validateProofs(proofs, connectionID)
	seen := make(map[string]bool)
	hi := 0
	breakReason := ""
	for pi := len(proofs) - 1; pi >= 0 && hi < len(raws); pi-- {
		if !valid[pi] {
			breakReason = failReason[pi]
			break
		}
		raw := raws[hi]
		proof := proofs[pi]
		parsed, err := ParseAuthResults(raw)
		if err != nil {
			breakReason = ReasonParseError
			break
		}
		if sha256.Sum256([]byte(raw)) != proof.HeaderHash {
			breakReason = ReasonHeaderMismatch
			break
		}
		if !strings.EqualFold(parsed.AuthservID, proof.NodeID) {
			breakReason = ReasonNodeMismatch
			break
		}
		nodeKey := strings.ToLower(parsed.AuthservID)
		if seen[nodeKey] {
			breakReason = ReasonDuplicateNode
			break
		}
		if !methodsMatch(parsed, proof.Methods) {
			breakReason = ReasonMethodMismatch
			break
		}
		seen[nodeKey] = true
		res.Trusted = append(res.Trusted, TrustedResult{
			Hop:    pi,
			NodeID: proof.NodeID,
			Parsed: parsed,
			Level:  LevelVerified,
		})
		hi++
	}
	for ; hi < len(raws); hi++ {
		reason := breakReason
		if reason == "" {
			reason = ReasonNoProof
		}
		res.Untrusted = append(res.Untrusted, Evidence{Raw: raws[hi], Reason: reason})
		if breakReason != "" {
			breakReason = ReasonBrokenChain
		}
	}
	return res
}

func authResHeaders(h mail.Header) []string {
	for k, v := range h {
		if strings.EqualFold(k, "Authentication-Results") {
			return v
		}
	}
	return nil
}

// methodsMatch reports whether the parsed methods are exactly the attested
// multiset of methods: nothing added, nothing dropped.
func methodsMatch(parsed *AuthResult, attested []string) bool {
	if len(parsed.Methods) != len(attested) {
		return false
	}
	count := make(map[string]int, len(parsed.Methods))
	for _, m := range parsed.Methods {
		count[m.Method]++
	}
	for _, a := range attested {
		count[strings.ToLower(a)]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}
