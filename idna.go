package netsec

// Host canonicalization for connection identity binding.
//
// The implementation is intentionally self-contained (only the standard
// library) and implements the subset of IDNA needed to compare HTTP
// authorities against TLS server name indications and certificate SANs:
// lower-casing, trailing-dot removal and RFC 3492 Punycode (ToASCII)
// conversion of non-ASCII labels. Full IDNA2008 mapping/NFC tables are out of
// scope; every label is simple case-mapped with unicode.ToLower before being
// encoded, which covers the host-name forms used by HTTP virtual hosts.

import (
	"errors"
	"net"
	"strings"
	"unicode"
)

const acePrefix = "xn--"

var (
	errEmptyHost   = errors.New("netsec: empty host")
	errInvalidHost = errors.New("netsec: invalid host")
)

// canonicalHost returns the comparison form of a host:
//   - a single trailing dot (the DNS root) is removed;
//   - ASCII labels are lower-cased;
//   - non-ASCII labels are case-mapped and Punycode-encoded (ToASCII);
//   - IP literals are returned in their canonical net.IP string form.
//
// Both the TLS SNI and the request authority are reduced with this function so
// that comparisons are always performed against lower-case Punycode without a
// trailing dot.
func canonicalHost(host string) (string, error) {
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", errEmptyHost
	}

	// IP literal (with an optional RFC 4007 zone, which never participates in
	// identity comparison).
	if ip := net.ParseIP(zoneLess(host)); ip != nil {
		return strings.ToLower(ip.String()), nil
	}

	labels := strings.Split(host, ".")
	out := make([]string, len(labels))
	for i, label := range labels {
		if label == "" {
			return "", errInvalidHost
		}
		ascii, err := labelToASCII(label)
		if err != nil {
			return "", err
		}
		if len(ascii) > 63 {
			return "", errInvalidHost
		}
		out[i] = ascii
	}

	joined := strings.Join(out, ".")
	if len(joined) > 253 {
		return "", errInvalidHost
	}
	return joined, nil
}

// hostOnly strips an optional port (handling bracketed IPv6 literals).
func hostOnly(authority string) string {
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return host
	}
	if len(authority) >= 2 && authority[0] == '[' && authority[len(authority)-1] == ']' {
		return authority[1 : len(authority)-1]
	}
	return authority
}

func zoneLess(host string) string {
	if i := strings.IndexByte(host, '%'); i >= 0 {
		return host[:i]
	}
	return host
}

// labelToASCII implements a focused ToASCII for a single DNS label.
func labelToASCII(label string) (string, error) {
	lower := strings.ToLower(label)

	// An existing A-label is accepted only when its Punycode body decodes, so
	// that garbled "xn--" inputs fail closed instead of being compared.
	if strings.HasPrefix(lower, acePrefix) {
		if _, err := punyDecode(lower[len(acePrefix):]); err != nil {
			return "", errInvalidHost
		}
		return lower, nil
	}

	if isASCII(label) {
		return lower, nil
	}

	var mapped strings.Builder
	mapped.Grow(len(label))
	for _, r := range label {
		mapped.WriteRune(unicode.ToLower(r))
	}
	return acePrefix + punyEncode([]rune(mapped.String())), nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// --- RFC 3492 Punycode -----------------------------------------------------

const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
	punyDelimiter   = '-'
)

func punyAdapt(delta, numPoints int, firstTime bool) int {
	if firstTime {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	const threshold = ((punyBase - punyTMin) * punyTMax) / 2
	for delta > threshold {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + ((punyBase-punyTMin+1)*delta)/(delta+punySkew)
}

func punyDigit(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r-'0') + 26, true
	case r >= 'a' && r <= 'z':
		return int(r - 'a'), true
	case r >= 'A' && r <= 'Z':
		return int(r - 'A'), true
	}
	return 0, false
}

func punyEncodeDigit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + (d - 26))
}

// punyEncode encodes a Unicode code-point sequence into Punycode (without the
// "xn--" ACE prefix), following the RFC 3492 Bootstring reference algorithm.
func punyEncode(input []rune) string {
	var out strings.Builder

	// Emit the basic code points first, followed by the delimiter. The
	// delimiter is always emitted when basic code points exist, even if there
	// are no extended code points.
	basic := 0
	for _, r := range input {
		if r < punyInitialN {
			out.WriteRune(r)
			basic++
		}
	}
	if basic > 0 {
		out.WriteByte(punyDelimiter)
	}

	n := punyInitialN
	delta := 0
	bias := punyInitialBias
	handled := basic

	for handled < len(input) {
		// Find the smallest code point >= n.
		m := -1
		for _, r := range input {
			if int(r) >= n && (m == -1 || int(r) < m) {
				m = int(r)
			}
		}
		delta += (m - n) * (handled + 1)
		n = m
		for _, r := range input {
			if int(r) < n {
				delta++
				continue
			}
			if int(r) > n {
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
				out.WriteByte(punyEncodeDigit(t + (q-t)%(punyBase-t)))
				q = (q - t) / (punyBase - t)
			}
			out.WriteByte(punyEncodeDigit(q))

			bias = punyAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}

	return out.String()
}

// punyDecode decodes a Punycode body (without the "xn--" prefix), following
// the RFC 3492 section 6.2 reference algorithm.
func punyDecode(input string) ([]rune, error) {
	if input == "" {
		return nil, nil
	}

	// "pos" points just past the last delimiter (or at the start when there is
	// none). A leading delimiter with no basic code points is invalid.
	pos := 1 + strings.LastIndexByte(input, punyDelimiter)
	if pos == 1 {
		return nil, errInvalidHost
	}

	output := make([]rune, 0, len(input))
	if pos != 0 {
		for i := 0; i < pos-1; i++ {
			if input[i] >= punyInitialN {
				return nil, errInvalidHost
			}
			output = append(output, rune(input[i]))
		}
	}

	// A trailing delimiter with no extended portion means the whole label was
	// basic code points.
	if pos == len(input) {
		return output, nil
	}

	n := punyInitialN
	bias := punyInitialBias
	i := 0

	for pos < len(input) {
		oldI := i
		w := 1
		for k := punyBase; ; k += punyBase {
			if pos >= len(input) {
				return nil, errInvalidHost
			}
			digit, ok := punyDigit(rune(input[pos]))
			pos++
			if !ok {
				return nil, errInvalidHost
			}
			if digit > (maxInt-i)/w {
				return nil, errInvalidHost
			}
			i += digit * w

			t := k - bias
			if k <= bias {
				t = punyTMin
			} else if k >= bias+punyTMax {
				t = punyTMax
			}
			if digit < t {
				break
			}
			if w > maxInt/(punyBase-t) {
				return nil, errInvalidHost
			}
			w *= punyBase - t
		}

		numPoints := len(output) + 1
		bias = punyAdapt(i-oldI, numPoints, oldI == 0)
		if i/numPoints > maxInt-n {
			return nil, errInvalidHost
		}
		n += i / numPoints
		i %= numPoints
		if n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
			return nil, errInvalidHost
		}
		output = append(output, 0)
		copy(output[i+1:], output[i:])
		output[i] = rune(n)

		i++
	}

	return output, nil
}

const maxInt = int(^uint(0) >> 1)
