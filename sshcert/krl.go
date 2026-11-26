package sshcert

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

const (
	krlMagic        = "SSHKRL"
	krlVersionMajor = 1

	// Top-level section types from PROTOCOL.krl.
	krlSectionNull              = 0
	krlSectionCertificates      = 1
	krlSectionExplicitKey       = 2
	krlSectionFingerprintSHA1   = 3
	krlSectionSignature         = 4
	krlSectionFingerprintSHA256 = 5

	// CERTIFICATES-section subsection types.
	krlCertSerialList   = 0x20
	krlCertSerialRange  = 0x21
	krlCertSerialBitmap = 0x22
	krlCertKeyID        = 0x23
)

type serialRange struct {
	min uint64
	max uint64
}

type serialBitmap struct {
	offset uint64
	bitmap []byte
}

type caRevocation struct {
	caKeyBlob []byte
	serials   []uint64
	ranges    []serialRange
	bitmaps   []serialBitmap
	keyIDs    [][]byte

	lastSubsectionType int
	lastRangeIndex     int
	lastBitmapOffset   uint64
}

// KRL is a fully parsed and validated binary key revocation list.
type KRL struct {
	version       uint32
	generatedDate uint64
	comment       string

	caGroups     []caRevocation
	explicitKeys [][]byte
	fingerprints [][]byte
}

func (k *KRL) Version() uint32       { return k.version }
func (k *KRL) GeneratedDate() uint64 { return k.generatedDate }
func (k *KRL) Comment() string       { return k.comment }

// ParseKRL decodes and strictly validates an SSHKRL binary blob. It rejects
// truncated records, out-of-bounds lengths, unknown/duplicated/out-of-order
// sections, unsorted CA keys, unsorted or overlapping serial structures,
// out-of-order bitmaps, malformed key-id lists and bad fingerprint widths.
func ParseKRL(raw []byte) (*KRL, error) {
	r := newWireReader(raw)

	magic, err := r.bytes(len(krlMagic))
	if err != nil {
		return nil, ErrTruncated
	}
	if string(magic) != krlMagic {
		return nil, ErrKRLMagic
	}
	version, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if version != krlVersionMajor {
		return nil, fmt.Errorf("%w: got %d", ErrKRLVersion, version)
	}
	generated, err := r.uint64()
	if err != nil {
		return nil, err
	}
	comment, err := r.string()
	if err != nil {
		return nil, err
	}

	k := &KRL{
		version:       version,
		generatedDate: generated,
		comment:       string(comment),
	}

	lastSection := krlSectionNull
	for r.remaining() > 0 {
		sectionType, err := r.byte()
		if err != nil {
			return nil, err
		}
		sectionLen, err := r.uint32()
		if err != nil {
			return nil, err
		}
		if uint64(sectionLen) > uint64(r.remaining()) {
			return nil, ErrTruncated
		}
		payload, err := r.bytes(int(sectionLen))
		if err != nil {
			return nil, ErrTruncated
		}

		if sectionType == krlSectionNull {
			// The NULL section terminates the KRL; it carries no payload.
			// Bytes after it are ignored rather than treated as a new
			// section, matching OpenSSH's parser behavior.
			if sectionLen != 0 {
				return nil, ErrKRLSectionOrder
			}
			return k, nil
		}
		// Sections must appear once, in strictly ascending type order.
		if int(sectionType) <= lastSection {
			return nil, ErrKRLSectionOrder
		}
		lastSection = int(sectionType)

		switch sectionType {
		case krlSectionCertificates:
			if err := k.parseCertificates(payload); err != nil {
				return nil, err
			}
		case krlSectionExplicitKey:
			if err := k.parseExplicitKeys(payload); err != nil {
				return nil, err
			}
		case krlSectionFingerprintSHA1:
			// Valid on disk (20-byte records), but this verifier only
			// implements SHA-256 fingerprints; structural validation still
			// runs so a corrupt SHA1 section cannot be silently accepted.
			if err := validateFingerprintWidth(payload, 20); err != nil {
				return nil, err
			}
		case krlSectionFingerprintSHA256:
			if err := k.parseFingerprints(payload); err != nil {
				return nil, err
			}
		case krlSectionSignature:
			// A detached KRL signature section: structurally length-bounded
			// by the generic section reader; its contents do not affect
			// revocation decisions and need no further decoding here.
		default:
			return nil, fmt.Errorf("%w: type %d", ErrKRLUnknownSection, sectionType)
		}
	}
	return k, nil
}

