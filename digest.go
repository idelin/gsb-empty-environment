package credentialhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const decisionDigestSchema = "credentialhost/authorization-decision/v1"

type canonicalDecision struct {
	Schema           string `json:"schema"`
	Principal        string `json:"principal"`
	TenantID         string `json:"tenantID"`
	RoleID           string `json:"roleID"`
	ExternalID       string `json:"externalID"`
	SourceResource   string `json:"sourceResource"`
	DurationSeconds  int64  `json:"durationSeconds"`
	ParametersDigest string `json:"parametersDigest"`
}

// CanonicalDecision returns the canonical bytes covered by a decision digest.
// The representation is a fixed-field JSON object, so it is stable across map
// iteration order and process restarts. SessionName and RequestID are
// intentionally excluded.
func CanonicalDecision(decision Decision) []byte {
	canonical, _ := json.Marshal(canonicalDecision{
		Schema:           decisionDigestSchema,
		Principal:        decision.Principal,
		TenantID:         decision.TenantID,
		RoleID:           decision.RoleID,
		ExternalID:       decision.ExternalID,
		SourceResource:   decision.SourceResource,
		DurationSeconds:  decision.DurationSeconds,
		ParametersDigest: decision.ParametersDigest,
	})
	return canonical
}

// ComputeDecisionDigest returns the lowercase hex SHA-256 of CanonicalDecision.
func ComputeDecisionDigest(decision Decision) string {
	sum := sha256.Sum256(CanonicalDecision(decision))
	return hex.EncodeToString(sum[:])
}

type canonicalRequestFingerprint struct {
	Schema           string   `json:"schema"`
	Principal        string   `json:"principal"`
	TenantIDs        []string `json:"tenantIDs"`
	TenantID         string   `json:"tenantID"`
	RoleID           string   `json:"roleID"`
	ExternalID       string   `json:"externalID"`
	SourceResource   string   `json:"sourceResource"`
	SessionName      string   `json:"sessionName"`
	DurationSeconds  int64    `json:"durationSeconds"`
	ParametersDigest string   `json:"parametersDigest"`
}

const requestFingerprintSchema = "credentialhost/request-fingerprint/v1"

func computeRequestFingerprint(request AssumeRequest, principal Principal) string {
	tenants := append([]string(nil), principal.TenantIDs...)
	sortStrings(tenants)
	tenants = uniqueStrings(tenants)
	canonical, _ := json.Marshal(canonicalRequestFingerprint{
		Schema:           requestFingerprintSchema,
		Principal:        principal.Subject,
		TenantIDs:        tenants,
		TenantID:         request.TenantID,
		RoleID:           request.RoleID,
		ExternalID:       request.ExternalID,
		SourceResource:   request.SourceResource,
		SessionName:      request.SessionName,
		DurationSeconds:  request.DurationSeconds,
		ParametersDigest: request.ParametersDigest,
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func expectedSessionID(requestID, decisionDigest string, tenantVersion, resourceVersion uint64) string {
	material := "credentialhost/session/v1\x00" + requestID + "\x00" + decisionDigest +
		"\x00" + u64String(tenantVersion) + "\x00" + u64String(resourceVersion)
	sum := sha256.Sum256([]byte(material))
	return "gsh_" + hex.EncodeToString(sum[:16])
}
