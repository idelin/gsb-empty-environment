package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// PACType identifies a PAC_INFO_BUFFER as defined by [MS-PAC] section 2.4.
type PACType uint32

const (
	// PACTypeLogonInfo contains the KERB_VALIDATION_INFO structure.
	PACTypeLogonInfo PACType = 1
	// PACTypeCredentialInfo contains credential information.
	PACTypeCredentialInfo PACType = 2
	// PACTypeServerChecksum contains the service signature.
	PACTypeServerChecksum PACType = 6
	// PACTypeKDCChecksum contains the KDC (privilege server) signature.
	PACTypeKDCChecksum PACType = 7

	// PACTypePrivSvcChecksum is the name used by some specifications for the
	// KDC signature buffer.
	PACTypePrivSvcChecksum = PACTypeKDCChecksum
)

const (
	pacHeaderSize     = 8
	pacInfoBufferSize = 16

	// MaxPACBuffers bounds attacker-controlled descriptor processing.
	MaxPACBuffers = 4096

	// PACKeyUsage is KERB_NON_KERB_CKSUM_SALT, the key usage required for PAC
	// signatures.
	PACKeyUsage uint32 = 17
)

var (
	// ErrMalformedPAC indicates a structurally invalid PAC.
	ErrMalformedPAC = errors.New("netsec: malformed PAC")
	// ErrDuplicatePACBuffer indicates a duplicated PAC buffer type.
	ErrDuplicatePACBuffer = errors.New("netsec: duplicate PAC buffer type")
	// ErrOverlappingPACBuffers indicates PAC buffers whose byte ranges intersect.
	ErrOverlappingPACBuffers = errors.New("netsec: overlapping PAC buffers")
	// ErrMissingPACBuffer indicates a required PAC buffer is absent.
	ErrMissingPACBuffer = errors.New("netsec: missing PAC buffer")
	// ErrMissingKDCSignature indicates the PAC has no KDC signature.
	ErrMissingKDCSignature = errors.New("netsec: PAC has no KDC signature")
	// ErrInvalidPACSignature indicates a signature does not match its keyed hash.
	ErrInvalidPACSignature = errors.New("netsec: invalid PAC signature")
	// ErrUnsupportedChecksum indicates an unsupported checksum/signature type.
	ErrUnsupportedChecksum = errors.New("netsec: unsupported PAC checksum type")
	// ErrUnverifiedPAC indicates that logon information was requested from a
	// PAC whose server and KDC signatures have not both been verified.
	ErrUnverifiedPAC = errors.New("netsec: PAC has not been verified")
	// ErrMalformedLogonInfo indicates a structurally invalid KERB_VALIDATION_INFO.
	ErrMalformedLogonInfo = errors.New("netsec: malformed logon info")
)

// PACInfoBuffer is the parsed descriptor for one PAC buffer.
type PACInfoBuffer struct {
	Type   PACType
	Offset uint64
	Size   uint32
}

type pacBuffer struct {
	PACInfoBuffer
	data []byte
}

// PAC is a strictly parsed privilege attribute certificate. The input bytes
// are copied during parsing, so later mutation of the caller's slice cannot
// change a verified PAC.
type PAC struct {
	Version uint32

	raw     []byte
	buffers []pacBuffer

	serverVerified bool
	kdcVerified    bool
}

// ParsePAC parses a PAC without trusting any of its contents.
//
// Every descriptor is checked for integer overflow, bounds relative to the
// buffer, and accidental references into the PAC header/descriptor table.
// Buffer types must be unique and buffer byte ranges must not overlap.
func ParsePAC(data []byte) (*PAC, error) {
	if len(data) < pacHeaderSize {
		return nil, fmt.Errorf("%w: short header", ErrMalformedPAC)
	}

	raw := make([]byte, len(data))
	copy(raw, data)

	count := binary.LittleEndian.Uint32(raw[0:4])
	version := binary.LittleEndian.Uint32(raw[4:8])
	if version != 0 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedPAC, version)
	}
	if count == 0 {
		return nil, fmt.Errorf("%w: no buffers", ErrMalformedPAC)
	}
	if count > MaxPACBuffers {
		return nil, fmt.Errorf("%w: too many buffers (%d)", ErrMalformedPAC, count)
	}

	// The descriptor table must be present in full.
	if uint64(count) > uint64(len(raw)-pacHeaderSize)/pacInfoBufferSize {
		return nil, fmt.Errorf("%w: truncated buffer table", ErrMalformedPAC)
	}
	tableEnd := uint64(pacHeaderSize) + uint64(count)*pacInfoBufferSize

	p := &PAC{Version: version, raw: raw, buffers: make([]pacBuffer, 0, count)}
	seenTypes := make(map[PACType]struct{}, count)
	spans := make([]struct {
		start uint64
		end   uint64
		t     PACType
	}, 0, count)

	for i := uint32(0); i < count; i++ {
		desc := uint64(pacHeaderSize) + uint64(i)*pacInfoBufferSize
		typ := PACType(binary.LittleEndian.Uint32(raw[desc : desc+4]))
		size := binary.LittleEndian.Uint32(raw[desc+4 : desc+8])
		offset := binary.LittleEndian.Uint64(raw[desc+8 : desc+16])

		if _, ok := seenTypes[typ]; ok {
			return nil, fmt.Errorf("%w: type %d", ErrDuplicatePACBuffer, typ)
		}
		seenTypes[typ] = struct{}{}

		if size == 0 {
			return nil, fmt.Errorf("%w: type %d has zero length", ErrMalformedPAC, typ)
		}
		if offset < tableEnd {
			return nil, fmt.Errorf("%w: type %d offset %d points into the PAC header", ErrMalformedPAC, typ, offset)
		}
		if offset > uint64(len(raw)) || uint64(size) > uint64(len(raw))-offset {
			return nil, fmt.Errorf("%w: type %d range [%d,%d) is out of bounds", ErrMalformedPAC, typ, offset, offset+uint64(size))
		}
		end := offset + uint64(size)

		if (typ == PACTypeServerChecksum || typ == PACTypeKDCChecksum) && size < 4 {
			return nil, fmt.Errorf("%w: signature buffer type %d is too short", ErrMalformedPAC, typ)
		}

		p.buffers = append(p.buffers, pacBuffer{
			PACInfoBuffer: PACInfoBuffer{Type: typ, Offset: offset, Size: size},
			data:          raw[offset:end],
		})
		spans = append(spans, struct {
			start uint64
			end   uint64
			t     PACType
		}{offset, end, typ})
	}

	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return nil, fmt.Errorf(
				"%w: type %d [%d,%d) intersects type %d [%d,%d)",
				ErrOverlappingPACBuffers,
				spans[i-1].t, spans[i-1].start, spans[i-1].end,
				spans[i].t, spans[i].start, spans[i].end,
			)
		}
	}

	return p, nil
}

