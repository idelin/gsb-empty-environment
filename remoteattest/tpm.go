package remoteattest

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// TPM 2.0 algorithm and structure identifiers used by the verifier.
const (
	// MagicTCG is the 4-byte TPM_GENERATED marker at the start of every
	// TPMS_ATTEST blob (0xFF "TCG").
	MagicTCG uint32 = 0xFF544347
	// TPMSTAttestQuote is TPM_ST_ATTEST_QUOTE, the attestation type of a
	// PCR Quote.
	TPMSTAttestQuote uint16 = 0x8018
	// TPMAlgSHA256 is the TPM2_ALG_SHA256 algorithm identifier.
	TPMAlgSHA256 uint16 = 0x000B
	// TPMAlgECDSA is the TPM2_ALG_ECDSA signature scheme identifier.
	TPMAlgECDSA uint16 = 0x0010

	// p256CoordinateSize is the fixed R/S coordinate size of an ECDSA-P256
	// signature and the SHA-256 PCR digest size.
	p256CoordinateSize = 32
	// compressedPointSize is the SEC1 compressed P-256 point length.
	compressedPointSize = 33
)

var (
	ErrMalformedAttestation = errors.New("remoteattest: malformed TPMS_ATTEST blob")
	ErrBadMagic             = errors.New("remoteattest: attestation magic mismatch")
	ErrNotQuote             = errors.New("remoteattest: attestation type is not TPM_ST_ATTEST_QUOTE")
	ErrNonceMismatch        = errors.New("remoteattest: extraData does not match expected nonce")
	ErrUnsupportedHash      = errors.New("remoteattest: only SHA-256 PCR bank is supported")
	ErrMalformedSignature   = errors.New("remoteattest: malformed ECDSA-P256 signature blob")
	ErrBadSignature         = errors.New("remoteattest: AK signature verification failed")
	ErrAKNotTrusted         = errors.New("remoteattest: AK certificate is not trusted")
	ErrAKBinding            = errors.New("remoteattest: qualifiedSigner does not bind to AK public key")
)

// byteReader is a bounds-checked big-endian cursor over a TPM wire buffer.
type byteReader struct {
	buf []byte
	pos int
}

func newByteReader(buf []byte) *byteReader {
	return &byteReader{buf: buf}
}

func (r *byteReader) remaining() int { return len(r.buf) - r.pos }

func (r *byteReader) bytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.buf) {
		return nil, fmt.Errorf("%w: need %d bytes at offset %d, have %d",
			ErrMalformedAttestation, n, r.pos, r.remaining())
	}
	out := r.buf[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *byteReader) u8() (byte, error) {
	b, err := r.bytes(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *byteReader) u16() (uint16, error) {
	b, err := r.bytes(2)
	if err != nil {
		return 0, err
	}
	return uint16(b[0])<<8 | uint16(b[1]), nil
}

func (r *byteReader) u32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), nil
}

func (r *byteReader) u64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v, nil
}

// tpm2b reads a TPM2B-sized octet string: 2-byte big-endian length then data.
func (r *byteReader) tpm2b() ([]byte, error) {
	n, err := r.u16()
	if err != nil {
		return nil, err
	}
	return r.bytes(int(n))
}

// ClockInfo mirrors TPMS_CLOCK_INFO.
type ClockInfo struct {
	Clock        uint64
	ResetCount   uint32
	RestartCount uint32
	Safe         byte
}

// PCRSelection mirrors TPMS_PCR_SELECTION. Bitmap bit i maps to PCR i.
type PCRSelection struct {
	HashAlg uint16
	Bitmap  []byte
}

// Contains reports whether PCR index pcr is selected.
func (s PCRSelection) Contains(pcr uint32) bool {
	byteIdx := int(pcr / 8)
	if byteIdx >= len(s.Bitmap) {
		return false
	}
	return s.Bitmap[byteIdx]&(1<<(pcr%8)) != 0
}

// Indices returns the selected PCR indices in ascending order.
func (s PCRSelection) Indices() []uint32 {
	var out []uint32
	for i, b := range s.Bitmap {
		for bit := 0; bit < 8; bit++ {
			if b&(1<<bit) != 0 {
				out = append(out, uint32(i*8+bit))
			}
		}
	}
	return out
}

// Attestation is the decoded TPMS_ATTEST of a TPM_ST_ATTEST_QUOTE.
type Attestation struct {
	Magic           uint32
	Type            uint16
	QualifiedSigner []byte
	ExtraData       []byte
	ClockInfo       ClockInfo
	FirmwareVersion uint64
	PCRSelection    PCRSelection
	PCRDigest       []byte
}

// SelectedPCRs returns the PCR indices covered by pcrSelect, ascending.
func (a *Attestation) SelectedPCRs() []uint32 {
	return a.PCRSelection.Indices()
}

// DecodeAttestation parses a complete TPMS_ATTEST quote blob from its raw
// big-endian wire bytes. Trailing bytes are rejected.
func DecodeAttestation(raw []byte) (*Attestation, error) {
	r := newByteReader(raw)

	magic, err := r.u32()
	if err != nil {
		return nil, err
	}
	if magic != MagicTCG {
		return nil, fmt.Errorf("%w: got 0x%08X want 0x%08X", ErrBadMagic, magic, MagicTCG)
	}

	typ, err := r.u16()
	if err != nil {
		return nil, err
	}
	if typ != TPMSTAttestQuote {
		return nil, fmt.Errorf("%w: got 0x%04X", ErrNotQuote, typ)
	}

	qualifiedSigner, err := r.tpm2b()
	if err != nil {
		return nil, err
	}
	extraData, err := r.tpm2b()
	if err != nil {
		return nil, err
	}

	clock, err := r.u64()
	if err != nil {
		return nil, err
	}
	resetCount, err := r.u32()
	if err != nil {
		return nil, err
	}
	restartCount, err := r.u32()
	if err != nil {
		return nil, err
	}
	safe, err := r.u8()
	if err != nil {
		return nil, err
	}
	firmware, err := r.u64()
	if err != nil {
		return nil, err
	}

	selection, err := decodePCRSelection(r)
	if err != nil {
		return nil, err
	}

	pcrDigest, err := r.tpm2b()
	if err != nil {
		return nil, err
	}

	if r.remaining() != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrMalformedAttestation, r.remaining())
	}

	if selection.HashAlg != TPMAlgSHA256 {
		return nil, fmt.Errorf("%w: bank 0x%04X", ErrUnsupportedHash, selection.HashAlg)
	}
	if len(pcrDigest) != sha256.Size {
		return nil, fmt.Errorf("%w: pcrDigest is %d bytes, want %d",
			ErrMalformedAttestation, len(pcrDigest), sha256.Size)
	}

	return &Attestation{
		Magic:           magic,
		Type:            typ,
		QualifiedSigner: qualifiedSigner,
		ExtraData:       extraData,
		ClockInfo: ClockInfo{
			Clock:        clock,
			ResetCount:   resetCount,
			RestartCount: restartCount,
			Safe:         safe,
		},
		FirmwareVersion: firmware,
		PCRSelection:    selection,
		PCRDigest:       pcrDigest,
	}, nil
}

func decodePCRSelection(r *byteReader) (PCRSelection, error) {
	hashAlg, err := r.u16()
	if err != nil {
		return PCRSelection{}, err
	}
	size, err := r.u8()
	if err != nil {
		return PCRSelection{}, err
	}
	bitmap, err := r.bytes(int(size))
	if err != nil {
		return PCRSelection{}, err
	}
	return PCRSelection{HashAlg: hashAlg, Bitmap: append([]byte(nil), bitmap...)}, nil
}
