package netsec

import "fmt"

// ParseTLSClientHello is an alias for ParseClientHello.
func ParseTLSClientHello(data []byte) (*ClientHello, error) {
	return ParseClientHello(data)
}

// BinderForPrefix is an alias for ComputeBinder.
func BinderForPrefix(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// BinderForTranscript is an alias for ComputeBinder.
func BinderForTranscript(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// ExpectedBinderForTranscript is an alias for ComputeBinder.
func ExpectedBinderForTranscript(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// ValidateForPrefix checks a binder against an already-truncated transcript
// prefix.
func ValidateForPrefix(prefix, binder []byte, opts ...any) error {
	return ValidateBinder(prefix, binder, opts...)
}

// VerifyTranscriptBinder is an alias for VerifyBinder.
func VerifyTranscriptBinder(prefix, binder []byte, opts ...any) bool {
	return VerifyBinder(prefix, binder, opts...)
}

// ValidateTranscriptBinder is an alias for ValidateBinder.
func ValidateTranscriptBinder(prefix, binder []byte, opts ...any) error {
	return ValidateBinder(prefix, binder, opts...)
}

// ComputeClientHelloBinders computes the expected binder for each identity in
// a ClientHello. It does not mutate clientHello.
func ComputeClientHelloBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return nil, err
	}
	return ch.ExpectedBinders(opts...)
}

// CalculateBinders is an alias for ComputeClientHelloBinders.
func CalculateBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	return ComputeClientHelloBinders(clientHello, opts...)
}

// ExpectedClientHelloBinders is an alias for ComputeClientHelloBinders.
func ExpectedClientHelloBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	return ComputeClientHelloBinders(clientHello, opts...)
}

// ComputePSKBinders is an alias for ComputeClientHelloBinders.
func ComputePSKBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	return ComputeClientHelloBinders(clientHello, opts...)
}

// ComputeBinders is an alias for ComputeClientHelloBinders.
func ComputeBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	return ComputeClientHelloBinders(clientHello, opts...)
}

// ExtractBinders returns a copy of the binders embedded in clientHello.
func ExtractBinders(clientHello []byte) ([]Binder, error) {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return nil, err
	}
	out := make([]Binder, len(ch.Binders))
	for i, binder := range ch.Binders {
		out[i] = binder
		out[i].Value = append([]byte(nil), binder.Value...)
	}
	return out, nil
}

// ComputePSKBinder returns the first expected binder in a ClientHello.
func ComputePSKBinder(clientHello []byte, opts ...any) ([]byte, error) {
	all, err := ComputeClientHelloBinders(clientHello, opts...)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, ErrNoPreSharedKey
	}
	return all[0], nil
}

// ComputeBinderForClientHello returns the first expected binder in a
// ClientHello.
func ComputeBinderForClientHello(clientHello []byte, opts ...any) ([]byte, error) {
	return ComputePSKBinder(clientHello, opts...)
}

// ExpectedBinderForClientHello is an alias for ComputeBinderForClientHello.
func ExpectedBinderForClientHello(clientHello []byte, opts ...any) ([]byte, error) {
	return ComputePSKBinder(clientHello, opts...)
}

// ComputeBinderAt returns the expected value of the binder at index.
func ComputeBinderAt(clientHello []byte, index int, opts ...any) ([]byte, error) {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(ch.Binders) {
		return nil, fmt.Errorf("netsec: binder index %d out of range", index)
	}
	values, err := ch.ExpectedBinders(opts...)
	if err != nil {
		return nil, err
	}
	return values[index], nil
}

// VerifyBinderAt verifies only the binder at index.
func VerifyBinderAt(clientHello []byte, index int, opts ...any) error {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(ch.Binders) {
		return fmt.Errorf("netsec: binder index %d out of range", index)
	}
	o, err := resolveOptions(opts)
	if err != nil {
		return err
	}
	candidates := [][]byte{ch.BinderPrefix()}
	if o.Mode == EitherCoverage || o.AcceptAnyCoverage {
		candidates = append(candidates,
			ch.Raw[ch.MessageOffset:ch.Binders[index].LengthOffset],
			ch.PositionalPrefix(index),
		)
	} else if o.Mode == PositionalCoverage {
		candidates = [][]byte{ch.PositionalPrefix(index)}
	}
	for _, prefix := range candidates {
		expected := ComputeBinder(prefix, o)
		if constantTimeEqual(expected, ch.Binders[index].Value) {
			return nil
		}
	}
	return &BinderMismatchError{
		Index:    index,
		Expected: ComputeBinder(candidates[0], o),
		Actual:   append([]byte(nil), ch.Binders[index].Value...),
	}
}

// VerifyBinderValue reports whether binder matches the expected value for an
// already-truncated transcript prefix.
func VerifyBinderValue(prefix, binder []byte, opts ...any) bool {
	return VerifyBinder(prefix, binder, opts...)
}

