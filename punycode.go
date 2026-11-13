package netsec

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	punyBase        int32 = 36
	punyDamp        int32 = 700
	punyInitialBias int32 = 72
	punyInitialN    int32 = 128
	punySkew        int32 = 38
	punyTMax        int32 = 26
	punyTMin        int32 = 1
	acePrefix             = "xn--"
)

// PunycodeError reports an invalid Punycode-encoded label.
type PunycodeError struct {
	Label string
}

func (e *PunycodeError) Error() string {
	return fmt.Sprintf("idna: invalid Punycode label %q", e.Label)
}

// PunycodeDecode decodes a Punycode label. It accepts either an A-label with
// the "xn--" prefix or the bare Punycode body.
func PunycodeDecode(encoded string) (string, error) {
	if strings.HasPrefix(strings.ToLower(encoded), acePrefix) {
		encoded = encoded[len(acePrefix):]
		if encoded == "" {
			return "", &PunycodeError{Label: acePrefix}
		}
	}
	return punycodeDecode(encoded)
}

// PunycodeEncode encodes a Unicode label, without adding the "xn--" prefix.
func PunycodeEncode(label string) (string, error) {
	return punycodeEncode("", label)
}

func punycodeDecode(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	pos := 1 + strings.LastIndex(encoded, "-")
	if pos == 1 {
		return "", &PunycodeError{Label: encoded}
	}
	if pos == len(encoded) {
		return encoded[:len(encoded)-1], nil
	}
	output := make([]rune, 0, len(encoded))
	if pos != 0 {
		for _, r := range encoded[:pos-1] {
			output = append(output, r)
		}
	}
	i, n, bias := int32(0), punyInitialN, punyInitialBias
	for pos < len(encoded) {
		oldI, w := i, int32(1)
		for k := punyBase; ; k += punyBase {
			if pos == len(encoded) {
				return "", &PunycodeError{Label: encoded}
			}
			digit, ok := decodeDigit(encoded[pos])
			if !ok {
				return "", &PunycodeError{Label: encoded}
			}
			pos++
			var overflow bool
			i, overflow = madd(i, digit, w)
			if overflow {
				return "", &PunycodeError{Label: encoded}
			}
			t := k - bias
			if k <= bias {
				t = punyTMin
			} else if k >= bias+punyTMax {
				t = punyTMax
			}
			if digit < t {
				break
			}
			w, overflow = madd(0, w, punyBase-t)
			if overflow {
				return "", &PunycodeError{Label: encoded}
			}
		}
		if len(output) >= 1024 {
			return "", &PunycodeError{Label: encoded}
		}
		x := int32(len(output) + 1)
		bias = adapt(i-oldI, x, oldI == 0)
		n += i / x
		i %= x
		if n < 0 || n > utf8.MaxRune {
			return "", &PunycodeError{Label: encoded}
		}
		output = append(output, 0)
		copy(output[i+1:], output[i:])
		output[i] = n
		i++
	}
	return string(output), nil
}

func punycodeEncode(prefix, s string) (string, error) {
	output := make([]byte, len(prefix), len(prefix)+1+2*len(s))
	copy(output, prefix)
	delta, n, bias := int32(0), punyInitialN, punyInitialBias
	b, remaining := int32(0), int32(0)
	for _, r := range s {
		if r < 0x80 {
			b++
			output = append(output, byte(r))
		} else {
			remaining++
		}
	}
	h := b
	if b > 0 {
		output = append(output, '-')
	}
	for remaining != 0 {
		m := int32(0x7fffffff)
		for _, r := range s {
			if m > r && r >= n {
				m = r
			}
		}
		var overflow bool
		delta, overflow = madd(delta, m-n, h+1)
		if overflow {
			return "", &PunycodeError{Label: s}
		}
		n = m
		for _, r := range s {
			if r < n {
				delta++
				if delta < 0 {
					return "", &PunycodeError{Label: s}
				}
				continue
			}
			if r > n {
				continue
			}
			q := delta
			for k := punyBase; ; k += punyBase {
				t := k - bias
				if k <= bias {
					t = punyTMin
				} else if k >= bias+punyTMax {
					t = punyTMax
				}
				if q < t {
					break
				}
				output = append(output, encodeDigit(t+(q-t)%(punyBase-t)))
				q = (q - t) / (punyBase - t)
			}
			output = append(output, encodeDigit(q))
			bias = adapt(delta, h+1, h == b)
			delta = 0
			h++
			remaining--
		}
		delta++
		n++
	}
	return string(output), nil
}

func madd(a, b, c int32) (next int32, overflow bool) {
	p := int64(b) * int64(c)
	if p > math.MaxInt32-int64(a) {
		return 0, true
	}
	return a + int32(p), false
}

func decodeDigit(x byte) (digit int32, ok bool) {
	switch {
	case '0' <= x && x <= '9':
		return int32(x - ('0' - 26)), true
	case 'A' <= x && x <= 'Z':
		return int32(x - 'A'), true
	case 'a' <= x && x <= 'z':
		return int32(x - 'a'), true
	}
	return 0, false
}

func encodeDigit(digit int32) byte {
	switch {
	case 0 <= digit && digit < 26:
		return byte(digit + 'a')
	case 26 <= digit && digit < 36:
		return byte(digit + ('0' - 26))
	}
	panic("idna: internal error in Punycode encoding")
}

func adapt(delta, numPoints int32, firstTime bool) int32 {
	if firstTime {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := int32(0)
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}
