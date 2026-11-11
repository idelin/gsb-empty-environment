package netsec

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// PublicKey is the minimal view of a host or CA public key. Key material
// comparison (for both allow entries and revocations) is done on the
// canonical marshaled form.
type PublicKey interface {
	Type() string
	Marshal() []byte
}

// KeyMaterialEqual reports whether two keys carry identical key material.
func KeyMaterialEqual(a, b PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Type() == b.Type() && bytes.Equal(a.Marshal(), b.Marshal())
}

// HostKeyEntry pins a host key to a set of host patterns (known-hosts
// style). The same key material may appear in several entries (aliases).
type HostKeyEntry struct {
	Patterns []Pattern
	Key      PublicKey
}

// CAEntry trusts a certificate authority for the hosts covered by Patterns
// (the @cert-authority scope).
type CAEntry struct {
	Patterns []Pattern
	CA       PublicKey
}

// Revocation revokes a key by its public key material. Revocations take
// priority over every allow entry and every CA trust entry.
type Revocation struct {
	Key PublicKey
}

// Certificate is an already signature-verified host certificate. The
// decider still enforces hostname principals, the validity window and the
// CA scope.
type Certificate struct {
	Key         PublicKey // certified host key
	CA          PublicKey // signing CA key
	Principals  []string  // valid hostnames
	ValidAfter  time.Time
	ValidBefore time.Time
}

// Input describes one connection attempt.
type Input struct {
	Host string
	Port int
	Now  time.Time
	Key  PublicKey    // presented raw host key
	Cert *Certificate // if non-nil, the certificate path is evaluated
}

// EvidenceKind classifies one entry in the decision evidence trail.
type EvidenceKind string

const (
	EvRevocationMatch   EvidenceKind = "revocation_match"
	EvAllowMatch        EvidenceKind = "allow_match"
	EvAllowKeyMismatch  EvidenceKind = "allow_key_mismatch"
	EvAllowHostMismatch EvidenceKind = "allow_host_mismatch"
	EvCertHostnameMatch EvidenceKind = "cert_hostname_match"
	EvCertHostnameBad   EvidenceKind = "cert_hostname_mismatch"
	EvCertValidityOK    EvidenceKind = "cert_validity_ok"
	EvCertValidityBad   EvidenceKind = "cert_validity_expired"
	EvCertCAMatch       EvidenceKind = "cert_ca_match"
	EvCertCABad         EvidenceKind = "cert_ca_untrusted"
)

// Evidence is one recorded step of the evaluation.
type Evidence struct {
	Kind    EvidenceKind
	Pattern string // textual pattern or entry description involved
	Detail  string
}

// Decision is the outcome of evaluating one Input.
type Decision struct {
	Allowed  bool
	Reason   string
	Evidence []Evidence
}

