package netsec

import "errors"

// ErrTruncatedNumber is returned when a full packet number does not fit the
// negotiated truncation width.
var ErrTruncatedNumber = errors.New("netsec: packet number does not fit truncation width")

// BuildHeader encodes a key phase bit and full packet number into a header of
// width+1 bytes. The first byte carries only the phase bit in WirePhaseBit;
// the truncated number occupies the following width bytes in network byte
// order and keeps every bit of its width.
func BuildHeader(width byte, phase byte, number uint64) ([]byte, error) {
	if width < Width1 || width > Width4 {
		return nil, ErrBadWidth
	}
	window := uint64(1) << (width * 8)
	if number >= window {
		return nil, ErrTruncatedNumber
	}

	header := make([]byte, int(width)+1)
	if phase != 0 {
		header[0] = WirePhaseBit
	}
	for i := byte(0); i < width; i++ {
		shift := uint((width - 1 - i) * 8)
		header[int(i)+1] = byte(number >> shift)
	}
	return header, nil
}

// ParseHeader decodes a width+1 byte header produced by BuildHeader, returning
// the phase bit (0 or WirePhaseBit) and the truncated packet number.
func ParseHeader(header []byte) (phase byte, truncated uint64) {
	phase = header[0] & WirePhaseBit
	truncated = 0
	for _, b := range header[1:] {
		truncated = (truncated << 8) | uint64(b)
	}
	return phase, truncated
}

// EncodeDatagram is a convenience wrapper that builds a header and appends the
// payload, giving the transport host a ready-to-send datagram.
func EncodeDatagram(width byte, phase byte, number uint64, payload []byte) ([]byte, error) {
	header, err := BuildHeader(width, phase, number)
	if err != nil {
		return nil, err
	}
	datagram := make([]byte, 0, len(header)+len(payload))
	datagram = append(datagram, header...)
	datagram = append(datagram, payload...)
	return datagram, nil
}