// parseCertificates validates the CERTIFICATES section. On disk each record
// is:
//
//	string ca_key        // empty string means "all CAs"
//	string reserved      // currently ignored, must still be consumed
//	byte   cert_section_type
//	string cert_section_data
//
// Adjacent records carrying the same CA blob form one logical CA group.
func (k *KRL) parseCertificates(payload []byte) error {
	r := newWireReader(payload)
	var prevCA []byte
	groupIdx := -1

	for r.remaining() > 0 {
		caBlob, err := r.string()
		if err != nil {
			return ErrKRLBadCAKey
		}
		// The per-record reserved string must exist and be consumed.
		if _, err := r.string(); err != nil {
			return ErrKRLBadCAKey
		}

		// CA blobs must be non-decreasing; equal adjacent blobs continue a
		// group, a strictly larger blob (including the empty wildcard blob)
		// opens the next CA group.
		if prevCA != nil && bytesLess(caBlob, prevCA) {
			return ErrKRLSectionOrder
		}
		if groupIdx == -1 || !bytesEqual(caBlob, prevCA) {
			k.caGroups = append(k.caGroups, caRevocation{
				caKeyBlob:          append([]byte(nil), caBlob...),
				lastSubsectionType: 0,
				lastRangeIndex:     -1,
			})
			groupIdx = len(k.caGroups) - 1
		}
		prevCA = caBlob

		subType, err := r.byte()
		if err != nil {
			return ErrTruncated
		}
		subPayload, err := r.string()
		if err != nil {
			return ErrTruncated
		}
		if err := k.caGroups[groupIdx].parseSubsection(subType, subPayload); err != nil {
			return err
		}
	}
	return nil
}

// parseSubsection enforces per-type ordering and structural rules for one
// cert subsection inside a CA group. The same subsection type may repeat
// (multiple ranges, bitmaps or lists); earlier types may never reappear.
func (g *caRevocation) parseSubsection(subType byte, payload []byte) error {
	if int(subType) < g.lastSubsectionType {
		return ErrKRLSectionOrder
	}

	switch subType {
	case krlCertSerialList:
		g.lastSubsectionType = krlCertSerialList
		return g.parseSerialList(payload)
	case krlCertSerialRange:
		g.lastSubsectionType = krlCertSerialRange
		return g.parseSerialRange(payload)
	case krlCertSerialBitmap:
		g.lastSubsectionType = krlCertSerialBitmap
		return g.parseBitmap(payload)
	case krlCertKeyID:
		g.lastSubsectionType = krlCertKeyID
		return g.parseKeyIDs(payload)
	default:
		return ErrKRLUnknownSection
	}
}

// parseSerialList parses a raw sequence of uint64 serials running to the
// end of the subsection (there is no per-list reserved field on disk).
// List sections may appear multiple times within one CA group; the merged
// result must remain strictly ascending.
func (g *caRevocation) parseSerialList(payload []byte) error {
	r := newWireReader(payload)
	start := len(g.serials)
	for r.remaining() > 0 {
		serial, err := r.uint64()
		if err != nil {
			return ErrTruncated
		}
		g.serials = append(g.serials, serial)
	}
	if !uint64Sorted(g.serials[start:]) {
		return ErrKRLBadSerialList
	}
	if start > 0 && len(g.serials) > start && g.serials[start] <= g.serials[start-1] {
		return ErrKRLBadSerialList
	}
	return nil
}

