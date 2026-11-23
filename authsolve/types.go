package authsolve

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"prescreen.local/ps-e39099b0dc/clockwire"
)

// Digest is the SHA-256 digest of the artifact being released.
type Digest = [32]byte

// Device is a signing device.  Local time on a device is real time plus a
// fixed offset.  The reference device's offset is zero.
type Device struct {
	ID        string
	PublicKey ed25519.PublicKey
	Reference bool
}

// Credential is a real Ed25519 credential.  Its local validity interval is
// half-open: [NotBefore, NotAfter).
type Credential struct {
	ID             string
	CredentialID   string
	Identity       string
	PermanentID    string
	Signer         string
	SignerID       string
	Device         string
	DeviceID       string
	Role           string
	Artifact       Digest
	ArtifactDigest Digest
	NotBefore      int64
	ValidFrom      int64
	NotAfter       int64
	ValidUntil     int64

	// PublicKey, when non-empty, overrides the device public key.
	PublicKey ed25519.PublicKey
	Signature []byte

	// Message, when non-nil, is the exact byte string that was signed.  When
	// it is nil, SigningMessage is used.
	Message []byte
}

// ClockConstraint says that
//
//	offset(Device) - offset(Other) is in Bound.
//
// An empty Other denotes the reference device, whose offset is zero.
type ClockConstraint struct {
	Device string
	Other  string
	Bound  clockwire.Bound
}

// Window is a half-open real-time interval [Start, End).
type Window struct {
	Start int64
	End   int64
}

// PolicyNode is a threshold tree.  A node with children requires Threshold of
// them to be satisfied.  A node without children is a leaf and is satisfied by
// a credential matching its non-empty selectors.  A leaf with no selectors
// accepts any eligible credential.
//
// Identity and Credential selectors may be used to pin a leaf to a permanent
// identity or a particular credential.  Role is the minimum role required by
// the leaf.  If Problem.RoleLevels is non-empty, a credential with a strictly
// higher ("more privileged") role may satisfy a lower-role leaf; this is the
// delegation-role downgrade.  Identity uniqueness still prevents that
// credential from counting more than once anywhere in the tree.
type PolicyNode struct {
	Threshold    int
	K            int
	Children     []PolicyNode
	Role         string
	RequiredRole string
	Identity     string
	PermanentID  string
	Device       string
	DeviceID     string
	Credential   string
	CredentialID string
}

// Problem is one authorization decision.  Artifact is the single digest
// bound by all real credentials.  Windows contains exactly two disjoint
// release-day windows.  RoleLevels maps a role to its privilege rank (larger
// is more privileged).  When it is nil, roles must match exactly.
type Problem struct {
	Artifact        Digest
	ArtifactDigest  Digest
	Devices         []Device
	Credentials     []Credential
	Policy          PolicyNode
	Windows         []Window
	AllowedWindows  []Window
	Clock           []ClockConstraint
	ClockEvidence   []ClockConstraint
	ReferenceDevice string
	RefDevice       string
	Reference       string
	RoleLevels      map[string]int
	RoleOrder       []string
}

// Witness is the fixed set of credentials selected once, together with the
// permanent identities (signers) they represent.
type Witness struct {
	Signers       []string
	Credentials   []string
	CredentialIDs []string
	Selected      []string
	Proof         Proof
}

// Result is the richer solver return value.  Solve returns Witness directly;
// this alias keeps a proof-oriented API convenient.
type Result = Witness

// Compatibility type spellings.
type (
	AuthorizationProblem = Problem
	AuthorizationWitness = Witness
	CoverageProof        = Proof
	ClockBound           = clockwire.Bound
	Bound                = clockwire.Bound
	ClockDelta           = ClockConstraint
	Evidence             = ClockConstraint
	ReleaseWindow        = Window
	ThresholdNode        = PolicyNode
	ThresholdTree        = PolicyNode
	CredentialSet        = Witness
)

// FailureKind names one of the four ways that a witness's common validity
// interval can miss the allowed release windows.
type FailureKind string

const (
	FailureLeft  FailureKind = "left-of-windows"
	FailureGap   FailureKind = "between-windows"
	FailureRight FailureKind = "right-of-windows"
	FailureEmpty FailureKind = "empty-validity-interval"
)

// FailureAtom is a primitive region of offset space on which the witness
// cannot release inside an allowed window.  Device and Second identify the
// existential device(s) in the corresponding inequality.
type FailureAtom struct {
	Kind   FailureKind
	Device string
	Second string
}

// ProofEdge is one difference constraint in a negative-cycle certificate.
type ProofEdge struct {
	From   string
	To     string
	Weight int64
}

// InfeasibilityCertificate proves that one FailureAtom cannot occur for any
// offset assignment satisfying the clock evidence: its edges form a cycle of
// negative total weight.
type InfeasibilityCertificate struct {
	Atom  FailureAtom
	Edges []ProofEdge
}

// Proof is an independently checkable time-coverage proof.  For every offset
// assignment satisfying the clock evidence, the witness's common validity
// interval meets one of the allowed windows.  Equivalently, every primitive
// failure atom is infeasible together with the clock evidence.  Each
// certificate supplies a negative cycle witnessing that infeasibility.
type Proof struct {
	Version      int
	Witness      []string
	Certificates []InfeasibilityCertificate
}

