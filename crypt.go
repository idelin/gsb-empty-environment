package netsec

import (
	"encoding/hex"
	"strconv"
	"strings"
)

func validateCrypt(segment *Segment, index int) error {
	if len(segment.Params) < 1 {
		return segmentError(ErrInvalidParameters, index, "crypt requires a cipher specification")
	}

	cipherSpec := strings.ToLower(segment.Params[0])
	if isNullCipher(cipherSpec) {
		return segmentError(ErrInsecureCipher, index, "%q", segment.Params[0])
	}
	if len(segment.Params) < 5 {
		return segmentError(ErrInvalidParameters, index,
			"crypt requires cipher, key, IV offset, device, and device offset")
	}

	if _, err := parseUint(segment.Params[2]); err != nil {
		return segmentError(ErrInvalidParameters, index, "invalid crypt IV offset %q", segment.Params[2])
	}
	if _, err := parseUint(segment.Params[4]); err != nil {
		return segmentError(ErrInvalidParameters, index, "invalid crypt device offset %q", segment.Params[4])
	}

	key := segment.Params[1]
	if len(key)%2 != 0 {
		return segmentError(ErrInvalidKey, index, "hex key has an odd number of digits")
	}
	decoded := make([]byte, len(key)/2)
	if _, err := hex.Decode(decoded, []byte(key)); err != nil {
		return segmentError(ErrInvalidKey, index, "%v", err)
	}

	allowed, ok := cipherKeySizes(cipherSpec)
	if !ok {
		return segmentError(ErrUnsupportedCipher, index, "%q", segment.Params[0])
	}
	for _, size := range allowed {
		if len(decoded) == size {
			return nil
		}
	}
	return segmentError(ErrKeyLength, index,
		"%q uses a %d-byte key; allowed lengths are %s",
		segment.Params[0], len(decoded), keyLengthList(allowed))
}

func isNullCipher(spec string) bool {
	base := spec
	if dash := strings.IndexByte(base, '-'); dash >= 0 {
		base = base[:dash]
	}
	switch base {
	case "cipher_null", "cipher-null", "null":
		return true
	default:
		return strings.HasPrefix(spec, "cipher_null-") ||
			strings.HasPrefix(spec, "cipher-null-")
	}
}

func cipherKeySizes(spec string) ([]int, bool) {
	parts := strings.Split(spec, "-")
	cipher := parts[0]
	mode := ""
	if len(parts) > 1 {
		mode = parts[1]
	}

	switch cipher {
	case "aes":
		if mode == "xts" {
			return []int{32, 64}, true
		}
		return []int{16, 24, 32}, true
	case "aes128":
		return []int{16}, true
	case "aes192":
		return []int{24}, true
	case "aes256":
		return []int{32}, true
	case "sm4":
		if mode == "xts" {
			return []int{32, 64}, true
		}
		return []int{16}, true
	case "des":
		return []int{8}, true
	case "des3", "des3_ede", "3des":
		return []int{24}, true
	case "blowfish":
		return []int{16, 24, 32}, true
	case "twofish", "serpent", "cast6", "camellia", "anubis":
		return []int{16, 24, 32}, true
	case "cast5", "tea", "xtea":
		return []int{16}, true
	case "chacha20", "chacha12", "xchacha20", "xchacha12", "salsa20":
		return []int{32}, true
	default:
		return nil, false
	}
}

func keyLengthList(sizes []int) string {
	var builder strings.Builder
	for i, size := range sizes {
		if i > 0 {
			if i == len(sizes)-1 {
				builder.WriteString(", or ")
			} else {
				builder.WriteString(", ")
			}
		}
		builder.WriteString(strconv.Itoa(size))
		builder.WriteString(" bytes")
	}
	return builder.String()
}
