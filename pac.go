// Package netsec implements validation of Kerberos Privilege Attribute
// Certificates (PAC) for service-side acceptors.
//
// A PAC arrives inside the authorization data of a Kerberos ticket and
// carries the user's group SIDs. Because a service trusts these SIDs for
// authorization decisions, the PAC must be verified before use:
//
//   - The buffer directory is parsed strictly by offset and length.
//     Duplicate buffer types and overlapping buffers are rejected.
//   - Both the server and the KDC checksum are recomputed over a copy of
//     the PAC in which the signature fields have been zeroed, and compared
//     against the transmitted values.
//   - A PAC without a KDC signature is rejected: its SIDs must never be
//     trusted, since only the KDC can vouch for the group membership.
//
// A GSS acceptor extracts the PAC bytes from the ticket's authorization
// data (AD-WIN2K-PAC) and calls VerifyPAC before reading any SID.
package netsec

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// PAC buffer types (MS-PAC 2.4).
const (
	BufferTypeLogonInfo      uint32 = 1
	BufferTypeServerChecksum uint32 = 6
	BufferTypeKDCChecksum    uint32 = 7
)

// Checksum types carried in PAC_SIGNATURE_DATA.SignatureType.
const (
	checksumHMACMD5        uint32 = 0xFFFFFF76 // KERB_CHECKSUM_HMAC_MD5 (-138)
	checksumHMACSHA1AES128 uint32 = 15         // HMAC_SHA1_96_AES128
	checksumHMACSHA1AES256 uint32 = 16         // HMAC_SHA1_96_AES256
)

const (
	pacVersion        = 0
	infoBufferLen     = 20 // ulType(4) + cbBufferSize(4) + Offset(8)
	pacHeaderLen      = 8  // cBuffers(4) + Version(4)
	pacBufferAlign    = 8
	maxBuffers        = 64
	rodcIdentifierLen = 2
)

var (
	ErrMalformedPAC           = errors.New("netsec: malformed PAC")
	ErrDuplicateBufferType    = errors.New("netsec: duplicate PAC buffer type")
	ErrOverlappingBuffers     = errors.New("netsec: overlapping PAC buffers")
	ErrMissingServerSignature = errors.New("netsec: PAC has no server signature")
	ErrMissingKDCSignature    = errors.New("netsec: PAC has no KDC signature; its SIDs must not be trusted")
	ErrBadSignature           = errors.New("netsec: PAC signature verification failed")
	ErrUnsupportedChecksum    = errors.New("netsec: unsupported PAC checksum type")
)

// PAC is a verified Privilege Attribute Certificate.
type PAC struct {
	buffers   []parsedBuffer
	logonInfo []byte
}

type parsedBuffer struct {
	typ    uint32
	offset int // absolute offset of the buffer within the PAC
	data   []byte
}

// Buffer returns a copy of the raw contents of the PAC buffer of the
// given type.
func (p *PAC) Buffer(typ uint32) ([]byte, bool) {
	for _, b := range p.buffers {
		if b.typ == typ {
			out := make([]byte, len(b.data))
			copy(out, b.data)
			return out, true
		}
	}
	return nil, false
}

// LogonInfo returns the PAC_LOGON_INFO buffer containing the user's
// group SIDs. It is only available after successful verification,
// including the KDC signature.
func (p *PAC) LogonInfo() []byte {
	out := make([]byte, len(p.logonInfo))
	copy(out, p.logonInfo)
	return out
}

// VerifyPAC parses and cryptographically verifies a PAC. serverKey and
// kdcKey are the long-term keys of the service and the KDC respectively.
// It fails if the buffer directory is malformed, if either signature is
// missing or invalid, or if a buffer type appears twice. On success the
// returned PAC's SIDs may be trusted for authorization.
func VerifyPAC(pacData, serverKey, kdcKey []byte) (*PAC, error) {
	buffers, err := parsePAC(pacData)
	if err != nil {
		return nil, err
	}

	serverBuf := findBuffer(buffers, BufferTypeServerChecksum)
	if serverBuf == nil {
		return nil, ErrMissingServerSignature
	}
	kdcBuf := findBuffer(buffers, BufferTypeKDCChecksum)
	if kdcBuf == nil {
		return nil, ErrMissingKDCSignature
	}

	serverSig, err := parseSignature(*serverBuf)
	if err != nil {
		return nil, err
	}
	kdcSig, err := parseSignature(*kdcBuf)
	if err != nil {
		return nil, err
	}

	// Both checksums are computed over the PAC with the signature
	// fields of the server and KDC checksum buffers zeroed.
	zeroed := make([]byte, len(pacData))
	copy(zeroed, pacData)
	zeroField(zeroed, serverSig)
	zeroField(zeroed, kdcSig)

	serverSum, err := computeChecksum(serverSig.sigType, serverKey, zeroed)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(serverSum, serverSig.signature) {
		return nil, fmt.Errorf("%w: server checksum mismatch", ErrBadSignature)
	}

	kdcSum, err := computeChecksum(kdcSig.sigType, kdcKey, zeroed)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(kdcSum, kdcSig.signature) {
		return nil, fmt.Errorf("%w: KDC checksum mismatch", ErrBadSignature)
	}

	pac := &PAC{buffers: buffers}
	if li := findBuffer(buffers, BufferTypeLogonInfo); li != nil {
		pac.logonInfo = li.data
	}
	return pac, nil
}