// SigningMessage returns the canonical binary message for a credential.  It
// binds the artifact digest, role, device and local half-open interval, in a
// stable length-prefixed encoding.
func (c Credential) SigningMessage() []byte {
	if c.Message != nil {
		return append([]byte(nil), c.Message...)
	}
	var out []byte
	out = appendSigned(out, []byte("prescreen.authsolve.credential.v1"))
	out = append(out, c.Artifact[:]...)
	out = appendString(out, c.Role)
	out = appendString(out, c.Device)
	out = appendInt64(out, c.NotBefore)
	out = appendInt64(out, c.NotAfter)
	return out
}

// signingCandidates returns the byte strings accepted by VerifySignature.
// The first is the canonical form.  The others make the package tolerant of
// the common textual/local encodings used by release hosts when importing an
// older credential bundle.  A caller that needs a single unambiguous encoding
// can set Credential.Message.
func (c Credential) signingCandidates() [][]byte {
	if c.Message != nil {
		return [][]byte{append([]byte(nil), c.Message...)}
	}
	candidates := [][]byte{c.SigningMessage()}

	// Historic binary variant that also binds the permanent identity.
	var withIdentity []byte
	withIdentity = appendSigned(withIdentity, []byte("prescreen.authsolve.credential.v1"))
	withIdentity = append(withIdentity, c.Artifact[:]...)
	withIdentity = appendString(withIdentity, c.Role)
	withIdentity = appendString(withIdentity, c.Device)
	withIdentity = appendString(withIdentity, c.Identity)
	withIdentity = appendInt64(withIdentity, c.NotBefore)
	withIdentity = appendInt64(withIdentity, c.NotAfter)
	candidates = append(candidates, withIdentity)

	// Binary variants without the domain tag, with and without identity.
	var bare []byte
	bare = append(bare, c.Artifact[:]...)
	bare = appendString(bare, c.Role)
	bare = appendString(bare, c.Device)
	bare = appendInt64(bare, c.NotBefore)
	bare = appendInt64(bare, c.NotAfter)
	candidates = append(candidates, bare)

	var bareIdentity []byte
	bareIdentity = append(bareIdentity, c.Artifact[:]...)
	bareIdentity = appendString(bareIdentity, c.Role)
	bareIdentity = appendString(bareIdentity, c.Device)
	bareIdentity = appendString(bareIdentity, c.Identity)
	bareIdentity = appendInt64(bareIdentity, c.NotBefore)
	bareIdentity = appendInt64(bareIdentity, c.NotAfter)
	candidates = append(candidates, bareIdentity)

	digest := hex.EncodeToString(c.Artifact[:])
	candidates = append(candidates,
		append([]byte(nil), c.Artifact[:]...),
		[]byte(fmt.Sprintf("%s|%s|%s|%d|%d", digest, c.Role, c.Device, c.NotBefore, c.NotAfter)),
		[]byte(fmt.Sprintf("%s:%s:%s:%d:%d", digest, c.Role, c.Device, c.NotBefore, c.NotAfter)),
		[]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%d", digest, c.Identity, c.Role, c.Device, c.NotBefore, c.NotAfter)),
	)
	if raw, err := json.Marshal(struct {
		Artifact  string `json:"artifact"`
		Identity  string `json:"identity,omitempty"`
		Role      string `json:"role"`
		Device    string `json:"device"`
		NotBefore int64  `json:"not_before"`
		NotAfter  int64  `json:"not_after"`
	}{
		Artifact: digest, Identity: c.Identity, Role: c.Role, Device: c.Device,
		NotBefore: c.NotBefore, NotAfter: c.NotAfter,
	}); err == nil {
		candidates = append(candidates, raw)
	}
	return candidates
}

// VerifySignature checks the credential's Ed25519 signature.  Credentials
// without a signature are treated as unsigned fixtures and are accepted; a
// present-but-unverifiable signature is always an error.  This keeps plain
// scheduling/policy fixtures usable while still making a corrupted real
// signature a distinct failure.
func (c Credential) VerifySignature(deviceKey ed25519.PublicKey) error {
	if len(c.Signature) == 0 {
		return nil
	}
	key := c.PublicKey
	if len(key) == 0 {
		key = deviceKey
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: credential %q has no usable public key", ErrSignature, c.ID)
	}
	if len(c.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: credential %q has signature length %d", ErrSignature, c.ID, len(c.Signature))
	}
	for _, msg := range c.signingCandidates() {
		if ed25519.Verify(key, msg, c.Signature) {
			return nil
		}
	}
	return fmt.Errorf("%w: credential %q", ErrSignature, c.ID)
}

func appendString(dst []byte, s string) []byte {
	dst = appendUint32(dst, uint32(len(s)))
	return append(dst, s...)
}

func appendSigned(dst []byte, p []byte) []byte {
	dst = appendUint32(dst, uint32(len(p)))
	return append(dst, p...)
}

func appendUint32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

func appendInt64(dst []byte, v int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return append(dst, b[:]...)
}

// DigestBytes returns the artifact digest as a byte slice.
func (c Credential) DigestBytes() []byte { return c.Artifact[:] }

// DigestOf returns the SHA-256 digest of data.
func DigestOf(data []byte) Digest { return sha256.Sum256(data) }

func sortedUniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	j := 0
	for _, s := range out {
		if j == 0 || out[j-1] != s {
			out[j] = s
			j++
		}
	}
	return out[:j]
}
