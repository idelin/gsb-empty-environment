package kt

import (
	"crypto/ed25519"
)

// OpKind enumerates the legal topology-migration operations.
type OpKind uint8

const (
	// OpSplit replaces the subtree at Prefix by a freshly built subtree whose
	// children were split into sub-prefixes.
	OpSplit OpKind = 1
	// OpMerge joins a prefix and its sibling back into their parent subtree.
	OpMerge OpKind = 2
	// OpRevokeOut abolishes a subtree: its entries are revoked and the subtree
	// is migrated out (replaced by an empty subtree).
	OpRevokeOut OpKind = 3
)

// String renders an operation kind for diagnostics.
func (k OpKind) String() string {
	switch k {
	case OpSplit:
		return "split"
	case OpMerge:
		return "merge"
	case OpRevokeOut:
		return "revoke-out"
	default:
		return "unknown"
	}
}

// authContext domain-separates authorization signatures.
var authContext = []byte("KT-AUTH-RECORD-v1")

// AuthRecord is a fresh issuance/authorization command produced by the
// directory authority. A resurrection attempt must present a brand new record
// (unique Nonce, current Epoch) rather than replaying an ancient one.
type AuthRecord struct {
	Epoch   uint32
	Prefix  BitPrefix
	Op      OpKind
	NewHash Hash // zero for OpRevokeOut
	Nonce   []byte
	Serial  uint64 // per-issuer strictly increasing sequence number
}

// SigningMessage returns the canonical byte string covered by the signature.
func (a AuthRecord) SigningMessage() []byte {
	parts := [][]byte{
		authContext,
		u32Bytes(a.Epoch),
		hashPrefix(a.Prefix),
		[]byte{byte(a.Op)},
		a.NewHash[:],
		a.Nonce,
		u64Bytes(a.Serial),
	}
	out := make([]byte, 0)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Hash returns the domain-tagged record commitment (used in replay checks).
func (a AuthRecord) Hash() Hash {
	return taggedHash(domainAuthRecord,
		u32Bytes(a.Epoch),
		hashPrefix(a.Prefix),
		[]byte{byte(a.Op)},
		a.NewHash[:],
		a.Nonce,
		u64Bytes(a.Serial))
}

// SignedAuth bundles an AuthRecord with an issuer Ed25519 signature.
type SignedAuth struct {
	Record    AuthRecord
	Issuer    ed25519.PublicKey
	Signature []byte
}

// SignAuth signs an authorization record.
func SignAuth(rec AuthRecord, issuer ed25519.PrivateKey) (*SignedAuth, error) {
	if len(issuer) != ed25519.PrivateKeySize {
		return nil, ErrBadKey
	}
	sig := ed25519.Sign(issuer, rec.SigningMessage())
	pub := append(ed25519.PublicKey(nil), issuer.Public().(ed25519.PublicKey)...)
	return &SignedAuth{Record: rec, Issuer: pub, Signature: sig}, nil
}

// Verify checks the signature against a set of trusted issuers and returns the
// issuer key on success.
func (s *SignedAuth) Verify(trustedIssuers []ed25519.PublicKey) error {
	if len(s.Issuer) != ed25519.PublicKeySize || len(s.Signature) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	trusted := false
	for _, k := range trustedIssuers {
		if equalBytes(k, s.Issuer) {
			trusted = true
			break
		}
	}
	if !trusted {
		return ErrUntrustedIssuer
	}
	if !ed25519.Verify(s.Issuer, s.Record.SigningMessage(), s.Signature) {
		return ErrBadSignature
	}
	return nil
}

// Migration is one topology change within an epoch. Multiple migrations per
// epoch are allowed, but their prefixes must be pairwise disjoint.
//
// OldHash is the subtree hash at Prefix in the previous epoch (empty hash for
// brand-new prefixes); NewHash is the replacement subtree hash in the current
// epoch (zero for OpRevokeOut).
type Migration struct {
	Epoch   uint32
	Prefix  BitPrefix
	Op      OpKind
	OldHash Hash
	NewHash Hash
	Auth    *SignedAuth
	// MergeOnly: sibling prefix and its previous-epoch hash for OpMerge.
	SiblingPrefix BitPrefix
	SiblingHash   Hash
}

// Hash returns the domain-tagged commitment to one migration entry.
func (m Migration) Hash() Hash {
	sibEnc := []byte{0}
	if m.Op == OpMerge {
		enc, _ := m.SiblingPrefix.MarshalBinary()
		sibEnc = append(enc, m.SiblingHash[:]...)
	}
	var authHash Hash
	if m.Auth != nil {
		authHash = m.Auth.Record.Hash()
	}
	return taggedHash(domainMigration,
		u32Bytes(m.Epoch),
		hashPrefix(m.Prefix),
		[]byte{byte(m.Op)},
		m.OldHash[:],
		m.NewHash[:],
		sibEnc,
		authHash[:])
}

// MigrationLog is the ordered set of migrations for one epoch.
type MigrationLog struct {
	Epoch   uint32
	Entries []Migration
}

// Hash computes the canonical, order-independent-ish (entries are sorted
// internally) commitment embedded in the epoch root.
func (l *MigrationLog) Hash() Hash {
	sorted := append([]Migration(nil), l.Entries...)
	sortMigrations(sorted)
	parts := [][]byte{u32Bytes(l.Epoch)}
	for i := range sorted {
		h := sorted[i].Hash()
		parts = append(parts, h[:])
	}
	return taggedHash(domainMigration, parts...)
}

func sortMigrations(ms []Migration) {
	// Deterministic ordering by prefix total order.
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && ms[j].Prefix.Compare(ms[j-1].Prefix) < 0; j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}

var (
	// ErrUntrustedIssuer indicates an authorization from an unknown issuer.
	ErrUntrustedIssuer = prefixError("kt: untrusted authorization issuer")
	// ErrUnknownOp indicates an operation kind outside the legal enum.
	ErrUnknownOp = prefixError("kt: unknown migration operation")
	// ErrMigrationPrefixes indicates overlapping migration prefixes.
	ErrMigrationPrefixes = prefixError("kt: migration prefixes must be disjoint")
	// ErrMigrationEpoch indicates an epoch number mismatch inside a log.
	ErrMigrationEpoch = prefixError("kt: migration epoch mismatch")
	// ErrMergeSibling indicates a malformed merge sibling relationship.
	ErrMergeSibling = prefixError("kt: merge sibling prefix/hash invalid")
	// ErrMissingAuth indicates a migration without a fresh authorization.
	ErrMissingAuth = prefixError("kt: missing fresh authorization record")
	// ErrAuthReplay indicates a replayed authorization (nonce/serial reuse).
	ErrAuthReplay = prefixError("kt: authorization record replay")
	// ErrAuthEpoch indicates an authorization bound to the wrong epoch.
	ErrAuthEpoch = prefixError("kt: authorization epoch mismatch")
	// ErrHashReuse indicates reuse of a revoked subtree hash (resurrection).
	ErrHashReuse = prefixError("kt: revoked subtree hash reuse forbidden")
	// ErrMigrationHash indicates an old/new subtree hash mismatch.
	ErrMigrationHash = prefixError("kt: migration subtree hash mismatch")
)
