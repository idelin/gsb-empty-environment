package netsec

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// decodeHexStrict decodes a non-empty, even-length run of hex digits.
func decodeHexStrict(src []byte) ([]byte, error) {
	if len(src) == 0 || len(src)%2 != 0 {
		return nil, errors.New("netsec: odd or empty hex input")
	}
	dst := make([]byte, hex.DecodedLen(len(src)))
	if _, err := hex.Decode(dst, src); err != nil {
		return nil, err
	}
	return dst, nil
}

// decodeBase64Strict decodes standard Base64, accepting either canonical
// padded input or canonical unpadded (raw) input. Non-canonical padding and
// non-zero trailing bits are rejected.
func decodeBase64Strict(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, errors.New("netsec: empty base64 input")
	}
	if len(src)%4 == 0 {
		if dst, err := decodeAll(base64.StdEncoding.Strict(), src); err == nil {
			return dst, nil
		}
	}
	if len(src)%4 != 1 {
		if dst, err := decodeAll(base64.RawStdEncoding.Strict(), src); err == nil {
			return dst, nil
		}
	}
	return nil, errors.New("netsec: invalid strict base64")
}

func decodeAll(enc *base64.Encoding, src []byte) ([]byte, error) {
	dst := make([]byte, enc.DecodedLen(len(src)))
	n, err := enc.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// hexByteMap maps each decoded byte to the absolute range of its two source
// characters in the parent content, given the parent offset start.
func hexByteMap(start, outLen int) []Range {
	m := make([]Range, outLen)
	for i := range m {
		m[i] = Range{start + 2*i, start + 2*i + 2}
	}
	return m
}

// base64ByteMap maps each decoded byte to the minimal absolute range of
// source characters it depends on, given the source length and the parent
// offset start. Padding characters are never included in any byte's range.
func base64ByteMap(srcLen, start, outLen int) []Range {
	m := make([]Range, outLen)
	si, oi := 0, 0
	for oi < outLen {
		nb := 3
		if rem := outLen - oi; rem < 3 {
			nb = rem
		}
		qlen := 4
		if nb < 3 {
			qlen = srcLen - si // final partial quantum, with or without padding
		}
		m[oi] = Range{start + si, start + si + 2}
		if nb >= 2 {
			m[oi+1] = Range{start + si + 1, start + si + 3}
		}
		if nb == 3 {
			m[oi+2] = Range{start + si + 2, start + si + 4}
		}
		si += qlen
		oi += nb
	}
	return m
}

func isHexChar(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func isB64Char(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '+' || c == '/'
}

func isHexSpan(b []byte) bool {
	if len(b) == 0 || len(b)%2 != 0 {
		return false
	}
	for _, c := range b {
		if !isHexChar(c) {
			return false
		}
	}
	return true
}

func isB64Span(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if !isB64Char(c) && c != '=' {
			return false
		}
	}
	return true
}