// Buffers returns a copy of the parsed buffer descriptors.
func (p *PAC) Buffers() []PACInfoBuffer {
	out := make([]PACInfoBuffer, len(p.buffers))
	for i, b := range p.buffers {
		out[i] = b.PACInfoBuffer
	}
	return out
}

// Buffer returns the descriptor for typ, if present.
func (p *PAC) Buffer(typ PACType) (PACInfoBuffer, bool) {
	for _, b := range p.buffers {
		if b.Type == typ {
			return b.PACInfoBuffer, true
		}
	}
	return PACInfoBuffer{}, false
}

// Verified reports whether both the server and KDC signatures have been
// successfully verified.
func (p *PAC) Verified() bool {
	return p.serverVerified && p.kdcVerified
}

// LogonInfo returns the parsed logon information. It refuses to return any
// SIDs unless both the server and KDC signatures have been verified.
func (p *PAC) LogonInfo() (*LogonInfo, error) {
	if !p.Verified() {
		return nil, ErrUnverifiedPAC
	}
	b, ok := p.buffer(PACTypeLogonInfo)
	if !ok {
		return nil, fmt.Errorf("%w: logon info", ErrMissingPACBuffer)
	}
	return parseLogonInfo(b.data)
}

func (p *PAC) buffer(typ PACType) (pacBuffer, bool) {
	for _, b := range p.buffers {
		if b.Type == typ {
			return b, true
		}
	}
	return pacBuffer{}, false
}

// ChecksumFunc computes a Kerberos keyed checksum. usage is the Kerberos key
// usage number. The returned value must be the exact signature length for the
// requested signature type.
type ChecksumFunc func(signatureType uint32, key, data []byte, usage uint32) ([]byte, error)

// Validator verifies a PAC for a GSS acceptor using the service key and the
// KDC key.
type Validator struct {
	// ServerKey is the long-term key shared by the KDC and the service.
	ServerKey []byte
	// KDCKey is the KDC (krbtgt/privilege-server) key.
	KDCKey []byte
	// KeyUsage is the Kerberos key usage. Zero selects PACKeyUsage (17).
	KeyUsage uint32
	// Checksum overrides the default checksum implementation. It can be used
	// for deployment-specific Kerberos crypto providers.
	Checksum ChecksumFunc
}

// ValidatePAC is a convenience wrapper for a one-shot validation with the
// service key and KDC key.
func ValidatePAC(data, serverKey, kdcKey []byte) (*LogonInfo, error) {
	return (&Validator{ServerKey: serverKey, KDCKey: kdcKey}).Accept(data)
}

// Validate parses and verifies a PAC, returning logon information only after
// both signatures have been validated. A PAC with a valid server signature but
// no KDC signature is rejected; SIDs from such a PAC must never be used.
//
// Validate is an alias for Accept.
func (v *Validator) Validate(data []byte) (*LogonInfo, error) {
	return v.Accept(data)
}

