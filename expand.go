package netsec

import "unicode/utf8"

// predefinedEntities are the five XML built-in general entities and their
// single-character expansions.
var predefinedEntities = map[string]string{
	"lt":   "<",
	"gt":   ">",
	"amp":  "&",
	"apos": "'",
	"quot": "\"",
}

// expandEntity fully resolves the raw literal value of the entity named
// owner. Every byte that ends up in an expansion, including each repeated
// occurrence of a referenced entity, is charged against the guard budget:
// this is what contains "billion laughs" style amplification. active is the
// current reference chain and rejects cycles regardless of declaration
// order.
func (s *scanner) expandEntity(owner string, raw []byte) (string, error) {
	active := map[string]bool{owner: true}
	return s.expandInto(raw, active)
}

// expandInto returns the fully expanded form of raw, charging each emitted
// byte. Declared entities are expanded once and memoized; every reference
// occurrence still pays for its bytes.
func (s *scanner) expandInto(raw []byte, active map[string]bool) (string, error) {
	var out []byte

	for i := 0; i < len(raw); {
		if raw[i] != '&' {
			if err := s.charge(1); err != nil {
				return "", err
			}
			out = append(out, raw[i])
			i++
			continue
		}

		// Character reference.
		if i+1 < len(raw) && raw[i+1] == '#' {
			runeVal, width, err := decodeCharRef(raw[i:])
			if err != nil {
				return "", s.fail(err)
			}
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], runeVal)
			if err := s.charge(n); err != nil {
				return "", err
			}
			out = append(out, buf[:n]...)
			i += width
			continue
		}

		// Named general entity reference.
		j := i + 1
		for j < len(raw) && isNameByte(raw[j]) {
			j++
		}
		if j == i+1 || j >= len(raw) || raw[j] != ';' {
			return "", s.fail(ErrMalformedDTD)
		}
		refName := string(raw[i+1 : j])

		if predefined, ok := predefinedEntities[refName]; ok {
			if err := s.charge(len(predefined)); err != nil {
				return "", err
			}
			out = append(out, predefined...)
			i = j + 1
			continue
		}

		info, ok := s.entities[refName]
		if !ok {
			// Optional caller-provided fallback for names the document did
			// not declare. Its output is charged like any other expansion.
			if s.guard.ResolveEntity == nil {
				return "", s.fail(ErrUndeclaredEntity)
			}
			value, found := s.guard.ResolveEntity(refName)
			if !found {
				return "", s.fail(ErrUndeclaredEntity)
			}
			if err := s.charge(len(value)); err != nil {
				return "", err
			}
			out = append(out, value...)
			i = j + 1
			continue
		}

		if active[refName] {
			return "", s.fail(ErrRecursiveEntity)
		}

		if info.expanded == "" {
			active[refName] = true
			nested, err := s.expandInto([]byte(info.raw), active)
			if err != nil {
				return "", err
			}
			delete(active, refName)
			info.expanded = nested
			s.entities[refName] = info
		}
		if err := s.charge(len(info.expanded)); err != nil {
			return "", err
		}
		out = append(out, info.expanded...)
		i = j + 1
	}

	return string(out), nil
}

// decodeCharRef decodes a numeric character reference such as "&#65;" or
// "&#x41;" starting at ref. It returns the rune, the byte width of the
// reference including the trailing semicolon, and ErrMalformedDTD for an
// out-of-range or otherwise illegal reference.
func decodeCharRef(ref []byte) (rune, int, error) {
	// ref begins with "&#".
	i := 2
	hex := false
	if i < len(ref) && (ref[i] == 'x' || ref[i] == 'X') {
		hex = true
		i++
	}

	var value rune
	digits := 0
	base := rune(10)
	if hex {
		base = 16
	}
	for i < len(ref) && ref[i] != ';' {
		var digit rune
		switch {
		case ref[i] >= '0' && ref[i] <= '9':
			digit = rune(ref[i] - '0')
		case hex && ref[i] >= 'a' && ref[i] <= 'f':
			digit = rune(ref[i]-'a') + 10
		case hex && ref[i] >= 'A' && ref[i] <= 'F':
			digit = rune(ref[i]-'A') + 10
		default:
			return 0, 0, ErrMalformedDTD
		}
		value = value*base + digit
		if value > utf8.MaxRune {
			return 0, 0, ErrMalformedDTD
		}
		digits++
		i++
	}
	if digits == 0 || i >= len(ref) || ref[i] != ';' {
		return 0, 0, ErrMalformedDTD
	}
	if !isValidCharRef(value) {
		return 0, 0, ErrMalformedDTD
	}
	return value, i + 1, nil
}

// isValidCharRef mirrors the XML 1.0 character reference validity rules:
// surrogate halves, permanently illegal code points and most control
// characters are forbidden.
func isValidCharRef(r rune) bool {
	switch {
	case r == 0x9 || r == 0xA || r == 0xD:
		return true
	case r < 0x20:
		return false
	case r >= 0xD800 && r <= 0xDFFF:
		return false
	case r == 0xFFFE || r == 0xFFFF:
		return false
	case r > utf8.MaxRune:
		return false
	default:
		return true
	}
}
