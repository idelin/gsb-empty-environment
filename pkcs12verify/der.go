package pkcs12verify

// Strict DER validation helpers. encoding/asn1 accepts some lenient encodings
// (notably non-minimal long-form lengths), so every blob is walked here before
// being handed to asn1.Unmarshal. This guarantees rejection of BER indefinite
// lengths, non-canonical lengths, constructed primitive TLVs and trailing
// garbage.

const (
	derTagBoolean         = 0x01
	derTagBitString       = 0x03
	derTagOctetString     = 0x04
	derTagNull            = 0x05
	derTagOID             = 0x06
	derTagSequence        = 0x10
	derTagSet             = 0x11
	derClassUniversal     = 0x00
	derClassContext       = 0x02
	derConstructedBit     = 0x20
	derLongFormTagBit     = 0x1f
	derIndefiniteLength   = 0x80
	derLongFormLengthMask = 0x7f
)

// checkStrictDER walks one complete DER TLV and returns any bytes remaining
// after it. A non-empty remainder means trailing garbage.
func checkStrictDER(b []byte) (rest []byte, err error) {
	rest, _, _, _, err = readStrictTLV(b)
	return rest, err
}

// requireSingleDER fails unless b contains exactly one strict DER TLV.
func requireSingleDER(b []byte) error {
	rest, err := checkStrictDER(b)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return ErrTrailingBytes
	}
	return nil
}

// readStrictTLV parses identifier octets, length octets and the content, then
// recursively validates every nested TLV. It returns the remainder, the tag
// (class bits + number), whether the TLV is constructed and the content.
func readStrictTLV(b []byte) (rest []byte, tag byte, constructed bool, content []byte, err error) {
	if len(b) < 2 {
		return nil, 0, false, nil, malformed("truncated DER identifier/length")
	}
	identifier := b[0]
	constructed = identifier&derConstructedBit != 0
	tag = identifier
	pos := 1

	if identifier&derLongFormTagBit == derLongFormTagBit {
		// Multi-byte (high tag number) form. Parse the base-128 tag number and
		// require the canonical minimal representation.
		var tagNumber uint
		first := true
		for {
			if pos >= len(b) {
				return nil, 0, false, nil, malformed("truncated DER tag number")
			}
			octet := b[pos]
			pos++
			if first && octet == 0x80 {
				return nil, 0, false, nil, nonCanonical("non-minimal high tag number")
			}
			first = false
			tagNumber = (tagNumber << 7) | uint(octet&0x7f)
			if octet&0x80 == 0 {
				break
			}
		}
		_ = tagNumber
	}

	if pos >= len(b) {
		return nil, 0, false, nil, malformed("truncated DER length")
	}
	lengthOctet := b[pos]
	pos++

	var contentLength int
	switch {
	case lengthOctet == derIndefiniteLength:
		return nil, 0, false, nil, nonCanonical("indefinite BER length is not DER")
	case lengthOctet < derIndefiniteLength:
		contentLength = int(lengthOctet)
	default:
		numBytes := int(lengthOctet & derLongFormLengthMask)
		if numBytes == 0 {
			return nil, 0, false, nil, nonCanonical("indefinite BER length is not DER")
		}
		if numBytes > 8 {
			return nil, 0, false, nil, malformed("DER length too large")
		}
		if pos+numBytes > len(b) {
			return nil, 0, false, nil, malformed("truncated DER long-form length")
		}
		if b[pos] == 0 {
			return nil, 0, false, nil, nonCanonical("non-minimal long-form length")
		}
		var n uint64
		for i := 0; i < numBytes; i++ {
			n = n<<8 | uint64(b[pos+i])
		}
		if n > uint64(len(b)) {
			return nil, 0, false, nil, malformed("DER length exceeds available data")
		}
		// Minimal-encoding rule: long form is only canonical for n >= 128, and
		// the number of length octets must be the smallest that fits n.
		if n < derIndefiniteLength {
			return nil, 0, false, nil, nonCanonical("long-form length used for value below 128")
		}
		if minimalLenBytes(n) != numBytes {
			return nil, 0, false, nil, nonCanonical("non-minimal long-form length")
		}
		contentLength = int(n)
		pos += numBytes
	}

	if pos+contentLength > len(b) {
		return nil, 0, false, nil, malformed("DER content shorter than declared length")
	}
	content = b[pos : pos+contentLength]
	rest = b[pos+contentLength:]

	if err := checkStrictContent(identifier, constructed, content); err != nil {
		return nil, 0, false, nil, err
	}
	return rest, tag, constructed, content, nil
}

// checkStrictContent enforces DER primitive/constructed rules and recursively
// validates nested TLVs.
func checkStrictContent(identifier byte, constructed bool, content []byte) error {
	class := identifier & 0xc0
	tagNumber := identifier & 0x1f

	if class == derClassUniversal {
		if tagNumber == derTagSequence || tagNumber == derTagSet {
			if !constructed {
				return nonCanonical("SEQUENCE/SET must be constructed")
			}
		} else {
			if constructed {
				return nonCanonical("primitive universal TLV must not be constructed")
			}
			if err := checkPrimitiveContent(tagNumber, content); err != nil {
				return err
			}
			return nil
		}
	}

	if !constructed {
		// Primitive context-specific or non-universal TLV: opaque octets.
		return nil
	}

	// Every constructed TLV (universal SEQUENCE/SET or context-specific
	// [n] EXPLICIT wrappers) must contain complete, strict child TLVs.
	remaining := content
	for len(remaining) > 0 {
		var err error
		remaining, _, _, _, err = readStrictTLV(remaining)
		if err != nil {
			return err
		}
	}
	return nil
}

// checkPrimitiveContent applies extra DER content rules to universal primitive
// tags that PKCS#12 structures can contain.
func checkPrimitiveContent(tagNumber byte, content []byte) error {
	switch tagNumber {
	case derTagBoolean:
		if len(content) != 1 || (content[0] != 0x00 && content[0] != 0xff) {
			return nonCanonical("non-canonical BOOLEAN value")
		}
	case derTagBitString:
		if len(content) == 0 {
			return malformed("empty BIT STRING")
		}
		if int(content[0]) >= len(content) {
			return malformed("BIT STRING unused-bits count exceeds length")
		}
	case derTagNull:
		if len(content) != 0 {
			return nonCanonical("NULL must have zero content")
		}
	case derTagOID, derTagOctetString, 0x02, 0x0c, 0x13, 0x14, 0x16, 0x17,
		0x18, 0x1e, 0x0d, 0x0e, 0x0f, 0x15, 0x12, 0x1a:
		// INTEGER, string types and opaque octets carry no extra DER constraint
		// beyond the TLV framing already validated.
	}
	return nil
}

func malformed(msg string) error {
	return wrap(ErrMalformed, msg)
}

func nonCanonical(msg string) error {
	return wrap(ErrNonCanonicalDER, msg)
}

// minimalLenBytes reports how many big-endian base-256 length octets are
// required to encode n in canonical DER long form (n >= 128).
func minimalLenBytes(n uint64) int {
	switch {
	case n < 1<<8:
		return 1
	case n < 1<<16:
		return 2
	case n < 1<<24:
		return 3
	case n < 1<<32:
		return 4
	case n < 1<<40:
		return 5
	case n < 1<<48:
		return 6
	case n < 1<<56:
		return 7
	default:
		return 8
	}
}
