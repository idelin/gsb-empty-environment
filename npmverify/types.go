package npmverify

import "context"

// Packument models the subset of an npm registry document (a "packument")
// that the verifier relies on. DistTags maps a tag such as "latest" to a
// concrete version string; Versions holds the per-version metadata.
type Packument struct {
	Name     string                 `json:"name"`
	DistTags map[string]string      `json:"dist-tags"`
	Versions map[string]*PkgVersion `json:"versions"`
}

// PkgVersion is the metadata published for a single name@version.
type PkgVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    Dist   `json:"dist"`
}

// Dist carries the artifact location and trust material for a version.
type Dist struct {
	// Tarball is the URL the tarball was fetched from. It is informational;
	// the verifier works on the already-downloaded bytes.
	Tarball string `json:"tarball"`

	// Integrity is the Subresource Integrity string, e.g.
	// "sha512-... sha256-...".
	Integrity string `json:"integrity"`

	// Signatures are registry signatures over name@version:integrity.
	Signatures []Signature `json:"signatures"`
}

// Signature is one entry of dist.signatures as published by the registry.
type Signature struct {
	// KeyID identifies the registry signing key, allowing the verifier
	// callback to select a trusted key.
	KeyID string `json:"keyid"`

	// Sig is the base64-encoded signature over the signature message.
	Sig string `json:"sig"`
}

// Manifest is the identity extracted from package.json inside the tarball.
type Manifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Options controls optional verifier behaviour.
type Options struct {
	// RequireSignature makes the registry signature mandatory. Defaults to
	// true, which is the safe choice for a proxy that must bind a
	// tag-resolved version back to the trusted registry's signing keys.
	RequireSignature *bool
}

// SignatureVerifier is the trust callback supplied by the embedding proxy.
// It receives the exact npm signature message (name@version:integrity) and
// each signature, and returns nil for signatures made by a trusted key. A
// non-nil error (including an unknown keyid) rejects that signature. The
// verifier accepts the version only if at least one signature is approved.
type SignatureVerifier func(ctx context.Context, message string, sig Signature) error

// Result reports the values that survived verification.
type Result struct {
	// ResolvedVersion is the concrete version the dist-tag pointed to.
	ResolvedVersion string

	// Algorithm and Digest describe the strongest declared SRI hash that
	// was matched against the tarball.
	Algorithm string
	Digest    string

	// AcceptedKeyID is the keyid whose signature callback returned nil.
	AcceptedKeyID string
}
