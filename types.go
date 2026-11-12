package taskpkg

import "fmt"

// SourceID identifies a package source/registry. A public mirror and an
// internal registry never share a SourceID even when they host the same path.
type SourceID string

// SignerID identifies the key/identity that signed a release.
type SignerID string

// Digest is the content digest of a candidate artifact (for example a hex SHA-256).
type Digest string

// NodeID identifies a graph node by BOTH source and path. Nodes that share a
// package path but come from different sources are distinct nodes and must
// never be merged by package name.
type NodeID struct {
	Source SourceID
	Path   string
}

// String renders the identity as "source/path" without changing identity
// semantics: callers must compare the struct fields, not this rendering.
func (n NodeID) String() string {
	return fmt.Sprintf("%s/%s", n.Source, n.Path)
}

// Proof is the cryptographic material checked by the injected Verifier. It is
// intentionally opaque: this package never inspects its contents directly.
type Proof struct {
	Signer  SignerID
	Payload []byte
}

// Candidate is a resolvable release of one NodeID.
type Candidate struct {
	Node    NodeID
	Version Version
	Digest  Digest
	Proof   Proof
}

// Verifier validates a candidate's Proof against its digest and claimed signer.
// Implementations are injected by the admission host so that this package stays
// independent of any concrete signing scheme or key store.
type Verifier interface {
	Verify(c Candidate) error
}

// VerifierFunc adapts a function to the Verifier interface.
type VerifierFunc func(Candidate) error

// Verify implements Verifier.
func (f VerifierFunc) Verify(c Candidate) error { return f(c) }

// MapVerifier is an in-memory Verifier useful for tests and deterministic
// offline hosts. Only digests present in ValidProofs can pass verification.
type MapVerifier struct {
	// ValidProofs maps a candidate digest to the single signer allowed to
	// have produced that artifact.
	ValidProofs map[Digest]SignerID
}

// Verify implements Verifier.
func (m MapVerifier) Verify(c Candidate) error {
	signer, ok := m.ValidProofs[c.Digest]
	if !ok {
		return fmt.Errorf("taskpkg: no trusted proof registered for digest %s", c.Digest)
	}
	if signer != c.Proof.Signer {
		return fmt.Errorf("taskpkg: proof signer %q does not match trusted signer %q for %s",
			c.Proof.Signer, signer, c.Digest)
	}
	return nil
}
