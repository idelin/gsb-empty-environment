package srh

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
)

func verifySRH(src net.IP, raw []byte, keys *Keyring) error {
	srh, err := parseSRH(raw)
	if err != nil {
		return err
	}

	// Segments Left indexes the segment list; it may at most point one
	// position past the last entry (the "all segments processed" state).
	if int(srh.segmentsLeft) > int(srh.lastEntry)+1 {
		return fmt.Errorf("%w: SL=%d, Last Entry=%d",
			ErrSegmentsLeft, srh.segmentsLeft, srh.lastEntry)
	}

	if srh.hmac == nil {
		return ErrNoHMAC
	}
	if srh.hmac.keyID == 0 {
		return ErrZeroKeyID
	}
	key, ok := keys.lookup(srh.hmac.keyID)
	if !ok {
		return fmt.Errorf("%w: Key ID=%d", ErrUnknownKey, srh.hmac.keyID)
	}

	expected := computeMAC(src, srh.lastEntry, srh.flags, srh.hmac.keyID, srh.segmentList, key)
	if !hmac.Equal(expected, srh.hmac.mac) {
		return ErrBadMAC
	}
	return nil
}

// writeMACInput appends the authenticated octets described in the package
// comment to buf.
func writeMACInput(buf []byte, src net.IP, lastEntry, flags byte, keyID uint32, segmentList []byte) []byte {
	buf = append(buf, src.To16()...)
	buf = append(buf, lastEntry, flags)
	buf = append(buf, 0, 0) // 2-octet reserved field, always zero
	var keyIDBytes [4]byte
	binary.BigEndian.PutUint32(keyIDBytes[:], keyID)
	buf = append(buf, keyIDBytes[:]...)
	buf = append(buf, segmentList...)
	return buf
}

func computeMAC(src net.IP, lastEntry, flags byte, keyID uint32, segmentList, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	input := make([]byte, 0, net.IPv6len+1+1+2+4+len(segmentList))
	mac.Write(writeMACInput(input, src, lastEntry, flags, keyID, segmentList))
	return mac.Sum(nil)
}
