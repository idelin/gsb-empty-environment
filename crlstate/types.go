package crlstate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// IssuerID is the permanent identity of a certificate issuer. It is not the
// textual name from a certificate; hosts should use a name-constrained,
// collision-resistant identifier (for example a SPKI hash).
type IssuerID string

// RootID identifies a cryptographic root or cross-signing trust anchor.
type RootID string

// ReasonCode uses the X.509 CRLReason code space.
type ReasonCode uint8

const (
	ReasonUnspecified          ReasonCode = 0
	ReasonKeyCompromise        ReasonCode = 1
	ReasonCACompromise         ReasonCode = 2
	ReasonAffiliationChanged   ReasonCode = 3
	ReasonSuperseded           ReasonCode = 4
	ReasonCessationOfOperation ReasonCode = 5
	ReasonCertificateHold      ReasonCode = 6
	ReasonRemoveFromCRL        ReasonCode = 8
	ReasonPrivilegeWithdrawn   ReasonCode = 9
	ReasonAACompromise         ReasonCode = 10
)

// ReasonMask is a bit mask over ReasonCode values. Bit n represents
// ReasonCode(n).
type ReasonMask uint16

const (
	MaskUnspecified          ReasonMask = 1 << ReasonUnspecified
	MaskKeyCompromise        ReasonMask = 1 << ReasonKeyCompromise
	MaskCACompromise         ReasonMask = 1 << ReasonCACompromise
	MaskAffiliationChanged   ReasonMask = 1 << ReasonAffiliationChanged
	MaskSuperseded           ReasonMask = 1 << ReasonSuperseded
	MaskCessationOfOperation ReasonMask = 1 << ReasonCessationOfOperation
	MaskCertificateHold      ReasonMask = 1 << ReasonCertificateHold
	MaskRemoveFromCRL        ReasonMask = 1 << ReasonRemoveFromCRL
	MaskPrivilegeWithdrawn   ReasonMask = 1 << ReasonPrivilegeWithdrawn
	MaskAACompromise         ReasonMask = 1 << ReasonAACompromise

	// AllStatusReasons contains every reason that can describe a status. It
	// deliberately excludes removeFromCRL, which is a delta-CRL operation
	// rather than a reason a certificate can be revoked for.
	AllStatusReasons = MaskUnspecified |
		MaskKeyCompromise |
		MaskCACompromise |
		MaskAffiliationChanged |
		MaskSuperseded |
		MaskCessationOfOperation |
		MaskCertificateHold |
		MaskPrivilegeWithdrawn |
		MaskAACompromise

	// AllReasonBits contains every defined reason bit, including the
	// removeFromCRL operation bit.
	AllReasonBits = AllStatusReasons | MaskRemoveFromCRL
)

// Bit returns the mask containing only code.
func (c ReasonCode) Bit() ReasonMask {
	if c > 15 {
		return 0
	}
	return 1 << c
}

// Mask returns the singleton mask for c.
func (c ReasonCode) Mask() ReasonMask { return c.Bit() }

// Has reports whether m contains code.
func (m ReasonMask) Has(code ReasonCode) bool {
	return m&code.Bit() != 0
}

// HasAll reports whether m contains every bit in other.
func (m ReasonMask) HasAll(other ReasonMask) bool { return m&other == other }

// Intersect returns m∩other.
func (m ReasonMask) Intersect(other ReasonMask) ReasonMask { return m & other }

// Union returns m∪other.
func (m ReasonMask) Union(other ReasonMask) ReasonMask { return m | other }

// IsZero reports whether no bit is set.
func (m ReasonMask) IsZero() bool { return m == 0 }

// Bits iterates over set bits in ascending order.
func (m ReasonMask) Bits() []ReasonCode {
	var out []ReasonCode
	for i := 0; i < 16; i++ {
		bit := ReasonMask(1 << i)
		if m&bit != 0 {
			out = append(out, ReasonCode(i))
		}
	}
	return out
}

