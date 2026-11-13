package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// CacheID identifies one RPKI cache. It is an alias so configuration files
// and callers can use ordinary strings without conversions.
type CacheID = string

// Digest is a SHA-256 digest of a canonically encoded VRP set.
type Digest [32]byte

func (d Digest) Hex() string {
	const hexDigits = "0123456789abcdef"
	var out [64]byte
	for i, b := range d {
		out[i*2] = hexDigits[b>>4]
		out[i*2+1] = hexDigits[b&0x0f]
	}
	return string(out[:])
}

func (d Digest) String() string { return d.Hex() }

func DigestFromBytes(b []byte) (Digest, error) {
	var d Digest
	if len(b) != len(d) {
		return d, fmt.Errorf("%w: digest is %d bytes, want %d", ErrInvalidState, len(b), len(d))
	}
	copy(d[:], b)
	return d, nil
}

// VRP is a validated-route advertisement payload:
// (prefix, maxLength, originAS).
type VRP struct {
	Prefix    string `json:"prefix"`
	MaxLength uint8  `json:"maxLength"`
	OriginAS  uint32 `json:"originAS"`
}

// ChangeOperation is the operation carried by an incremental update.
type ChangeOperation uint8

const (
	ChangeAdd    ChangeOperation = 1
	ChangeRemove ChangeOperation = 2
)

