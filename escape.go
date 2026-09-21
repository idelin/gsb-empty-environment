package framemux

// Byte-layer constants. The frame delimiter (flag) and the escape
// introducer are fixed for the lifetime of a connection; only the set of
// bytes the encoder MUST escape varies with the encoding epoch.
const (
	flagByte = 0x7E
	escByte  = 0x7D
	escXor   = 0x20
)

// maxEpoch is the highest supported encoding epoch.
const maxEpoch = 1

func validEpoch(e uint8) bool { return e <= maxEpoch }

// escapeSets defines, per encoding epoch, which bytes the encoder must
// escape. Decoding is identical for every epoch (escByte followed by
// byte^escXor), so a receiver that has already switched epochs can still
// decode in-flight tail frames encoded with the previous epoch's smaller
// set. Epochs are additive hardening profiles (like PPP's ACCM): epoch 1
// additionally escapes bytes that some links corrupt.
var escapeSets = [maxEpoch + 1][256]bool{
	makeSet(),
	makeSet(0x00, 0x11, 0x13, 0xFF),
}

func makeSet(extra ...byte) (s [256]bool) {
	s[flagByte] = true
	s[escByte] = true
	for _, b := range extra {
		s[b] = true
	}
	return s
}

// appendEscaped escapes content using the epoch's escape set and wraps it
// in flag delimiters: FLAG escaped(content) FLAG.
func appendEscaped(dst, content []byte, epoch uint8) []byte {
	set := escapeSets[epoch]
	dst = append(dst, flagByte)
	for _, b := range content {
		if set[b] {
			dst = append(dst, escByte, b^escXor)
		} else {
			dst = append(dst, b)
		}
	}
	return append(dst, flagByte)
}
