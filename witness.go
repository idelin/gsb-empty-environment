package admission

import (
	"bytes"
	"fmt"
	"strconv"
)

// SignatureVerifier verifies an opaque witness signature. The domain is the
// actual administrative domain (not an alias), message is the canonical
// WitnessMessage, and signature is the provider's signature bytes.
type SignatureVerifier func(domain string, message, signature []byte) bool

// Policy defines the witness threshold and optional signature verifier.
//
// A Threshold of zero disables the per-edge witness requirement. When a
// verifier is configured, signatures must pass it. When no verifier is
// configured, signatures are assumed to have been verified by the caller, but
// their binding fields (source tag, tree size, and root) are still checked
// here. This keeps cross-tree replay detectable without imposing a particular
// signature algorithm on the host.
type Policy struct {
	Threshold              int               `json:"threshold"`
	SignatureVerifier      SignatureVerifier `json:"-"`
	RequireTargetSignature bool              `json:"require_target_signature"`
}

// WitnessSignature is a witness's attestation for one checkpoint.
//
// Domain is the actual administrative domain and is the key used for
// de-duplication. Alias is a display or provider alias and never changes the
// domain count. SourceTag is the origin/domain tag bound into the signature
// message. Size and Root (or Checkpoint) bind the signed tree state.
type WitnessSignature struct {
	Domain     string     `json:"domain"`
	Alias      string     `json:"alias,omitempty"`
	Witness    string     `json:"witness,omitempty"`
	KeyID      string     `json:"key_id,omitempty"`
	SourceTag  string     `json:"source_tag,omitempty"`
	Size       uint64     `json:"size"`
	Root       []byte     `json:"root"`
	Signature  []byte     `json:"signature,omitempty"`
	Payload    []byte     `json:"payload,omitempty"`
	Checkpoint Checkpoint `json:"checkpoint,omitempty"`

	// ActualDomain and SourceDomain are accepted aliases used by some
	// protocol adapters. Domain and SourceTag remain canonical.
	ActualDomain string `json:"actual_domain,omitempty"`
	SourceDomain string `json:"source_domain,omitempty"`
}

// Witness and Attestation are aliases for WitnessSignature.
type Witness = WitnessSignature
type Attestation = WitnessSignature

// NewWitnessSignature creates a correctly bound signature record. A real
// deployment should replace the signature bytes with a cryptographic signature
// and configure Policy.SignatureVerifier.
func NewWitnessSignature(domain, alias string, cp Checkpoint, signature []byte) WitnessSignature {
	return WitnessSignature{
		Domain:     domain,
		Alias:      alias,
		SourceTag:  domain,
		Size:       cp.Size,
		Root:       cloneBytes(cp.Root),
		Signature:  cloneBytes(signature),
		Checkpoint: cp.normalized(),
	}
}

func (s WitnessSignature) actualDomain() string {
	if s.Domain != "" {
		return s.Domain
	}
	return s.ActualDomain
}

func (s WitnessSignature) sourceTag(domain string) string {
	if s.SourceTag != "" {
		return s.SourceTag
	}
	if s.SourceDomain != "" {
		return s.SourceDomain
	}
	return domain
}

func (s WitnessSignature) boundCheckpoint() Checkpoint {
	cp := s.Checkpoint
	if cp.Size == 0 && s.Size != 0 {
		cp.Size = s.Size
	}
	if len(cp.Root) == 0 && len(s.Root) != 0 {
		cp.Root = cloneBytes(s.Root)
	}
	return cp
}

// WitnessMessage returns the canonical bytes that a witness must sign. The
// message intentionally includes the origin domain tag, tree size, and root
// so that a signature cannot be replayed for another tree or source.
func WitnessMessage(sourceTag string, size uint64, root []byte) []byte {
	var b bytes.Buffer
	b.WriteString("go-module-admission/witness/v1\n")
	b.WriteString("source=")
	b.WriteString(sourceTag)
	b.WriteByte('\n')
	b.WriteString("size=")
	b.WriteString(strconv.FormatUint(size, 10))
	b.WriteByte('\n')
	b.WriteString("root=")
	b.WriteString(hexString(root))
	b.WriteByte('\n')
	return b.Bytes()
}

func hexString(root []byte) string {
	if len(root) == 0 {
		return ""
	}
	const digits = "0123456789abcdef"
	out := make([]byte, len(root)*2)
	for i, v := range root {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}

// BindingError checks that the signature is bound to the supplied checkpoint
// and source domain. A nil error does not imply cryptographic validity; that is
// the job of Policy.SignatureVerifier.
func (s WitnessSignature) BindingError(expectedDomain string, cp Checkpoint) error {
	if domain := s.actualDomain(); domain == "" {
		return fmt.Errorf("witness has no administrative domain")
	}
	if len(cp.Root) != HashSize {
		return fmt.Errorf("invalid checkpoint root")
	}
	bound := s.boundCheckpoint()
	if bound.Size != cp.Size {
		return fmt.Errorf("witness signature is bound to size %d, expected %d", bound.Size, cp.Size)
	}
	if !bytes.Equal(bound.Root, cp.Root) {
		return fmt.Errorf("witness signature is bound to root %x, expected %x", bound.Root, cp.Root)
	}
	tag := s.sourceTag(s.actualDomain())
	if tag == "" {
		return fmt.Errorf("witness signature has no source domain tag")
	}
	if expectedDomain != "" && tag != expectedDomain && s.actualDomain() != expectedDomain {
		// A source tag may differ from the signing domain, but when the caller
		// names an expected domain at least one of the two must identify it.
		return fmt.Errorf("witness source tag %q is not in expected domain %q", tag, expectedDomain)
	}
	if len(s.Payload) != 0 {
		expected := WitnessMessage(tag, cp.Size, cp.Root)
		if !bytes.Equal(s.Payload, expected) {
			return fmt.Errorf("witness payload does not bind source tag, size, and root")
		}
	}
	return nil
}

// WitnessMessage returns the canonical message represented by the signature.
func (s WitnessSignature) WitnessMessage() []byte {
	return WitnessMessage(s.sourceTag(s.actualDomain()), s.boundCheckpoint().Size, s.boundCheckpoint().Root)
}

// ValidWitnesses returns the number of distinct actual administrative domains
// with valid, bound attestations for cp.
func ValidWitnesses(signatures []WitnessSignature, cp Checkpoint, verifier SignatureVerifier) int {
	seen := make(map[string]struct{})
	for _, sig := range signatures {
		domain := sig.actualDomain()
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		if err := sig.BindingError("", cp); err != nil {
			continue
		}
		if verifier != nil {
			if len(sig.Signature) == 0 {
				continue
			}
			tag := sig.sourceTag(domain)
			if !verifier(domain, WitnessMessage(tag, cp.Size, cp.Root), sig.Signature) {
				continue
			}
		}
		seen[domain] = struct{}{}
	}
	return len(seen)
}

// MeetsWitnessThreshold reports whether signatures contain at least threshold
// valid attestations from distinct actual domains.
func MeetsWitnessThreshold(signatures []WitnessSignature, cp Checkpoint, threshold int, verifier SignatureVerifier) bool {
	if threshold <= 0 {
		return true
	}
	return ValidWitnesses(signatures, cp, verifier) >= threshold
}