// parseSerialRange parses one (min, max) pair. Ranges must be ascending and
// non-overlapping; max == 0 denotes an open-ended range and must be last.
func (g *caRevocation) parseSerialRange(payload []byte) error {
	r := newWireReader(payload)
	lo, err := r.uint64()
	if err != nil {
		return ErrTruncated
	}
	hi, err := r.uint64()
	if err != nil {
		return ErrTruncated
	}
	if !r.done() {
		return ErrKRLBadSerialRange
	}
	if hi != 0 && lo > hi {
		return ErrKRLBadSerialRange
	}
	if g.lastRangeIndex >= 0 {
		prev := g.ranges[g.lastRangeIndex]
		if prev.max == 0 || lo <= prev.max {
			return ErrKRLBadSerialRange
		}
	}
	g.ranges = append(g.ranges, serialRange{min: lo, max: hi})
	g.lastRangeIndex = len(g.ranges) - 1
	return nil
}

// parseBitmap parses a uint64 serial_offset followed by an mpint bitmap
// (RFC 4251 signed bignum, here required to be non-negative). Offsets must
// be strictly ascending and the bitmap must be present.
func (g *caRevocation) parseBitmap(payload []byte) error {
	r := newWireReader(payload)
	offset, err := r.uint64()
	if err != nil {
		return ErrTruncated
	}
	bitmap, err := r.mpint()
	if err != nil {
		return err
	}
	if !r.done() {
		return ErrKRLBadBitmap
	}
	if len(bitmap) == 0 {
		return ErrKRLBadBitmap
	}
	if len(g.bitmaps) > 0 && offset <= g.lastBitmapOffset {
		return ErrKRLBadBitmap
	}
	g.bitmaps = append(g.bitmaps, serialBitmap{
		offset: offset,
		bitmap: append([]byte(nil), bitmap...),
	})
	g.lastBitmapOffset = offset
	return nil
}

// parseKeyIDs parses strictly ascending SSH strings. An empty string is the
// wildcard ("revoke every key id for this CA") and must appear alone.
func (g *caRevocation) parseKeyIDs(payload []byte) error {
	r := newWireReader(payload)
	var prev []byte
	wildcard := false
	for r.remaining() > 0 {
		id, err := r.string()
		if err != nil {
			return ErrKRLBadKeyID
		}
		if wildcard {
			return ErrKRLBadKeyID
		}
		if len(id) == 0 {
			wildcard = true
			g.keyIDs = append(g.keyIDs, []byte{})
			continue
		}
		if prev != nil && !bytesLess(prev, id) {
			return ErrKRLBadKeyID
		}
		g.keyIDs = append(g.keyIDs, append([]byte(nil), id...))
		prev = id
	}
	if !r.done() {
		return ErrKRLBadKeyID
	}
	return nil
}

// parseExplicitKeys parses ordered SSH public-key blobs, one per string.
func (k *KRL) parseExplicitKeys(payload []byte) error {
	r := newWireReader(payload)
	var prev []byte
	for r.remaining() > 0 {
		blob, err := r.string()
		if err != nil {
			return ErrKRLBadExplicitKey
		}
		if len(blob) == 0 {
			return ErrKRLBadExplicitKey
		}
		blob = wrapString(blob)
		if prev != nil && !bytesLess(prev, blob) {
			return ErrKRLBadExplicitKey
		}
		k.explicitKeys = append(k.explicitKeys, append([]byte(nil), blob...))
		prev = blob
	}
	if !r.done() {
		return ErrKRLBadExplicitKey
	}
	return nil
}

// parseFingerprints parses fixed-width 32-byte SHA-256 records, strictly
// ascending.
func (k *KRL) parseFingerprints(payload []byte) error {
	if err := validateFingerprintWidth(payload, sha256.Size); err != nil {
		return err
	}
	r := newWireReader(payload)
	var prev []byte
	for r.remaining() > 0 {
		hash, err := r.bytes(sha256.Size)
		if err != nil {
			return ErrTruncated
		}
		if prev != nil && !bytesLess(prev, hash) {
			return ErrKRLBadFingerprint
		}
		k.fingerprints = append(k.fingerprints, append([]byte(nil), hash...))
		prev = hash
	}
	return nil
}

// validateFingerprintWidth enforces that a fingerprint section consists of
// an integral number of fixed-width records (20 bytes for legacy SHA-1,
// 32 bytes for SHA-256).
func validateFingerprintWidth(payload []byte, width int) error {
	if len(payload) == 0 || len(payload)%width != 0 {
		return ErrKRLBadFingerprint
	}
	return nil
}

