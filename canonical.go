package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
)

const (
	decisionDomain    = "netsec.delegation.decision.v1"
	fingerprintDomain = "netsec.assume.request.v1"
)

func appendStringField(dst []byte, name, value string) []byte {
	dst = appendUint64(dst, uint64(len(name)))
	dst = append(dst, name...)
	dst = appendUint64(dst, uint64(len(value)))
	dst = append(dst, value...)
	return dst
}

func appendUint64(dst []byte, value uint64) []byte {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	return append(dst, raw[:]...)
}

func appendInt64Field(dst []byte, name string, value int64) []byte {
	dst = appendUint64(dst, uint64(len(name)))
	dst = append(dst, name...)
	// The value length is fixed at eight bytes.
	dst = appendUint64(dst, 8)
	return appendUint64(dst, uint64(value))
}

func appendUint64Field(dst []byte, name string, value uint64) []byte {
	dst = appendUint64(dst, uint64(len(name)))
	dst = append(dst, name...)
	dst = appendUint64(dst, 8)
	return appendUint64(dst, value)
}

func appendBoolField(dst []byte, name string, value bool) []byte {
	dst = appendUint64(dst, uint64(len(name)))
	dst = append(dst, name...)
	dst = appendUint64(dst, 1)
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

// computeDecisionDigest binds all security-relevant request inputs, the
// authenticated requester, and the exact tenant configuration generation into
// one canonical decision identifier.
func computeDecisionDigest(d Decision) string {
	var buf []byte
	buf = appendStringField(buf, "domain", decisionDomain)
	buf = appendStringField(buf, "request_id", d.RequestID)
	buf = appendStringField(buf, "principal_id", d.PrincipalID)
	buf = appendStringField(buf, "tenant_id", d.TenantID)
	buf = appendStringField(buf, "role_id", d.RoleID)
	buf = appendStringField(buf, "external_id", d.ExternalID)
	buf = appendStringField(buf, "source_resource", d.SourceResource)
	buf = appendStringField(buf, "parameters_digest", d.ParametersDigest)
	buf = appendInt64Field(buf, "requested_duration_seconds", d.RequestedDurationSeconds)
	buf = appendInt64Field(buf, "effective_duration_seconds", d.EffectiveDurationSeconds)
	buf = appendStringField(buf, "policy_digest", d.PolicyDigest)
	buf = appendUint64Field(buf, "tenant_generation", d.TenantGeneration)
	buf = appendStringField(buf, "audience", d.Audience)
	buf = appendStringField(buf, "source_identity", d.SourceIdentity)
	buf = appendStringField(buf, "session_id", d.SessionID)
	buf = appendInt64Field(buf, "not_after_unix_nano", d.NotAfter.UnixNano())
	buf = appendBoolField(buf, "allowed", d.Allowed)
	buf = appendStringField(buf, "deny_code", d.DenyCode)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// RecomputeDigest returns the canonical SHA-256 decision digest for the
// receiver's bound fields. It is useful for independent audit verification.
func (d Decision) RecomputeDigest() string {
	d.Digest = ""
	return computeDecisionDigest(d)
}

// requestFingerprint is the idempotency identity of an assume command. It
// covers every request field plus the authenticated principal. Session name is
// included here because it is part of the command being replayed, even though
// it never participates in policy matching.
func requestFingerprint(principal string, req AssumeRequest) string {
	var buf []byte
	buf = appendStringField(buf, "domain", fingerprintDomain)
	buf = appendStringField(buf, "principal_id", principal)
	buf = appendStringField(buf, "request_id", req.RequestID)
	buf = appendStringField(buf, "tenant_id", req.TenantID)
	buf = appendStringField(buf, "role_id", req.RoleID)
	buf = appendStringField(buf, "external_id", req.ExternalID)
	buf = appendStringField(buf, "source_resource", req.SourceResource)
	buf = appendStringField(buf, "session_name", req.SessionName)
	buf = appendInt64Field(buf, "duration_seconds", req.DurationSeconds)
	buf = appendStringField(buf, "parameters_digest", req.ParametersDigest)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// constantTimeStringEqual hashes both inputs first so the comparison does not
// branch on matching prefixes or on unequal lengths. External IDs are bounded
// before this helper is reached.
func constantTimeStringEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