// String renders the mask as a compact list of reason names.
func (m ReasonMask) String() string {
	if m == 0 {
		return "none"
	}
	names := []string{
		"unspecified", "keyCompromise", "cACompromise", "affiliationChanged",
		"superseded", "cessationOfOperation", "certificateHold", "bit7",
		"removeFromCRL", "privilegeWithdrawn", "aACompromise",
	}
	var parts []string
	for _, code := range m.Bits() {
		if int(code) < len(names) {
			parts = append(parts, names[code])
		} else {
			parts = append(parts, fmt.Sprintf("bit%d", code))
		}
	}
	return strings.Join(parts, "|")
}

// Entry is one revoked or hold-released serial in a partition manifest.
type Entry struct {
	Serial    Serial
	RevokedAt time.Time
	Reason    ReasonCode
	// HoldID identifies the suspended credential. A removeFromCRL entry only
	// releases a certificateHold with the same IssuerID, Serial and HoldID.
	// Empty HoldID means the credential is the serial itself.
	HoldID string
}

// EffectiveCredential returns the stable key of the suspended credential.
func (e Entry) effectiveCredential() string {
	if e.HoldID != "" {
		return e.HoldID
	}
	return "serial:" + e.Serial.String()
}

// Delegation is one link in an indirectCRL authorization chain.
//
// The first link must be issued by Manifest.IssuerID, each subsequent link
// must continue from the previous Child, and the final Child must be the
// manifest's SignerID. A link's scope must contain the manifest's serial,
// reason and time scope.
type Delegation struct {
	Parent     IssuerID
	Child      IssuerID
	Root       RootID
	SerialLo   Serial
	SerialHi   Serial
	Reasons    ReasonMask
	ValidFrom  time.Time
	ValidUntil time.Time
}

// PartitionSpan describes a serial/reason region owned by one partition. It
// is used by split and merge migration declarations.
type PartitionSpan struct {
	PartitionID string
	SerialLo    Serial
	SerialHi    Serial
	Reasons     ReasonMask
}

// MigrationKind names a partition topology change.
type MigrationKind string

const (
	MigrationSplit MigrationKind = "split"
	MigrationMerge MigrationKind = "merge"
)

// Migration is a dual-root declaration that hands a serial/reason region from
// one partition generation to another. Old and New must describe exactly the
// same serial×reason region; the migration is signed by both roots.
type Migration struct {
	ID          string
	Kind        MigrationKind
	IssuerID    IssuerID
	FromEpoch   uint64
	ToEpoch     uint64
	OldRoot     RootID
	NewRoot     RootID
	Old         []PartitionSpan
	New         []PartitionSpan
	EffectiveAt time.Time
	// OldSignature is made with OldRoot's key and NewSignature with NewRoot's
	// key. Both sign the same canonical migration body.
	OldSignature []byte
	NewSignature []byte
}

// Manifest describes one full or delta CRL partition generation.
type Manifest struct {
	PartitionID string
	IssuerID    IssuerID
	// SignerID is the CRL signer. It defaults to IssuerID for a direct CRL.
	SignerID   IssuerID
	RootID     RootID
	Epoch      uint64
	SerialLo   Serial
	SerialHi   Serial
	Reasons    ReasonMask
	ValidFrom  time.Time
	ValidUntil time.Time
	Indirect   []Delegation
	// CRLNumber is this manifest's generation number. A full manifest and its
	// deltas share PartitionID; deltas increase CRLNumber.
	CRLNumber uint64
	// IsDelta marks an incremental manifest. BaseDelta must equal the
	// CRLNumber of the full base or of the preceding delta manifest.
	IsDelta   bool
	BaseDelta uint64
	Entries   []Entry
	Signature []byte
}

// Certificate is the status query for one certificate lifetime.
type Certificate struct {
	IssuerID IssuerID
	Serial   Serial
	Start    time.Time
	End      time.Time
	// Reasons narrows the status bits the host requires coverage for. Zero
	// means AllStatusReasons, which is the conservative default required by
	// the package contract.
	Reasons ReasonMask
}