// VerifyClientHelloBinders verifies every binder embedded in clientHello
// against the complete transcript prefix for its position.
func VerifyClientHelloBinders(clientHello []byte, opts ...any) error {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return err
	}
	return ch.Verify(opts...)
}

// ValidateClientHelloBinders is an alias for VerifyClientHelloBinders.
func ValidateClientHelloBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// CheckClientHelloBinders is an alias for VerifyClientHelloBinders.
func CheckClientHelloBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// VerifyPSKBinders is an alias for VerifyClientHelloBinders.
func VerifyPSKBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// ValidatePSKBinders is an alias for VerifyClientHelloBinders.
func ValidatePSKBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// CheckPSKBinders is an alias for VerifyClientHelloBinders.
func CheckPSKBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// VerifyPSKBinder verifies all binders in clientHello. The name is singular
// for compatibility with TLS stacks that expose one combined PSK operation.
func VerifyPSKBinder(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// ValidatePSKBinder is an alias for VerifyPSKBinder.
func ValidatePSKBinder(clientHello []byte, opts ...any) error {
	return VerifyPSKBinder(clientHello, opts...)
}

// CheckPSKBinder is an alias for VerifyPSKBinder.
func CheckPSKBinder(clientHello []byte, opts ...any) error {
	return VerifyPSKBinder(clientHello, opts...)
}

// VerifyBinders is an alias for VerifyClientHelloBinders.
func VerifyBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// ValidateBinders is an alias for VerifyClientHelloBinders.
func ValidateBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// CheckBinders is an alias for VerifyClientHelloBinders.
func CheckBinders(clientHello []byte, opts ...any) error {
	return VerifyClientHelloBinders(clientHello, opts...)
}

// BindersCoverTranscript reports whether all binders cover the complete
// transcript prefix.
func BindersCoverTranscript(clientHello []byte, opts ...any) bool {
	return VerifyClientHelloBinders(clientHello, opts...) == nil
}

// FillClientHelloBinders returns a copy of clientHello with every binder
// replaced by the value for its complete transcript prefix. For unkeyed
// transcript hashes each binder covers everything before that binder value;
// keyed binders default to the RFC 8446 identities-only prefix. Pass
// StandardCoverage or PositionalCoverage to select the form explicitly.
// Existing binder entries must already have the correct length for the
// selected hash.
func FillClientHelloBinders(clientHello []byte, opts ...any) ([]byte, error) {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return nil, err
	}
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), clientHello...)
	for i, entry := range ch.Binders {
		var prefix []byte
		if usePositionalCoverage(o) {
			prefix = out[ch.MessageOffset:entry.Offset]
		} else {
			prefix = out[ch.MessageOffset:ch.IdentitiesEnd]
		}
		v := ComputeBinder(prefix, o)
		if len(v) != len(entry.Value) {
			return nil, fmt.Errorf("netsec: binder %d has length %d, need %d for selected hash", i, len(entry.Value), len(v))
		}
		copy(out[entry.Offset:entry.Offset+len(v)], v)
	}
	return out, nil
}

// FillPositionalBinders fills each binder with the hash of the prefix ending
// immediately before that binder's value. Later binders therefore cover the
// already-filled earlier binder entries.
func FillPositionalBinders(clientHello []byte, opts ...any) ([]byte, error) {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	o.Mode = PositionalCoverage
	return FillClientHelloBinders(clientHello, o)
}

// FillStandardBinders fills each binder with the RFC 8446 hash through the
// pre_shared_key identities vector.
func FillStandardBinders(clientHello []byte, opts ...any) ([]byte, error) {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	o.Mode = StandardCoverage
	return FillClientHelloBinders(clientHello, o)
}

// ComputePositionalBinders computes binders covering everything before each
// binder value.
func ComputePositionalBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	o.Mode = PositionalCoverage
	return ComputeClientHelloBinders(clientHello, o)
}

// ComputeStandardBinders computes RFC 8446 binders covering the ClientHello
// through the pre_shared_key identities vector.
func ComputeStandardBinders(clientHello []byte, opts ...any) ([][]byte, error) {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	o.Mode = StandardCoverage
	return ComputeClientHelloBinders(clientHello, o)
}

// SetBinders is an alias for FillClientHelloBinders.
func SetBinders(clientHello []byte, opts ...any) ([]byte, error) {
	return FillClientHelloBinders(clientHello, opts...)
}

// ComputeAndFillBinders is an alias for FillClientHelloBinders.
func ComputeAndFillBinders(clientHello []byte, opts ...any) ([]byte, error) {
	return FillClientHelloBinders(clientHello, opts...)
}

