package srh

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Sign appends an HMAC TLV to the SRH in packet and returns a new packet.
// It is the egress-side counterpart of Admit: the SRH HMAC flag is set, the
// Hdr Ext Len is adjusted, and HMAC-SHA-256 is computed over the input
// documented on the package. The input packet is not modified. Signing an
// SRH that already carries an HMAC TLV fails.
func Sign(packet []byte, keyID uint32, key []byte) ([]byte, error) {
	if keyID == 0 {
		return nil, ErrZeroKeyID
	}
	if len(key) == 0 {
		return nil, errors.New("srh: empty HMAC key")
	}
	src, off, length, err := locateSRH(packet)
	if err != nil {
		return nil, err
	}
	oldSRH := packet[off : off+length]
	srh, err := parseSRH(oldSRH)
	if err != nil {
		return nil, err
	}
	if srh.hmac != nil {
		return nil, errors.New("srh: SRH already carries an HMAC TLV")
	}

	newLen := length + hmacTLVTotalLen
	if (newLen-srhFixedHeaderLen)%8 != 0 {
		return nil, fmt.Errorf("%w: new SRH length %d is not 8-octet aligned", ErrMalformed, newLen)
	}

	signed := make([]byte, len(packet)+hmacTLVTotalLen)
	copy(signed, packet[:off])
	newSRH := signed[off : off+newLen]
	copy(newSRH, oldSRH)
	copy(signed[off+newLen:], packet[off+length:])

	// Set the HMAC flag and account for the appended TLV in Hdr Ext Len,
	// which counts 8-octet units beyond the first 8 octets.
	newSRH[1] += byte(hmacTLVTotalLen / 8)
	newSRH[5] |= hmacFlagBit

	tlv := newSRH[length:newLen]
	tlv[0] = tlvTypeHMAC
	tlv[1] = hmacTLVValueLen
	tlv[2] = 0
	tlv[3] = 0
	binary.BigEndian.PutUint32(tlv[4:8], keyID)

	digest := computeMAC(src, newSRH[4], newSRH[5], keyID, srh.segmentList, key)
	copy(tlv[8:8+hmacSHA256Len], digest)
	return signed, nil
}
