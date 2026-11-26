// Package sigverify implements fully offline verification of a Sigstore-style
// signing bundle using only the Go standard library.
//
// Verification is intentionally self-contained: no network access is performed
// and no external Sigstore SDK or other third-party module is required. The
// caller supplies all trust anchors (Fulcio root CA, Rekor log key and policy)
// up front, so every check runs against locally provisioned material.
package sigverify

import "errors"

// Sentinel errors returned by the verifier. Callers can use errors.Is to map a
// rejection to a concrete failure class.
var (
	// ErrMalformedBundle is returned when the bundle cannot be parsed or is
	// missing mandatory fields.
	ErrMalformedBundle = errors.New("sigverify: malformed bundle")

	// ErrArtifactDigestMismatch is returned when the digest of the artifact
	// presented for verification does not match the digest recorded in the
	// bundle.
	ErrArtifactDigestMismatch = errors.New("sigverify: artifact digest mismatch")

	// ErrLogEntryBindingMismatch is returned when the log entry does not bind
	// exactly the same artifact digest/signature as the bundle.
	ErrLogEntryBindingMismatch = errors.New("sigverify: log entry does not bind the bundle artifact")

	// ErrProofIndex is returned for an illegal index/tree-size relationship
	// (for example logIndex >= treeSize).
	ErrProofIndex = errors.New("sigverify: invalid inclusion proof index")

	// ErrProofLength is returned when the number of supplied hashes does not
	// match the exact RFC 6962 path length, including extra or missing nodes.
	ErrProofLength = errors.New("sigverify: inclusion proof path length mismatch")

	// ErrProofHash is returned when a supplied hash is not a 32-byte SHA-256
	// digest.
	ErrProofHash = errors.New("sigverify: invalid inclusion proof hash length")

	// ErrRootMismatch is returned when the Merkle root rebuilt from the
	// inclusion proof does not equal the signed checkpoint root.
	ErrRootMismatch = errors.New("sigverify: recomputed Merkle root does not match checkpoint")

	// ErrCheckpoint is returned for a malformed signed checkpoint.
	ErrCheckpoint = errors.New("sigverify: malformed signed checkpoint")

	// ErrCheckpointSignature is returned when the checkpoint Ed25519 signature
	// does not validate under the trusted Rekor key.
	ErrCheckpointSignature = errors.New("sigverify: checkpoint signature verification failed")

	// ErrCheckpointOrigin is returned when the checkpoint origin line is not
	// the origin configured in the trusted material.
	ErrCheckpointOrigin = errors.New("sigverify: checkpoint origin mismatch")

	// ErrCheckpointBinding is returned when the size/root inside the
	// checkpoint is inconsistent with the inclusion proof inputs.
	ErrCheckpointBinding = errors.New("sigverify: checkpoint does not match proof parameters")

	// ErrSETSignature is returned when an optional signed entry timestamp does
	// not validate under the trusted Rekor key.
	ErrSETSignature = errors.New("sigverify: signed entry timestamp verification failed")

	// ErrCertificateChain is returned when the leaf certificate cannot be
	// built into a chain to a trusted Fulcio root.
	ErrCertificateChain = errors.New("sigverify: leaf certificate does not chain to trusted Fulcio root")

	// ErrEKU is returned when the leaf certificate lacks the CodeSigning
	// extended key usage.
	ErrEKU = errors.New("sigverify: leaf certificate is missing CodeSigning EKU")

	// ErrIssuer is returned when no OIDC issuer extension is present or its
	// value does not exactly match the policy allowlist.
	ErrIssuer = errors.New("sigverify: OIDC issuer not allowed by policy")

	// ErrSubject is returned when none of the leaf SAN identities (Email/URI)
	// matches the policy allowlist.
	ErrSubject = errors.New("sigverify: certificate SAN identity not allowed by policy")

	// ErrTimeWindow is returned when the attested integratedTime does not
	// fall within the leaf certificate validity window.
	ErrTimeWindow = errors.New("sigverify: integratedTime outside certificate validity window")

	// ErrArtifactSignature is returned when the ECDSA artifact signature does
	// not verify under the leaf certificate public key.
	ErrArtifactSignature = errors.New("sigverify: artifact ECDSA signature verification failed")
)
