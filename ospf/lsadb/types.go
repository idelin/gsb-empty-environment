package lsadb

import "errors"

// OSPFv2 protocol constants from RFC 2328.
const (
	// MaxAge is the age at which an LSA must be flushed from the domain.
	MaxAge = 3600

	// HeaderLength is the size in bytes of the fixed OSPFv2 LSA header.
	HeaderLength = 20

	// InitSequenceNumber is the first sequence number used when an LSA is
	// originated (RFC 2328 section 12.1.2).
	InitSequenceNumber int32 = -0x7fffffff // 0x80000001

	// MaxSequenceNumber is the largest legal sequence number. When it is
	// reached the LSA is MaxAge-flushed and re-originated at
	// InitSequenceNumber.
	MaxSequenceNumber int32 = 0x7fffffff

	// reservedSequenceNumber (0x80000000) must never appear in an LSA.
	reservedSequenceNumber int32 = -0x80000000
)

// Sentinel errors returned by the admission path. Callers can use errors.Is
// to distinguish rejection reasons.
var (
	ErrMalformedLSA     = errors.New("lsadb: malformed LSA")
	ErrChecksum         = errors.New("lsadb: LSA Fletcher checksum mismatch")
	ErrReservedSequence = errors.New("lsadb: reserved LS sequence number 0x80000000")
	ErrAuthSequence     = errors.New("lsadb: cryptographic authentication sequence number regression")
	ErrReoriginate      = errors.New("lsadb: self LSA re-origination failed")
)

// RouterID is an OSPF router identifier, a 32-bit unsigned value that is
// conventionally printed in dotted-decimal notation.
type RouterID uint32

// String renders the router ID in dotted-decimal notation.
func (id RouterID) String() string {
	return formatIPv4(uint32(id))
}

func formatIPv4(v uint32) string {
	const digits = "0123456789"
	buf := make([]byte, 0, 15)
	for shift := 24; shift >= 0; shift -= 8 {
		octet := byte(v >> shift)
		if octet >= 100 {
			buf = append(buf, digits[octet/100], digits[(octet/10)%10], digits[octet%10])
		} else if octet >= 10 {
			buf = append(buf, digits[octet/10], digits[octet%10])
		} else {
			buf = append(buf, digits[octet])
		}
		if shift != 0 {
			buf = append(buf, '.')
		}
	}
	return string(buf)
}

// Key uniquely identifies an LSA instance in the link-state database: an LSA
// is fully identified by its type, Link State ID and advertising router.
type Key struct {
	LSType            uint8
	LinkStateID       RouterID
	AdvertisingRouter RouterID
}

// LSA is a parsed OSPFv2 link-state advertisement together with its original
// wire bytes (when it was produced by ParseLSA or Marshal).
type LSA struct {
	// LSAGE is the LS age field in seconds.
	LSAGE uint16

	// Options is the OSPF options byte carried in the LSA header.
	Options uint8

	// LSType is the LS type field, e.g. 1 for a router-LSA.
	LSType uint8

	// LinkStateID is the Link State ID field.
	LinkStateID RouterID

	// AdvertisingRouter is the router ID of the LSA originator.
	AdvertisingRouter RouterID

	// SeqNum is the signed 32-bit LS sequence number.
	SeqNum int32

	// Checksum is the Fletcher-16 LS checksum covering the whole LSA except
	// the LS age field.
	Checksum uint16

	// Length is the total LSA length in bytes, including the 20-byte header.
	Length uint16

	// Body holds the LSA payload after the fixed header.
	Body []byte

	// raw is the canonical wire encoding; nil when the LSA was built
	// in-process and has not yet been marshaled.
	raw []byte
}

// Key returns the database key of the LSA.
func (l *LSA) Key() Key {
	return Key{
		LSType:            l.LSType,
		LinkStateID:       l.LinkStateID,
		AdvertisingRouter: l.AdvertisingRouter,
	}
}

// Raw returns the canonical wire encoding of the LSA, or nil when it has not
// been marshaled yet. The returned slice must not be modified.
func (l *LSA) Raw() []byte {
	return l.raw
}

// IsMaxAge reports whether the LSA has reached MaxAge and must be flushed.
func (l *LSA) IsMaxAge() bool {
	return l.LSAGE >= MaxAge
}