// RemoveExtension returns a copy of clientHello with the first extension of
// the given type removed and all enclosing lengths adjusted. It is primarily
// useful for negative tests that ensure a binder no longer validates when an
// authenticated extension disappears.
func RemoveExtension(clientHello []byte, extType uint16) ([]byte, error) {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return nil, err
	}
	var target *Extension
	for i := range ch.Extensions {
		if ch.Extensions[i].Type == extType {
			target = &ch.Extensions[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("netsec: extension %d not found", extType)
	}

	removeLen := 4 + len(target.Data)
	start := target.Offset
	end := start + removeLen
	out := make([]byte, 0, len(clientHello)-removeLen)
	out = append(out, clientHello[:start]...)
	out = append(out, clientHello[end:]...)

	if !ch.Headerless {
		decrementUint24(out, ch.MessageOffset+1, removeLen)
	}
	if !ch.Headerless && ch.MessageOffset == 5 && len(clientHello) >= 5 && clientHello[0] == recordTypeHandshake {
		decrementUint16(out, 3, removeLen)
	}
	if len(ch.Extensions) > 0 {
		decrementUint16(out, ch.Extensions[0].Offset-2, removeLen)
	}
	return out, nil
}

// RemoveIdentity returns a copy of clientHello with one PSK identity and its
// matching binder removed and all enclosing lengths adjusted.
func RemoveIdentity(clientHello []byte, index int) ([]byte, error) {
	ch, err := ParseClientHello(clientHello)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(ch.Identities) || index >= len(ch.Binders) {
		return nil, fmt.Errorf("netsec: identity index %d out of range", index)
	}

	psk := ch.PSKExtension
	oldData := psk.Data
	base := psk.DataOffset
	if len(oldData) < 4 {
		return nil, ErrMalformedClientHello
	}

	identitiesLen := int(oldData[0])<<8 | int(oldData[1])
	bindersLenPos := 2 + identitiesLen
	if bindersLenPos+2 > len(oldData) {
		return nil, ErrMalformedClientHello
	}
	bindersLen := int(oldData[bindersLenPos])<<8 | int(oldData[bindersLenPos+1])

	identity := ch.Identities[index]
	identityStart := identity.Offset - base
	identityEnd := identity.DataOffset + len(identity.Identity) + 4 - base
	binder := ch.Binders[index]
	binderStart := binder.LengthOffset - base
	binderEnd := binder.Offset + len(binder.Value) - base
	if identityStart < 2 || identityEnd > bindersLenPos ||
		binderStart < bindersLenPos+2 || binderEnd > len(oldData) {
		return nil, ErrMalformedClientHello
	}

	newIdentitiesLen := identitiesLen - (identityEnd - identityStart)
	newBindersLen := bindersLen - (binderEnd - binderStart)
	newData := make([]byte, 0, len(oldData)-(identityEnd-identityStart)-(binderEnd-binderStart))
	newData = appendUint16(newData, newIdentitiesLen)
	newData = append(newData, oldData[2:identityStart]...)
	newData = append(newData, oldData[identityEnd:bindersLenPos]...)
	newData = appendUint16(newData, newBindersLen)
	newData = append(newData, oldData[bindersLenPos+2:binderStart]...)
	newData = append(newData, oldData[binderEnd:]...)

	delta := len(oldData) - len(newData)
	out := make([]byte, 0, len(clientHello)-delta)
	out = append(out, clientHello[:base]...)
	out = append(out, newData...)
	out = append(out, clientHello[base+len(oldData):]...)

	// The PSK extension is required to be last. Its extension length field and
	// the extensions-vector length field both precede the extension data.
	decrementUint16(out, psk.Offset+2, delta)
	decrementUint16(out, ch.Extensions[0].Offset-2, delta)
	if !ch.Headerless {
		decrementUint24(out, ch.MessageOffset+1, delta)
	}
	if !ch.Headerless && ch.MessageOffset == 5 && len(clientHello) >= 5 && clientHello[0] == recordTypeHandshake {
		decrementUint16(out, 3, delta)
	}
	return out, nil
}

func appendUint16(dst []byte, v int) []byte {
	return append(dst, byte(v>>8), byte(v))
}

func decrementUint16(b []byte, offset, delta int) {
	if offset < 0 || offset+2 > len(b) {
		return
	}
	v := int(b[offset])<<8 | int(b[offset+1])
	v -= delta
	b[offset] = byte(v >> 8)
	b[offset+1] = byte(v)
}

func decrementUint24(b []byte, offset, delta int) {
	if offset < 0 || offset+3 > len(b) {
		return
	}
	v := int(b[offset])<<16 | int(b[offset+1])<<8 | int(b[offset+2])
	v -= delta
	b[offset] = byte(v >> 16)
	b[offset+1] = byte(v >> 8)
	b[offset+2] = byte(v)
}