func (op ChangeOperation) String() string {
	switch op {
	case ChangeAdd:
		return "add"
	case ChangeRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Change is one Add/Remove operation in a serial delta.
type Change struct {
	Operation ChangeOperation `json:"operation"`
	VRP       VRP             `json:"vrp"`
}

// NewVRP validates and canonicalises a VRP.
func NewVRP(prefix string, maxLength uint8, originAS uint32) (VRP, error) {
	rec, err := canonicalVRP(VRP{Prefix: prefix, MaxLength: maxLength, OriginAS: originAS})
	if err != nil {
		return VRP{}, err
	}
	return rec.vrp, nil
}

// VRPFromPrefix is the netip.Prefix form of NewVRP.
func VRPFromPrefix(prefix netip.Prefix, maxLength uint8, originAS uint32) (VRP, error) {
	return NewVRP(prefix.String(), maxLength, originAS)
}

// MustVRP is NewVRP for static configuration.
func MustVRP(prefix string, maxLength uint8, originAS uint32) VRP {
	v, err := NewVRP(prefix, maxLength, originAS)
	if err != nil {
		panic(err)
	}
	return v
}

// Validate reports whether the VRP is syntactically valid and canonical.
func (v VRP) Validate() error {
	rec, err := canonicalVRP(v)
	if err != nil {
		return err
	}
	if rec.vrp != v {
		return fmt.Errorf("%w: VRP is not canonical (use %s)", ErrMalformedMessage, rec.vrp.Prefix)
	}
	return nil
}

// Key returns a canonical map key. Invalid VRPs return the empty string.
func (v VRP) Key() string {
	rec, err := canonicalVRP(v)
	if err != nil {
		return ""
	}
	return rec.key
}

type vrpRecord struct {
	vrp       VRP
	key       string
	prefix    netip.Prefix
	bits      int
	maxLength uint8
	originAS  uint32
}

func canonicalVRP(v VRP) (vrpRecord, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(v.Prefix))
	if err != nil {
		return vrpRecord{}, fmt.Errorf("%w: invalid prefix %q: %v", ErrMalformedMessage, v.Prefix, err)
	}
	if !prefix.IsValid() {
		return vrpRecord{}, fmt.Errorf("%w: invalid prefix %q", ErrMalformedMessage, v.Prefix)
	}

	prefix = prefix.Masked()
	bits := prefix.Bits()
	addrBits := prefix.Addr().BitLen()
	if bits < 0 || bits > addrBits {
		return vrpRecord{}, fmt.Errorf("%w: prefix length %d for %q", ErrMalformedMessage, bits, v.Prefix)
	}
	if int(v.MaxLength) < bits || int(v.MaxLength) > addrBits {
		return vrpRecord{}, fmt.Errorf("%w: maxLength %d outside [%d,%d]", ErrMalformedMessage, v.MaxLength, bits, addrBits)
	}

	canonical := VRP{
		Prefix:    prefix.String(),
		MaxLength: v.MaxLength,
		OriginAS:  v.OriginAS,
	}
	key := prefix.String() + "|" + strconv.Itoa(int(v.MaxLength)) + "|" + strconv.FormatUint(uint64(v.OriginAS), 10)
	return vrpRecord{
		vrp:       canonical,
		key:       key,
		prefix:    prefix,
		bits:      bits,
		maxLength: v.MaxLength,
		originAS:  v.OriginAS,
	}, nil
}

// CanonicalVRPs validates, de-duplicates and sorts a set. Duplicate entries,
// including differently-written forms of the same prefix, are rejected.
func CanonicalVRPs(vrps []VRP) ([]VRP, error) {
	_, sorted, err := canonicalVRPSet(vrps)
	if err != nil {
		return nil, err
	}
	out := make([]VRP, len(sorted))
	for i := range sorted {
		out[i] = sorted[i].vrp
	}
	return out, nil
}

func canonicalVRPSet(vrps []VRP) (map[string]vrpRecord, []vrpRecord, error) {
	set := make(map[string]vrpRecord, len(vrps))
	sorted := make([]vrpRecord, 0, len(vrps))
	for _, v := range vrps {
		rec, err := canonicalVRP(v)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := set[rec.key]; exists {
			return nil, nil, fmt.Errorf("%w: %s", ErrDuplicateVRP, rec.key)
		}
		set[rec.key] = rec
		sorted = append(sorted, rec)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	return set, sorted, nil
}

// ComputeRoot returns the canonical root digest of a VRP set.
func ComputeRoot(vrps []VRP) (Digest, error) {
	_, sorted, err := canonicalVRPSet(vrps)
	if err != nil {
		return Digest{}, err
	}

	h := sha256.New()
	_, _ = h.Write([]byte("netsec/vrp-root/v1\x00"))
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(sorted)))
	_, _ = h.Write(buf[:])
	for _, rec := range sorted {
		addr := rec.prefix.Addr()
		if addr.Is4() {
			_, _ = h.Write([]byte{4})
			a := addr.As4()
			_, _ = h.Write(a[:])
		} else {
			_, _ = h.Write([]byte{6})
			a := addr.As16()
			_, _ = h.Write(a[:])
		}
		binary.BigEndian.PutUint32(buf[:], uint32(rec.bits))
		_, _ = h.Write(buf[:])
		_, _ = h.Write([]byte{rec.maxLength})
		binary.BigEndian.PutUint32(buf[:], rec.originAS)
		_, _ = h.Write(buf[:])
	}
	var out Digest
	copy(out[:], h.Sum(nil))
	return out, nil
}

// ComputeRootDigest is an alias retained for callers that prefer the longer
// name.
func ComputeRootDigest(vrps []VRP) (Digest, error) { return ComputeRoot(vrps) }

func encodeVRP(buf []byte, rec vrpRecord) []byte {
	addr := rec.prefix.Addr()
	if addr.Is4() {
		buf = append(buf, 4)
		a := addr.As4()
		buf = append(buf, a[:]...)
	} else {
		buf = append(buf, 6)
		a := addr.As16()
		buf = append(buf, a[:]...)
	}
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(rec.bits))
	buf = append(buf, tmp[:]...)
	buf = append(buf, rec.maxLength)
	binary.BigEndian.PutUint32(tmp[:], rec.originAS)
	buf = append(buf, tmp[:]...)
	return buf
}

func vrpMatches(rec vrpRecord, route netip.Prefix, originAS uint32) bool {
	if !route.IsValid() {
		return false
	}
	route = route.Masked()
	if int(rec.maxLength) < route.Bits() {
		return false
	}
	if rec.bits > route.Bits() {
		return false
	}
	if !rec.prefix.Contains(route.Addr()) {
		return false
	}
	return rec.originAS == originAS
}