// Accept parses and verifies a PAC for GSS acceptor use.
func (v *Validator) Accept(data []byte) (*LogonInfo, error) {
	if len(v.ServerKey) == 0 {
		return nil, errors.New("netsec: server key is empty")
	}
	if len(v.KDCKey) == 0 {
		return nil, errors.New("netsec: KDC key is empty")
	}

	p, err := ParsePAC(data)
	if err != nil {
		return nil, err
	}

	if _, ok := p.buffer(PACTypeLogonInfo); !ok {
		return nil, fmt.Errorf("%w: logon info", ErrMissingPACBuffer)
	}
	if _, ok := p.buffer(PACTypeServerChecksum); !ok {
		return nil, fmt.Errorf("%w: server checksum", ErrMissingPACBuffer)
	}
	if _, ok := p.buffer(PACTypeKDCChecksum); !ok {
		return nil, ErrMissingKDCSignature
	}

	usage := v.KeyUsage
	if usage == 0 {
		usage = PACKeyUsage
	}
	checksum := v.Checksum
	if checksum == nil {
		checksum = DefaultChecksum
	}

	if err := p.verifyServerSignature(v.ServerKey, usage, checksum); err != nil {
		return nil, err
	}
	if err := p.verifyKDCSignature(v.KDCKey, usage, checksum); err != nil {
		return nil, err
	}
	return p.LogonInfo()
}

// VerifyServerSignature verifies only the service signature. Callers handling
// an AP-REQ should normally use Accept, which also requires and verifies the
// KDC signature.
func (p *PAC) VerifyServerSignature(key []byte, usage uint32) error {
	if usage == 0 {
		usage = PACKeyUsage
	}
	return p.verifyServerSignature(key, usage, DefaultChecksum)
}

// VerifyKDCSignature verifies only the KDC signature. The KDC signature is a
// keyed hash of the complete server-checksum buffer, including the server
// signature; this binds the KDC's approval to the service signature.
func (p *PAC) VerifyKDCSignature(key []byte, usage uint32) error {
	if usage == 0 {
		usage = PACKeyUsage
	}
	return p.verifyKDCSignature(key, usage, DefaultChecksum)
}

func (p *PAC) verifyServerSignature(key []byte, usage uint32, checksum ChecksumFunc) error {
	server, ok := p.buffer(PACTypeServerChecksum)
	if !ok {
		return fmt.Errorf("%w: server checksum", ErrMissingPACBuffer)
	}
	sigType, sig, err := signatureFields(server.data)
	if err != nil {
		return err
	}

	// [MS-PAC] 4: the server signature covers the entire PAC with the
	// Signature fields of both PAC_SIGNATURE_DATA structures zeroed.
	signed := make([]byte, len(p.raw))
	copy(signed, p.raw)
	p.zeroSignatureFields(signed, PACTypeServerChecksum)
	p.zeroSignatureFields(signed, PACTypeKDCChecksum)

	want, err := checksum(sigType, key, signed, usage)
	if err != nil {
		return err
	}
	if !equalBytes(sig, want) {
		return fmt.Errorf("%w: server", ErrInvalidPACSignature)
	}
	p.serverVerified = true
	return nil
}

func (p *PAC) verifyKDCSignature(key []byte, usage uint32, checksum ChecksumFunc) error {
	kdc, ok := p.buffer(PACTypeKDCChecksum)
	if !ok {
		return ErrMissingKDCSignature
	}
	server, ok := p.buffer(PACTypeServerChecksum)
	if !ok {
		return fmt.Errorf("%w: server checksum", ErrMissingPACBuffer)
	}
	sigType, sig, err := signatureFields(kdc.data)
	if err != nil {
		return err
	}

	// The KDC signature covers the server signature. Implementations differ
	// on whether they hash only the signature value, the complete
	// PAC_SIGNATURE_DATA buffer, or (as the extended/full-PAC checksum) the
	// whole PAC with both Signature fields zeroed. All three are KDC-keyed
	// commitments, so accept any of them.
	signed := make([]byte, len(p.raw))
	copy(signed, p.raw)
	p.zeroSignatureFields(signed, PACTypeServerChecksum)
	p.zeroSignatureFields(signed, PACTypeKDCChecksum)
	inputs := [][]byte{
		server.data[4:], // server Signature value (classic KDC checksum)
		server.data,     // complete PAC_SIGNATURE_DATA buffer
		signed,          // whole PAC with both signatures zeroed
	}
	for _, input := range inputs {
		want, err := checksum(sigType, key, input, usage)
		if err != nil {
			return err
		}
		if equalBytes(sig, want) {
			p.kdcVerified = true
			return nil
		}
	}
	return fmt.Errorf("%w: KDC", ErrInvalidPACSignature)
}

func (p *PAC) zeroSignatureFields(dst []byte, typ PACType) {
	b, ok := p.buffer(typ)
	if !ok {
		return
	}
	const sigTypeSize = 4
	if b.Size <= sigTypeSize {
		return
	}
	start := b.Offset + sigTypeSize
	end := b.Offset + uint64(b.Size)
	for i := start; i < end; i++ {
		dst[i] = 0
	}
}

func signatureFields(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, fmt.Errorf("%w: short signature buffer", ErrMalformedPAC)
	}
	sigType := binary.LittleEndian.Uint32(data[:4])
	sig := data[4:]
	if len(sig) == 0 {
		return 0, nil, fmt.Errorf("%w: empty signature", ErrMalformedPAC)
	}
	return sigType, sig, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
