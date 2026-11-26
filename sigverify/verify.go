package sigverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"time"
)

// TrustedMaterial contains the locally provisioned trust anchors. Nothing here
// is ever fetched over the network.
type TrustedMaterial struct {
	// FulcioRoots are the trusted Fulcio root certificates (DER encoded).
	FulcioRoots [][]byte

	// RekorPublicKey is the trusted Ed25519 key used to verify the signed
	// checkpoint and any optional SET.
	RekorPublicKey ed25519.PublicKey

	// ExpectedCheckpointOrigin, when non-empty, must equal the origin line of
	// every presented checkpoint.
	ExpectedCheckpointOrigin string
}

// Policy expresses the caller's identity allowlist. Issuers and subjects are
// matched exactly.
type Policy struct {
	// AllowedOIDCIssuers lists acceptable OIDC issuer URLs.
	AllowedOIDCIssuers []string

	// AllowedSubjects lists acceptable leaf SAN identities (Email or URI).
	AllowedSubjects []string
}

// Result reports the verified identity of a successfully checked bundle.
type Result struct {
	OIDCIssuer      string
	Subjects        []string
	IntegratedTime  time.Time
	LeafCertificate *x509.Certificate
}

// signedEntryTimestamp is the canonical payload covered by the optional Rekor
// SET signature.
type signedEntryTimestamp struct {
	LogIndex       uint64 `json:"logIndex"`
	IntegratedTime int64  `json:"integratedTime"`
	LeafHash       b64    `json:"leafHash"`
}

// Verify runs the complete offline verification pipeline:

//  1. the presented artifact must hash to the bundle digest;
//  2. the log entry must re-serialize canonically and bind the same digest,
//     signature and leaf certificate;
//  3. the signed checkpoint Ed25519 signature and origin must verify before
//     any root or timestamp is trusted;
//  4. the RFC 6962 inclusion proof must rebuild the Merkle root carried in the
//     signed checkpoint (rejecting bad indices and path lengths);
//  5. the optional SET must verify under the same log key;
//  6. the leaf must carry CodeSigning EKU and its attested-time validity
//     window, then chain to a trusted Fulcio root;
//  7. the OIDC issuer / SAN identity must match the caller policy;
//  8. the ECDSA artifact signature must verify under the leaf key.
func Verify(b *Bundle, artifact []byte, trust TrustedMaterial, policy Policy) (*Result, error) {
	if b == nil {
		return nil, ErrMalformedBundle
	}

	// (1) Artifact digest against the bundle claim.
	artifactDigest := sha256.Sum256(artifact)
	if !bytes.Equal(artifactDigest[:], b.ArtifactDigest.Value) {
		return nil, ErrArtifactDigestMismatch
	}

	// (2) Re-derive the canonical entry bytes and its RFC 6962 leaf hash.
	canonicalEntry, err := b.LogEntry.CanonicalBytes()
	if err != nil {
		return nil, ErrMalformedBundle
	}
	leafHash := LeafHash(canonicalEntry)

	// The immutable log entry must bind exactly the presented artifact digest
	// and ECDSA signature, and the certificate it records must be the leaf
	// under verification (binding by certificate hash prevents a swap).
	if !bytes.Equal(b.LogEntry.ArtifactDigest.Value, b.ArtifactDigest.Value) {
		return nil, ErrLogEntryBindingMismatch
	}
	if !bytes.Equal(b.LogEntry.Signature, b.ArtifactSignature) {
		return nil, ErrLogEntryBindingMismatch
	}
	if !bytes.Equal(b.LogEntry.CertificateDER, b.LeafCertificateDER) {
		return nil, ErrLogEntryBindingMismatch
	}

	// (3) Parse and verify the signed checkpoint before trusting any root or
	// timestamp derived from it.
	checkpoint, err := ParseCheckpoint(b.Checkpoint)
	if err != nil {
		return nil, err
	}
	if err := checkpoint.verify(trust.RekorPublicKey, trust.ExpectedCheckpointOrigin); err != nil {
		return nil, err
	}

	// (4) RFC 6962 inclusion proof against the signed checkpoint root.
	hashes, err := b.InclusionProof.proofHashes()
	if err != nil {
		return nil, err
	}
	if err := VerifyInclusion(
		b.InclusionProof.LogIndex,
		b.InclusionProof.TreeSize,
		leafHash,
		checkpoint.RootHash,
		hashes,
	); err != nil {
		return nil, err
	}

	// The checkpoint size must be the tree size used by the proof.
	if checkpoint.TreeSize != b.InclusionProof.TreeSize {
		return nil, ErrCheckpointBinding
	}

	// (5) Optional signed entry timestamp over (logIndex, integratedTime,
	// leafHash).
	if len(b.SET) != 0 {
		payload := signedEntryTimestamp{
			LogIndex:       b.InclusionProof.LogIndex,
			IntegratedTime: b.IntegratedTime,
			LeafHash:       leafHash,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, ErrMalformedBundle
		}
		if !ed25519.Verify(trust.RekorPublicKey, encoded, b.SET) {
			return nil, ErrSETSignature
		}
	}

	// (6) Parse the leaf certificate and verify the chain to a trusted root.
	leaf, err := x509.ParseCertificate(b.LeafCertificateDER)
	if err != nil {
		return nil, ErrMalformedBundle
	}
	if !hasCodeSigningEKU(leaf) {
		return nil, ErrEKU
	}
	attestedTime := time.Unix(b.IntegratedTime, 0).UTC()

	// (7) Strict validity window against the attested log time (inclusive).
	// This is checked before chain construction so an out-of-window attested
	// time is reported directly rather than as an indirect chain failure.
	if attestedTime.Before(leaf.NotBefore) || attestedTime.After(leaf.NotAfter) {
		return nil, ErrTimeWindow
	}

	rootPool, err := trustedRootPool(trust.FulcioRoots)
	if err != nil {
		return nil, err
	}
	intermediates := make([][]byte, len(b.IntermediateCertificateDER))
	for i, der := range b.IntermediateCertificateDER {
		intermediates[i] = der
	}
	if err := verifyChain(leaf, intermediates, rootPool, attestedTime); err != nil {
		return nil, err
	}

	// Identity policy: OIDC issuer and SAN subjects.
	issuer, err := ExtractOIDCIssuer(leaf)
	if err != nil {
		return nil, err
	}
	if !exactMatch(issuer, policy.AllowedOIDCIssuers) {
		return nil, ErrIssuer
	}
	subjects, err := ExtractSANIdentities(leaf)
	if err != nil {
		return nil, err
	}
	if !anyExactMatch(subjects, policy.AllowedSubjects) {
		return nil, ErrSubject
	}

	// (8) Artifact ECDSA signature under the leaf key.
	if err := verifyArtifactSignature(leaf, artifact, b.ArtifactSignature); err != nil {
		return nil, err
	}

	return &Result{
		OIDCIssuer:      issuer,
		Subjects:        subjects,
		IntegratedTime:  attestedTime,
		LeafCertificate: leaf,
	}, nil
}

func trustedRootPool(roots [][]byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	added := false
	for _, der := range roots {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, ErrCertificateChain
		}
		pool.AddCert(cert)
		added = true
	}
	if !added {
		return nil, ErrCertificateChain
	}
	return pool, nil
}

func exactMatch(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func anyExactMatch(values, allowed []string) bool {
	for _, value := range values {
		if exactMatch(value, allowed) {
			return true
		}
	}
	return false
}