// parsePAC strictly parses the PACTYPE header and buffer directory.
// Every buffer is validated by offset and length: offsets must be
// 8-byte aligned, lie within the buffer, and buffers must neither
// repeat a type nor overlap each other.
func parsePAC(data []byte) ([]parsedBuffer, error) {
	if len(data) < pacHeaderLen {
		return nil, fmt.Errorf("%w: shorter than PAC header", ErrMalformedPAC)
	}
	cBuffers := binary.LittleEndian.Uint32(data[0:4])
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != pacVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedPAC, version)
	}
	if cBuffers == 0 || cBuffers > maxBuffers {
		return nil, fmt.Errorf("%w: implausible buffer count %d", ErrMalformedPAC, cBuffers)
	}
	headerLen := uint64(pacHeaderLen) + uint64(cBuffers)*infoBufferLen
	if uint64(len(data)) < headerLen {
		return nil, fmt.Errorf("%w: truncated buffer directory", ErrMalformedPAC)
	}

	buffers := make([]parsedBuffer, 0, cBuffers)
	seen := make(map[uint32]bool, cBuffers)
	for i := uint32(0); i < cBuffers; i++ {
		entry := data[pacHeaderLen+int(i)*infoBufferLen:]
		typ := binary.LittleEndian.Uint32(entry[0:4])
		size := binary.LittleEndian.Uint32(entry[4:8])
		offset := binary.LittleEndian.Uint64(entry[8:16])

		if seen[typ] {
			return nil, fmt.Errorf("%w: type %d", ErrDuplicateBufferType, typ)
		}
		seen[typ] = true

		if size == 0 {
			return nil, fmt.Errorf("%w: empty buffer type %d", ErrMalformedPAC, typ)
		}
		if offset%pacBufferAlign != 0 {
			return nil, fmt.Errorf("%w: buffer type %d offset %d not %d-byte aligned", ErrMalformedPAC, typ, offset, pacBufferAlign)
		}
		if offset < headerLen {
			return nil, fmt.Errorf("%w: buffer type %d overlaps directory", ErrMalformedPAC, typ)
		}
		end := offset + uint64(size)
		if end > uint64(len(data)) {
			return nil, fmt.Errorf("%w: buffer type %d [%d:%d] out of bounds", ErrMalformedPAC, typ, offset, end)
		}
		buffers = append(buffers, parsedBuffer{
			typ:    typ,
			offset: int(offset),
			data:   data[offset:end],
		})
	}

	sorted := make([]parsedBuffer, len(buffers))
	copy(sorted, buffers)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].offset < sorted[j].offset })
	for i := 1; i < len(sorted); i++ {
		prevEnd := sorted[i-1].offset + len(sorted[i-1].data)
		if sorted[i].offset < prevEnd {
			return nil, fmt.Errorf("%w: types %d and %d", ErrOverlappingBuffers, sorted[i-1].typ, sorted[i].typ)
		}
	}
	return buffers, nil
}

type parsedSignature struct {
	sigType   uint32
	sigOffset int // absolute offset of the Signature field within the PAC
	signature []byte
}

// parseSignature validates a PAC_SIGNATURE_DATA buffer. The buffer must
// hold exactly the SignatureType, the fixed-length Signature, and an
// optional RODCIdentifier.
func parseSignature(buf parsedBuffer) (parsedSignature, error) {
	if len(buf.data) < 4 {
		return parsedSignature{}, fmt.Errorf("%w: signature buffer type %d too short", ErrMalformedPAC, buf.typ)
	}
	sigType := binary.LittleEndian.Uint32(buf.data[0:4])
	sigLen, ok := signatureLength(sigType)
	if !ok {
		return parsedSignature{}, fmt.Errorf("%w: %d", ErrUnsupportedChecksum, sigType)
	}
	rest := len(buf.data) - 4 - sigLen
	if rest != 0 && rest != rodcIdentifierLen {
		return parsedSignature{}, fmt.Errorf("%w: signature buffer type %d has invalid length %d", ErrMalformedPAC, buf.typ, len(buf.data))
	}
	return parsedSignature{
		sigType:   sigType,
		sigOffset: buf.offset + 4,
		signature: buf.data[4 : 4+sigLen],
	}, nil
}

func signatureLength(sigType uint32) (int, bool) {
	switch sigType {
	case checksumHMACMD5:
		return md5.Size, true
	case checksumHMACSHA1AES128, checksumHMACSHA1AES256:
		return 12, true // HMAC_SHA1_96: truncated to 96 bits
	default:
		return 0, false
	}
}

func zeroField(pac []byte, sig parsedSignature) {
	for i := range sig.signature {
		pac[sig.sigOffset+i] = 0
	}
}

func computeChecksum(sigType uint32, key, data []byte) ([]byte, error) {
	switch sigType {
	case checksumHMACMD5:
		mac := hmac.New(md5.New, key)
		mac.Write(data)
		return mac.Sum(nil), nil
	case checksumHMACSHA1AES128, checksumHMACSHA1AES256:
		mac := hmac.New(sha1.New, key)
		mac.Write(data)
		return mac.Sum(nil)[:12], nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedChecksum, sigType)
	}
}

func findBuffer(buffers []parsedBuffer, typ uint32) *parsedBuffer {
	for i := range buffers {
		if buffers[i].typ == typ {
			return &buffers[i]
		}
	}
	return nil
}
