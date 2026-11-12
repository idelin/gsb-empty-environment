package netsec

import (
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
)

// normalizeHost turns a hostname into the exact form used for netrc lookups.
// Ports and a trailing DNS root dot are ignored, and non-ASCII labels are
// encoded as punycode.
func normalizeHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.NewReplacer(
		"\u3002", ".",
		"\uff0e", ".",
		"\uff61", ".",
	).Replace(raw)

	raw = strings.TrimRight(raw, ".")
	raw = stripHostPort(raw)
	raw = strings.TrimRight(raw, ".")
	raw = strings.ToLower(raw)

	if raw == "" {
		return "", fmt.Errorf("netsec: empty hostname")
	}

	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = raw[1 : len(raw)-1]
	}
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String(), nil
	}

	labels := strings.Split(raw, ".")
	for i, label := range labels {
		if label == "" {
			return "", fmt.Errorf("netsec: invalid hostname %q", raw)
		}
		if isASCII(label) {
			labels[i] = strings.ToLower(label)
			continue
		}

		encoded, err := punycodeEncode(label)
		if err != nil {
			return "", fmt.Errorf("netsec: encode hostname %q: %w", raw, err)
		}
		labels[i] = "xn--" + encoded
	}

	return strings.Join(labels, "."), nil
}

func stripHostPort(host string) string {
	host = strings.TrimSpace(host)

	if h, port, err := net.SplitHostPort(host); err == nil && isDecimal(port) {
		return h
	}

	// netrc files occasionally include a port even though the protocol
	// ignores it. A single numeric suffix is safe to remove; IPv6 literals
	// contain more than one colon and are left untouched.
	if strings.Count(host, ":") == 1 {
		if i := strings.LastIndexByte(host, ':'); i > 0 {
			if port := host[i+1:]; isDecimal(port) {
				return host[:i]
			}
		}
	}

	return host
}

func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// punycodeEncode implements RFC 3492 encoding for one hostname label.
func punycodeEncode(label string) (string, error) {
	runes := []rune(label)
	var out strings.Builder

	basic := make([]rune, 0, len(runes))
	for _, r := range runes {
		if r < 0x80 {
			basic = append(basic, r)
		}
	}
	for _, r := range basic {
		out.WriteRune(r)
	}
	if len(basic) > 0 && len(basic) < len(runes) {
		out.WriteByte('-')
	}

	n := punyInitialN
	delta := 0
	bias := punyInitialBias

	for h := len(basic); h < len(runes); {
		m := int(^uint(0) >> 1)
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		if m == int(^uint(0)>>1) {
			return "", fmt.Errorf("no code point to encode")
		}

		delta += (m - n) * (h + 1)
		n = m

		for _, r := range runes {
			code := int(r)
			if code < n {
				delta++
			}
			if code != n {
				continue
			}

			q := delta
			for k := punyBase; ; k += punyBase {
				t := punyThreshold(k, bias)
				if q < t {
					break
				}
				out.WriteByte(punyDigit(t + (q-t)%(punyBase-t)))
				q = (q - t) / (punyBase - t)
			}
			out.WriteByte(punyDigit(q))

			bias = punyAdapt(delta, h+1, h == len(basic))
			delta = 0
			h++
		}

		delta++
		n++
	}

	return out.String(), nil
}

func punyThreshold(k, bias int) int {
	t := k - bias
	if t < punyTMin {
		return punyTMin
	}
	if t > punyTMax {
		return punyTMax
	}
	return t
}

func punyAdapt(delta, numPoints int, first bool) int {
	if first {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints

	k := 0
	for delta > (punyBase-punyTMin)*punyTMax/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}

func punyDigit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}
