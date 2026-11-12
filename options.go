package netsec

// Has reports whether code survived normalization.
func (options Options) Has(code uint8) bool {
	_, ok := options[code]
	return ok
}

// Raw returns a copy of the fully concatenated value of code, or nil when the
// option is absent. Returning a copy keeps the normalized map authoritative.
func (options Options) Raw(code uint8) []byte {
	value, ok := options[code]
	if !ok {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}

// Byte returns the first byte of the concatenated value, e.g. the DHCP
// message type. It returns ok=false for absent or zero-length options.
func (options Options) Byte(code uint8) (byte, bool) {
	value, ok := options[code]
	if !ok || len(value) == 0 {
		return 0, false
	}
	return value[0], true
}

// IPv4List returns every concatenated 4-byte value of code as IPv4 byte
// slices. It returns ok=false when the option is absent.
func (options Options) IPv4List(code uint8) ([][]byte, bool) {
	value, ok := options[code]
	if !ok || len(value)%4 != 0 {
		return nil, false
	}
	out := make([][]byte, 0, len(value)/4)
	for i := 0; i+4 <= len(value); i += 4 {
		addr := make([]byte, 4)
		copy(addr, value[i:i+4])
		out = append(out, addr)
	}
	return out, true
}