// HasEvidence reports whether the evidence trail contains the given kind.
func (d Decision) HasEvidence(kind EvidenceKind) bool {
	for _, e := range d.Evidence {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// Rejection reason strings.
const (
	ReasonRevoked      = "host key revoked"
	ReasonAllowed      = "matched known-hosts entry"
	ReasonCertAllowed  = "certificate accepted"
	ReasonNoMatch      = "no matching known-hosts entry"
	ReasonCertHostname = "certificate not valid for hostname"
	ReasonCertValidity = "certificate outside validity window"
	ReasonCertCA       = "certificate CA not trusted for host"
)

// Decider holds the operator-maintained trust stores. Evaluation never
// short-circuits: every revocation entry is checked even after an allow
// entry matched, so a revoked key can never slip through on an allow hit.
type Decider struct {
	HostKeys    []HostKeyEntry
	CAs         []CAEntry
	Revocations []Revocation
	HMAC        HMACFunc // required when hashed hostname patterns are used
}

// Decide evaluates in against all stores and returns the full evidence
// trail together with the verdict.
func (d *Decider) Decide(in Input) Decision {
	var ev []Evidence

	presented := in.Key
	if in.Cert != nil {
		presented = in.Cert.Key
	}

	// Revocations are evaluated for every entry, unconditionally.
	revoked := false
	for i := range d.Revocations {
		if KeyMaterialEqual(d.Revocations[i].Key, presented) {
			revoked = true
			ev = append(ev, Evidence{
				Kind:   EvRevocationMatch,
				Detail: fmt.Sprintf("presented key material matches revocation entry %d", i),
			})
		}
	}

	var dec Decision
	if in.Cert != nil {
		dec = d.decideCert(in, ev)
	} else {
		dec = d.decideRaw(in, ev)
	}
	if revoked {
		dec.Allowed = false
		dec.Reason = ReasonRevoked
	}
	return dec
}

func (d *Decider) decideRaw(in Input, ev []Evidence) Decision {
	allowed := false
	for i := range d.HostKeys {
		entry := &d.HostKeys[i]
		keyOK := KeyMaterialEqual(entry.Key, in.Key)
		hostOK := MatchAny(entry.Patterns, in.Host, in.Port, d.HMAC)
		switch {
		case keyOK && hostOK:
			allowed = true
			ev = append(ev, Evidence{
				Kind:    EvAllowMatch,
				Pattern: joinPatterns(entry.Patterns),
				Detail:  fmt.Sprintf("host key entry %d matched host and key", i),
			})
		case keyOK:
			ev = append(ev, Evidence{
				Kind:    EvAllowHostMismatch,
				Pattern: joinPatterns(entry.Patterns),
				Detail:  fmt.Sprintf("host key entry %d key matched but no pattern matched %s", i, hostPort(in.Host, in.Port)),
			})
		case hostOK:
			ev = append(ev, Evidence{
				Kind:    EvAllowKeyMismatch,
				Pattern: joinPatterns(entry.Patterns),
				Detail:  fmt.Sprintf("host key entry %d pattern matched but key material differs", i),
			})
		}
	}
	if allowed {
		return Decision{Allowed: true, Reason: ReasonAllowed, Evidence: ev}
	}
	return Decision{Allowed: false, Reason: ReasonNoMatch, Evidence: ev}
}

func (d *Decider) decideCert(in Input, ev []Evidence) Decision {
	cert := in.Cert
	ok := true
	var reasons []string

	// Hostname principal.
	hostOK := false
	for _, p := range cert.Principals {
		if strings.EqualFold(p, in.Host) {
			hostOK = true
			break
		}
	}
	if hostOK {
		ev = append(ev, Evidence{Kind: EvCertHostnameMatch, Detail: fmt.Sprintf("hostname %q listed in certificate principals", in.Host)})
	} else {
		ok = false
		reasons = append(reasons, ReasonCertHostname)
		ev = append(ev, Evidence{Kind: EvCertHostnameBad, Detail: fmt.Sprintf("hostname %q not in certificate principals %v", in.Host, cert.Principals)})
	}

	// Validity window.
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(cert.ValidAfter) || !now.Before(cert.ValidBefore) {
		ok = false
		reasons = append(reasons, ReasonCertValidity)
		ev = append(ev, Evidence{Kind: EvCertValidityBad, Detail: fmt.Sprintf("now=%s outside [%s, %s)", now, cert.ValidAfter, cert.ValidBefore)})
	} else {
		ev = append(ev, Evidence{Kind: EvCertValidityOK, Detail: "certificate within validity window"})
	}

	// CA scope: the signing CA must be trusted for this host/port.
	caOK := false
	for i := range d.CAs {
		entry := &d.CAs[i]
		if !KeyMaterialEqual(entry.CA, cert.CA) {
			continue
		}
		if MatchAny(entry.Patterns, in.Host, in.Port, d.HMAC) {
			caOK = true
			ev = append(ev, Evidence{
				Kind:    EvCertCAMatch,
				Pattern: joinPatterns(entry.Patterns),
				Detail:  fmt.Sprintf("CA entry %d trusts the signing CA for %s", i, hostPort(in.Host, in.Port)),
			})
		}
	}
	if !caOK {
		ok = false
		reasons = append(reasons, ReasonCertCA)
		ev = append(ev, Evidence{Kind: EvCertCABad, Detail: "no CA entry trusts the signing CA for this host"})
	}

	if ok {
		return Decision{Allowed: true, Reason: ReasonCertAllowed, Evidence: ev}
	}
	return Decision{Allowed: false, Reason: strings.Join(reasons, "; "), Evidence: ev}
}

func joinPatterns(patterns []Pattern) string {
	parts := make([]string, len(patterns))
	for i, p := range patterns {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

func hostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}
