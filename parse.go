package netsec

import "fmt"

// ParseOptions normalizes every DHCP option in packet into one Options map.
//
// Processing order is fixed and matches RFC 2131 §4.1 / RFC 3396:
//  1. the main option area (after the magic cookie),
//  2. the file field, only when the overload option requests it,
//  3. the sname field, only when the overload option requests it.
//
// PAD (0) only pads the current area. END (255) only terminates the current
// area; running out of area bytes without END is accepted as an implicit
// terminator. Repeated options are concatenated in the order above, and each
// concatenated value is copied so the result never aliases packet memory.
// A malformed packet returns a non-nil error and a nil Options map, so no
// partially parsed binding information can escape on failure.
func ParseOptions(packet []byte) (Options, error) {
	if len(packet) < bootpMinLen {
		return nil, ErrPacketTooShort
	}
	var cookie [magicCookieLen]byte
	copy(cookie[:], packet[mainOptionsStart:mainOptionsStart+magicCookieLen])
	if cookie != magicCookie {
		return nil, ErrBadCookie
	}

	options := make(Options)

	overload, found, err := parseArea(packet, packet[mainOptionsStart+magicCookieLen:], options, true)
	if err != nil {
		return nil, err
	}

	// Option 52 is only authoritative in the main area. The main-area scan
	// rejects every occurrence after the first, so duplicate overload options
	// fail the whole parse instead of silently overriding earlier values.
	if found {
		if overload < overloadFileBit || overload > (overloadFileBit|overloadSnameBit) {
			return nil, fmt.Errorf("%w: value %d is out of range", ErrOverloadValue, overload)
		}
		if overload&overloadFileBit != 0 {
			if _, _, err := parseArea(packet, packet[fileStart:fileStart+fileLen], options, false); err != nil {
				return nil, err
			}
		}
		if overload&overloadSnameBit != 0 {
			if _, _, err := parseArea(packet, packet[snameStart:snameStart+snameLen], options, false); err != nil {
				return nil, err
			}
		}
	}

	return options, nil
}

// parseArea scans one option area and appends every option it contains to
// options. The area is always addressed relative to its own boundaries, so a
// length byte can never pull bytes from another area (or from the packet tail
// beyond this area). allowOverload marks the main option area; an overload
// option encountered anywhere else is a nested-overload attack and is rejected.
func parseArea(packet, area []byte, options Options, allowOverload bool) (overload uint8, found bool, err error) {
	i := 0
	for i < len(area) {
		code := area[i]
		i++
		switch code {
		case OptPad:
			// PAD takes effect only inside its own area: skip one byte and
			// continue scanning. It never terminates the area.
			continue
		case OptEnd:
			// END terminates only the area containing it.
			return overload, found, nil
		}

		if i >= len(area) {
			return 0, false, fmt.Errorf("%w: option %d missing length byte", ErrTruncated, code)
		}
		optLen := int(area[i])
		i++
		if optLen > len(area)-i {
			return 0, false, fmt.Errorf("%w: option %d declares %d bytes, %d remain in area",
				ErrTruncated, code, optLen, len(area)-i)
		}
		value := area[i : i+optLen]
		i += optLen

		if code == OptOverload {
			if !allowOverload {
				return 0, false, fmt.Errorf("%w: option 52 seen in file/sname area", ErrNestedOverload)
			}
			if found || optLen != 1 {
				return 0, false, fmt.Errorf("%w: duplicate or malformed overload option", ErrOverloadValue)
			}
			overload = value[0]
			found = true
		}

		appendOption(options, code, value)
	}
	return overload, found, nil
}

// appendOption concatenates repeated option values per RFC 3396 and stores an
// independent copy of every byte.
func appendOption(options Options, code uint8, value []byte) {
	merged := make([]byte, 0, len(options[code])+len(value))
	merged = append(merged, options[code]...)
	merged = append(merged, value...)
	options[code] = merged
}