// CheckCertificate returns an error wrapping ErrRevoked when the KRL revokes
// the certificate through any supported mechanism:
//
//   - explicit public-key blob match,
//   - SHA-256 fingerprint of the public-key blob,
//   - CA-scoped serial list/range/bitmap (serial zero included),
//   - CA-scoped key id (including the wildcard entry).
func (k *KRL) CheckCertificate(c *Certificate) error {
	if idx := sort.Search(len(k.explicitKeys), func(i int) bool {
		return !bytesLess(k.explicitKeys[i], c.PublicKeyBlob)
	}); idx < len(k.explicitKeys) && bytesEqual(k.explicitKeys[idx], c.PublicKeyBlob) {
		return fmt.Errorf("%w: explicit key", ErrRevoked)
	}

	sum := sha256.Sum256(c.PublicKeyBlob)
	fp := sum[:]
	if idx := sort.Search(len(k.fingerprints), func(i int) bool {
		return !bytesLess(k.fingerprints[i], fp)
	}); idx < len(k.fingerprints) && bytesEqual(k.fingerprints[idx], fp) {
		return fmt.Errorf("%w: sha256 fingerprint", ErrRevoked)
	}

	idx := sort.Search(len(k.caGroups), func(i int) bool {
		return !bytesLess(k.caGroups[i].caKeyBlob, c.SignatureKey)
	})
	// An empty CA blob is the protocol wildcard: its subsections apply to
	// every certificate regardless of issuing CA.
	if idx < len(k.caGroups) && bytesEqual(k.caGroups[idx].caKeyBlob, c.SignatureKey) {
		if err := k.caGroups[idx].checkSerialAndKeyID(c); err != nil {
			return err
		}
	}
	for i := range k.caGroups {
		if len(k.caGroups[i].caKeyBlob) == 0 {
			if err := k.caGroups[i].checkSerialAndKeyID(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSerialAndKeyID applies every CA-scoped revocation mechanism. The
// serial-zero case is an ordinary membership test: zero is never treated as
// "unset" here, so certificates deliberately issued with serial 0 are still
// revocable through lists, ranges and bitmaps.
func (g *caRevocation) checkSerialAndKeyID(c *Certificate) error {
	// Key-id wildcard (empty entry) revokes every certificate of this CA.
	for _, id := range g.keyIDs {
		if len(id) == 0 || string(id) == c.KeyID {
			return fmt.Errorf("%w: key id", ErrRevoked)
		}
	}

	if i := sort.Search(len(g.serials), func(i int) bool {
		return g.serials[i] >= c.Serial
	}); i < len(g.serials) && g.serials[i] == c.Serial {
		return fmt.Errorf("%w: serial list", ErrRevoked)
	}

	for _, rng := range g.ranges {
		if c.Serial >= rng.min && (rng.max == 0 || c.Serial <= rng.max) {
			return fmt.Errorf("%w: serial range", ErrRevoked)
		}
	}

	for _, bm := range g.bitmaps {
		if err := bm.check(c.Serial); err != nil {
			return err
		}
	}
	return nil
}

// check applies one serial bitmap. The bitmap is a non-negative mpint; bit
// N of its magnitude corresponds to serial offset+N. The least-significant
// bit of the final (lowest-address after the optional sign zero byte)
// magnitude byte is bit 0.
func (bm serialBitmap) check(serial uint64) error {
	if serial < bm.offset {
		return nil
	}
	rel := serial - bm.offset
	byteIndex := rel / 8
	magnitude := bm.bitmap
	if len(magnitude) > 1 && magnitude[0] == 0 {
		magnitude = magnitude[1:]
	}
	if byteIndex >= uint64(len(magnitude)) {
		return nil
	}
	bit := byte(1 << (rel % 8))
	// Big-endian magnitude: bit 0 is the LSB of the final byte.
	bytePos := uint64(len(magnitude)) - 1 - byteIndex
	if magnitude[bytePos]&bit != 0 {
		return fmt.Errorf("%w: serial bitmap", ErrRevoked)
	}
	return nil
}
