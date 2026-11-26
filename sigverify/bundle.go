package sigverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

// BundleVersion is the schema version understood by this package.
const BundleVersion = "sigstoreoff-bundle/v1"

// b64 is a byte slice that is marshaled as standard base64 in JSON.
type b64 []byte

func (b b64) MarshalJSON() ([]byte, error) {
	return json.Marshal(base64.StdEncoding.EncodeToString([]byte(b)))
}

func (b *b64) UnmarshalJSON(data []byte) error {
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	*b = raw
	return nil
}

// Digest binds a hash algorithm to its value. Only SHA-256 is accepted.
type Digest struct {
	Algorithm string `json:"algorithm"`
	Value     b64    `json:"value"`
}

// LogEntry is the Rekor log entry payload. Its deterministic JSON
// serialization (see CanonicalBytes) is what the transparent log commits to.
type LogEntry struct {
	ArtifactDigest Digest `json:"artifactDigest"`
	Signature      b64    `json:"signature"`
	PublicKeyHint  string `json:"publicKeyHint"`
	CertificateDER b64    `json:"certificateDER"`
	SignatureAlgo  string `json:"signatureAlgorithm"`
	LogEntryType   string `json:"logEntryType"`
}

// CanonicalBytes returns the deterministic byte representation of the entry.
// Go's encoding/json emits struct fields in declaration order and escapes HTML
// deterministically, so the output is stable across processes; the verifier
// re-derives these bytes rather than trusting an attacker-supplied blob.
func (e LogEntry) CanonicalBytes() ([]byte, error) {
	return json.Marshal(e)
}

// InclusionProof carries the RFC 6962 Merkle inclusion path.
type InclusionProof struct {
	LogIndex uint64 `json:"logIndex"`
	TreeSize uint64 `json:"treeSize"`
	// Hashes are ordered from the leaf toward the root as in RFC 6962.
	Hashes []b64 `json:"hashes"`
}

// Bundle is the offline, self-contained Sigstore verification artifact.
type Bundle struct {
	Version string `json:"version"`

	// ArtifactDigest is the digest the caller expects the artifact to have.
	ArtifactDigest Digest `json:"artifactDigest"`

	// ArtifactSignature is the ECDSA signature over the raw artifact bytes.
	ArtifactSignature b64 `json:"artifactSignature"`

	// LeafCertificateDER is the short-lived Fulcio leaf certificate.
	LeafCertificateDER b64 `json:"leafCertificateDER"`

	// IntermediateCertificateDER is the Fulcio intermediate chain between the
	// leaf and the trusted root.
	IntermediateCertificateDER []b64 `json:"intermediateCertificateDER"`

	// LogEntry is the Rekor entry that binds digest, signature and leaf
	// certificate.
	LogEntry LogEntry `json:"logEntry"`

	// IntegratedTime is the unix timestamp at which the log integrated the
	// entry; it is trusted only after the inclusion proof and checkpoint
	// validate.
	IntegratedTime int64 `json:"integratedTime"`

	// InclusionProof proves that LogEntry is committed by the signed tree.
	InclusionProof InclusionProof `json:"inclusionProof"`

	// Checkpoint is the signed checkpoint note (origin, size, base64 root,
	// Ed25519 signature over the signed note body).
	Checkpoint b64 `json:"checkpoint"`

	// SET is the optional signed entry timestamp (Rekor SET). When present it
	// must verify under the trusted log key.
	SET b64 `json:"set,omitempty"`
}

// ParseBundle decodes a JSON bundle and performs structural validation. It
// deliberately does not perform any cryptographic verification; call Verify
// for the full offline check.
func ParseBundle(raw []byte) (*Bundle, error) {
	var b Bundle
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&b); err != nil {
		return nil, ErrMalformedBundle
	}
	if dec.More() {
		return nil, ErrMalformedBundle
	}
	if err := b.validateShape(); err != nil {
		return nil, err
	}
	return &b, nil
}

func (b *Bundle) validateShape() error {
	if b.Version != BundleVersion {
		return ErrMalformedBundle
	}
	if b.ArtifactDigest.Algorithm != "SHA-256" || len(b.ArtifactDigest.Value) != sha256.Size {
		return ErrMalformedBundle
	}
	if len(b.ArtifactSignature) == 0 || len(b.LeafCertificateDER) == 0 {
		return ErrMalformedBundle
	}
	if b.LogEntry.ArtifactDigest.Algorithm != "SHA-256" ||
		len(b.LogEntry.ArtifactDigest.Value) != sha256.Size {
		return ErrMalformedBundle
	}
	if len(b.LogEntry.Signature) == 0 || len(b.LogEntry.CertificateDER) == 0 {
		return ErrMalformedBundle
	}
	if b.InclusionProof.TreeSize == 0 || b.InclusionProof.LogIndex >= b.InclusionProof.TreeSize {
		return ErrProofIndex
	}
	if len(b.InclusionProof.Hashes) == 0 && b.InclusionProof.TreeSize > 1 {
		return ErrProofLength
	}
	if len(b.Checkpoint) == 0 || b.IntegratedTime <= 0 {
		return ErrMalformedBundle
	}
	return nil
}

// proofHashes converts the JSON base64 path into plain byte hashes.
func (p InclusionProof) proofHashes() ([][]byte, error) {
	out := make([][]byte, len(p.Hashes))
	for i, h := range p.Hashes {
		if len(h) != sha256.Size {
			return nil, ErrProofHash
		}
		out[i] = []byte(h)
	}
	return out, nil
}
