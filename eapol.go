package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// EAPOL packet types and key descriptor constants used by the four-way
// handshake. The constants follow IEEE 802.11 / IEEE 802.1X naming.
const (
	EAPOLVersion1 = 1
	EAPOLVersion2 = 2

	EAPOLTypeKey = 3

	DescriptorTypeWPA = 1
	DescriptorTypeRSN = 2

	KeyDescriptorVersionHMACMD5RC4  = 1
	KeyDescriptorVersionHMACSHA1AES = 2
	KeyDescriptorVersionAES128CMAC  = 3

	KeyInfoVersionMask      uint16 = 0x0007
	KeyInfoKeyType          uint16 = 0x0008
	KeyInfoInstall          uint16 = 0x0040
	KeyInfoAck              uint16 = 0x0080
	KeyInfoMIC              uint16 = 0x0100
	KeyInfoSecure           uint16 = 0x0200
	KeyInfoHasIV            uint16 = 0x0400
	KeyInfoRequest          uint16 = 0x0800
	KeyInfoEncryptedKeyData uint16 = 0x1000
	KeyInfoSMK              uint16 = 0x2000
)

const (
	eapolHeaderLen = 4

	// EAPOLKeyHeaderLen is the fixed part of an EAPOL-Key frame, from the
	// EAPOL version byte through the two-byte Key Data Length field.
	EAPOLKeyHeaderLen = 91

	// EAPOLKeyMICOffset is the byte offset of the 16-byte Key MIC field.
	EAPOLKeyMICOffset = 73

	// EAPOLKeyDataOffset is the byte offset of Key Data.
	EAPOLKeyDataOffset = EAPOLKeyHeaderLen
)

// ErrInvalidEAPOLKeyFrame is returned when a frame is too short, malformed,
// or has an unsupported EAPOL/descriptor type.
var ErrInvalidEAPOLKeyFrame = errors.New("netsec: invalid EAPOL-Key frame")

// EAPOLKeyFrame is the decoded form of an EAPOL-Key packet.
//
// Raw retains the bytes exactly as received (including the original MIC) so
// that the MIC can be verified over the wire representation. KeyData is the
// bytes as carried on the wire; it is encrypted when KeyInfo has
// KeyInfoEncryptedKeyData set. The state machine never decrypts KeyData
// before the MIC has been verified.
type EAPOLKeyFrame struct {
	Version        uint8
	DescriptorType uint8
	KeyInfo        uint16
	KeyLength      uint16
	ReplayCounter  uint64
	Nonce          [32]byte
	IV             [16]byte
	RSC            [8]byte
	MIC            [16]byte
	KeyData        []byte

	// Raw is the complete EAPOL packet as received or marshaled.
	Raw []byte
}

// ParseEAPOLKey parses an EAPOL-Key frame. It accepts an EAPOL packet that
// starts with the EAPOL header (version, type, length) and does not perform
// any MIC verification or Key Data decryption.
func ParseEAPOLKey(frame []byte) (*EAPOLKeyFrame, error) {
	if len(frame) < eapolHeaderLen {
		return nil, fmt.Errorf("%w: truncated EAPOL header", ErrInvalidEAPOLKeyFrame)
	}
	if frame[1] != EAPOLTypeKey {
		return nil, fmt.Errorf("%w: EAPOL type %d is not EAPOL-Key", ErrInvalidEAPOLKeyFrame, frame[1])
	}

	bodyLen := int(binary.BigEndian.Uint16(frame[2:4]))
	if bodyLen < 87 {
		return nil, fmt.Errorf("%w: key descriptor body too short", ErrInvalidEAPOLKeyFrame)
	}
	total := eapolHeaderLen + bodyLen
	if total > len(frame) {
		return nil, fmt.Errorf("%w: EAPOL length %d exceeds frame length %d", ErrInvalidEAPOLKeyFrame, total, len(frame))
	}

	data := frame[:total]
	if data[4] != DescriptorTypeWPA && data[4] != DescriptorTypeRSN {
		return nil, fmt.Errorf("%w: unsupported descriptor type %d", ErrInvalidEAPOLKeyFrame, data[4])
	}

	keyInfo := binary.BigEndian.Uint16(data[5:7])
	keyDataLen := int(binary.BigEndian.Uint16(data[89:91]))
	keyDataOffset := EAPOLKeyHeaderLen
	if keyDataOffset+keyDataLen > len(data) {
		return nil, fmt.Errorf("%w: Key Data length %d exceeds frame length", ErrInvalidEAPOLKeyFrame, keyDataLen)
	}

	f := &EAPOLKeyFrame{
		Version:        data[0],
		DescriptorType: data[4],
		KeyInfo:        keyInfo,
		KeyLength:      binary.BigEndian.Uint16(data[7:9]),
		ReplayCounter:  binary.BigEndian.Uint64(data[9:17]),
		KeyData:        append([]byte(nil), data[keyDataOffset:keyDataOffset+keyDataLen]...),
		Raw:            append([]byte(nil), data...),
	}
	copy(f.Nonce[:], data[17:49])
	copy(f.IV[:], data[49:65])
	copy(f.RSC[:], data[65:73])
	copy(f.MIC[:], data[73:89])

	return f, nil
}