// Status is the outcome of a status query.
type Status string

const (
	StatusUnknown Status = "unknown"
	StatusGood    Status = "good"
	StatusRevoked Status = "revoked"
	StatusLocked  Status = "locked"
)

// Gap describes a serial/reason/time region for which no complete signed
// partition plus matching delta chain was available.
type Gap struct {
	Start    time.Time
	End      time.Time
	Reasons  ReasonMask
	SerialLo Serial
	SerialHi Serial
	Epoch    uint64
	Root     RootID
}

// Snapshot is the immutable audit view of one manifest.
type Snapshot struct {
	PartitionID string
	IssuerID    IssuerID
	SignerID    IssuerID
	RootID      RootID
	Epoch       uint64
	CRLNumber   uint64
	IsDelta     bool
	BaseDelta   uint64
	SerialLo    Serial
	SerialHi    Serial
	Reasons     ReasonMask
	ValidFrom   time.Time
	ValidUntil  time.Time
	Digest      [32]byte
	Signature   []byte
}

// ForkEvidence records two valid, content-conflicting manifests under the same
// (IssuerID, RevocationEpoch).
type ForkEvidence struct {
	IssuerID IssuerID
	Epoch    uint64
	At       time.Time
	First    Snapshot
	Second   Snapshot
}

// Result is a complete status decision with audit details.
type Result struct {
	Status         Status
	Reason         ReasonCode
	RevokedAt      time.Time
	ManifestDigest [32]byte
	PartitionID    string
	Epoch          uint64
	Root           RootID
	Paths          int
	Missing        []Gap
	Fork           *ForkEvidence
	Err            error
}

// Clone returns a deep copy.
func (r Result) Clone() Result {
	out := r
	out.Missing = append([]Gap(nil), r.Missing...)
	if r.Fork != nil {
		f := *r.Fork
		f.First.Signature = append([]byte(nil), f.First.Signature...)
		f.Second.Signature = append([]byte(nil), f.Second.Signature...)
		out.Fork = &f
	}
	return out
}

func (m *Manifest) clone() *Manifest {
	if m == nil {
		return nil
	}
	out := *m
	out.Indirect = append([]Delegation(nil), m.Indirect...)
	out.Entries = append([]Entry(nil), m.Entries...)
	out.Signature = append([]byte(nil), m.Signature...)
	return &out
}

func (m *Manifest) snapshot(digest [32]byte) Snapshot {
	return Snapshot{
		PartitionID: m.PartitionID,
		IssuerID:    m.IssuerID,
		SignerID:    m.SignerID,
		RootID:      m.RootID,
		Epoch:       m.Epoch,
		CRLNumber:   m.CRLNumber,
		IsDelta:     m.IsDelta,
		BaseDelta:   m.BaseDelta,
		SerialLo:    m.SerialLo,
		SerialHi:    m.SerialHi,
		Reasons:     m.Reasons,
		ValidFrom:   m.ValidFrom,
		ValidUntil:  m.ValidUntil,
		Digest:      digest,
		Signature:   append([]byte(nil), m.Signature...),
	}
}

func (m *Migration) clone() *Migration {
	if m == nil {
		return nil
	}
	out := *m
	out.Old = append([]PartitionSpan(nil), m.Old...)
	out.New = append([]PartitionSpan(nil), m.New...)
	out.OldSignature = append([]byte(nil), m.OldSignature...)
	out.NewSignature = append([]byte(nil), m.NewSignature...)
	return &out
}

func sortSpans(spans []PartitionSpan) {
	sort.Slice(spans, func(i, j int) bool {
		if c := spans[i].SerialLo.Cmp(spans[j].SerialLo); c != 0 {
			return c < 0
		}
		if c := spans[i].SerialHi.Cmp(spans[j].SerialHi); c != 0 {
			return c < 0
		}
		if spans[i].Reasons != spans[j].Reasons {
			return spans[i].Reasons < spans[j].Reasons
		}
		return spans[i].PartitionID < spans[j].PartitionID
	})
}
