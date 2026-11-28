package esp

func buildESPPlaintext(payload []byte, nextHeader uint8, blockSize int) []byte {
	padLen := (blockSize - (len(payload)+2)%blockSize) % blockSize
	out := make([]byte, len(payload)+padLen+2)
	copy(out, payload)

	for i := 0; i < padLen; i++ {
		out[len(payload)+i] = byte(i + 1)
	}
	out[len(payload)+padLen] = byte(padLen)
	out[len(payload)+padLen+1] = nextHeader
	return out
}

// parseESPPlaintext validates the trailer without making the result depend on
// which padding byte was wrong. The packet has already been authenticated when
// this function is called, but the fixed-time loop also avoids an unnecessary
// second side channel.
func parseESPPlaintext(plaintext []byte) ([]byte, uint8, bool) {
	if len(plaintext) < 2 {
		return nil, 0, false
	}

	padLen := int(plaintext[len(plaintext)-2])
	bodyLen := len(plaintext) - 2 - padLen
	if bodyLen < 0 {
		return nil, 0, false
	}

	var bad int
	for i := 0; i < padLen; i++ {
		bad |= int(plaintext[bodyLen+i]) ^ (i + 1)
	}
	if bad != 0 {
		return nil, 0, false
	}

	return plaintext[:bodyLen], plaintext[len(plaintext)-1], true
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