// Marshal encodes the frame into its wire representation. The MIC field is
// copied as-is; use Sign or ComputeEAPOLKeyMIC when a fresh MIC is needed.
func (f *EAPOLKeyFrame) Marshal() ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: nil frame", ErrInvalidEAPOLKeyFrame)
	}

	version := f.Version
	if version == 0 {
		version = EAPOLVersion2
	}
	descriptorType := f.DescriptorType
	if descriptorType == 0 {
		descriptorType = DescriptorTypeRSN
	}
	if descriptorType != DescriptorTypeWPA && descriptorType != DescriptorTypeRSN {
		return nil, fmt.Errorf("%w: unsupported descriptor type %d", ErrInvalidEAPOLKeyFrame, descriptorType)
	}
	if len(f.KeyData) > 0xffff {
		return nil, fmt.Errorf("%w: Key Data too large", ErrInvalidEAPOLKeyFrame)
	}

	bodyLen := 87 + len(f.KeyData)
	out := make([]byte, EAPOLKeyHeaderLen+len(f.KeyData))
	out[0] = version
	out[1] = EAPOLTypeKey
	binary.BigEndian.PutUint16(out[2:4], uint16(bodyLen))
	out[4] = descriptorType
	binary.BigEndian.PutUint16(out[5:7], f.KeyInfo)
	binary.BigEndian.PutUint16(out[7:9], f.KeyLength)
	binary.BigEndian.PutUint64(out[9:17], f.ReplayCounter)
	copy(out[17:49], f.Nonce[:])
	copy(out[49:65], f.IV[:])
	copy(out[65:73], f.RSC[:])
	copy(out[73:89], f.MIC[:])
	binary.BigEndian.PutUint16(out[89:91], uint16(len(f.KeyData)))
	copy(out[EAPOLKeyDataOffset:], f.KeyData)

	f.Raw = append([]byte(nil), out...)
	return out, nil
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (f *EAPOLKeyFrame) MarshalBinary() ([]byte, error) {
	return f.Marshal()
}

// Sign computes and stores the Key MIC using kck.
func (f *EAPOLKeyFrame) Sign(kck []byte) error {
	raw, err := f.Marshal()
	if err != nil {
		return err
	}
	mic, err := ComputeEAPOLKeyMIC(raw, kck)
	if err != nil {
		return err
	}
	copy(f.MIC[:], mic)
	_, err = f.Marshal()
	return err
}

// KeyDescriptorVersion returns the key descriptor version from KeyInfo.
func (f *EAPOLKeyFrame) KeyDescriptorVersion() int {
	if f == nil {
		return 0
	}
	return int(f.KeyInfo & KeyInfoVersionMask)
}

// IsPairwise reports whether the frame carries a pairwise key.
func (f *EAPOLKeyFrame) IsPairwise() bool {
	return f != nil && f.KeyInfo&KeyInfoKeyType != 0
}

// IsMessage3 reports whether the frame has the shape of a four-way
// handshake Message 3 (pairwise, ACK, MIC, and no request flag).
func (f *EAPOLKeyFrame) IsMessage3() bool {
	if f == nil {
		return false
	}
	return f.IsPairwise() &&
		f.KeyInfo&KeyInfoAck != 0 &&
		f.KeyInfo&KeyInfoMIC != 0 &&
		f.KeyInfo&KeyInfoRequest == 0
}

// IsMessage4 reports whether the frame has the shape of Message 4
// (pairwise, MIC, no ACK, no Install, and no Key Data).
func (f *EAPOLKeyFrame) IsMessage4() bool {
	if f == nil {
		return false
	}
	return f.IsPairwise() &&
		f.KeyInfo&KeyInfoMIC != 0 &&
		f.KeyInfo&KeyInfoAck == 0 &&
		f.KeyInfo&KeyInfoInstall == 0 &&
		len(f.KeyData) == 0
}
