package netsec

import (
	"encoding/binary"
	"fmt"
)

const (
	// HandshakeTypeClientHello is the TLS handshake type for ClientHello.
	HandshakeTypeClientHello = 0x01
	// RecordTypeHandshake is the TLS record content type for handshake data.
	RecordTypeHandshake = 0x16
	// ExtensionPreSharedKey is the TLS 1.3 pre_shared_key extension type.
	ExtensionPreSharedKey = 41

	handshakeTypeClientHello = HandshakeTypeClientHello
	recordTypeHandshake      = RecordTypeHandshake
	extensionPreSharedKey    = ExtensionPreSharedKey
)

// Extension is one TLS ClientHello extension.
type Extension struct {
	Type       uint16
	Data       []byte
	Offset     int
	DataOffset int
}

// Identity is one entry in the pre_shared_key identities vector.
type Identity struct {
	Identity            []byte
	ObfuscatedTicketAge uint32
	Offset              int
	DataOffset          int
}

// BinderEntry is one PskBinderEntry.
type BinderEntry struct {
	Index        int
	Value        []byte
	Identity     []byte
	Offset       int
	LengthOffset int
}

// ClientHello is a parsed TLS 1.3 ClientHello. Offsets are absolute offsets
// into the input passed to ParseClientHello, so callers can mutate a copy of
// the input and reuse the offsets when sizes are unchanged.
type ClientHello struct {
	Raw           []byte
	Message       []byte
	MessageOffset int
	Body          []byte
	BodyOffset    int
	Headerless    bool

	Extensions []Extension
	Identities []Identity
	Binders    []BinderEntry

	PSKExtension        *Extension
	IdentitiesEnd       int
	BindersLengthOffset int
	BindersEnd          int
}

// HasPreSharedKey reports whether the ClientHello contains a valid
// pre_shared_key extension.
func (ch *ClientHello) HasPreSharedKey() bool {
	return ch != nil && ch.PSKExtension != nil
}

// BinderPrefix returns the RFC 8446 truncated ClientHello covered by every
// binder: the handshake message from its first byte through the end of the
// PSK identities vector. The binders vector length and binder values are
// excluded. ClientHello and extension length fields retain their on-wire
// values, as required by the TLS transcript.
func (ch *ClientHello) BinderPrefix() []byte {
	if ch == nil || !ch.HasPreSharedKey() {
		return nil
	}
	return ch.Raw[ch.MessageOffset:ch.IdentitiesEnd]
}

// PositionalPrefix returns the prefix covered by binder index when using
// PositionalCoverage. It ends immediately before the binder value, so the
// binder's one-byte length prefix and all earlier binders are included.
func (ch *ClientHello) PositionalPrefix(index int) []byte {
	if ch == nil || index < 0 || index >= len(ch.Binders) {
		return nil
	}
	return ch.Raw[ch.MessageOffset:ch.Binders[index].Offset]
}

// Prefix returns BinderPrefix for the RFC 8446 coverage mode.
func (ch *ClientHello) Prefix() []byte { return ch.BinderPrefix() }

// TranscriptPrefix is an alias for BinderPrefix.
func (ch *ClientHello) TranscriptPrefix() []byte { return ch.BinderPrefix() }

// BinderTranscript is an alias for BinderPrefix.
func (ch *ClientHello) BinderTranscript() []byte { return ch.BinderPrefix() }

// PrefixAt returns the complete prefix ending immediately before binder
// index's value.
func (ch *ClientHello) PrefixAt(index int) []byte {
	return ch.PositionalPrefix(index)
}

// FullPrefix is an alias for PrefixAt.
func (ch *ClientHello) FullPrefix(index int) []byte {
	return ch.PositionalPrefix(index)
}

// ComputeBinders is an alias for ExpectedBinders.
func (ch *ClientHello) ComputeBinders(opts ...any) ([][]byte, error) {
	return ch.ExpectedBinders(opts...)
}

// ValidateBinders is an alias for Verify.
func (ch *ClientHello) ValidateBinders(opts ...any) error {
	return ch.Verify(opts...)
}

// VerifyBinders is an alias for Verify.
func (ch *ClientHello) VerifyBinders(opts ...any) error {
	return ch.Verify(opts...)
}

// ParseClientHello parses a TLS ClientHello. It accepts a complete handshake
// message, a TLS handshake record containing one ClientHello, or a bare
// ClientHello body.
func ParseClientHello(data []byte) (*ClientHello, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedClientHello)
	}

	ch := &ClientHello{Raw: data, MessageOffset: 0, BodyOffset: 0}
	bodyStart := 0
	bodyEnd := len(data)

	switch {
	case len(data) >= 5 && data[0] == recordTypeHandshake:
		recordLen := int(binary.BigEndian.Uint16(data[3:5]))
		if 5+recordLen > len(data) {
			return nil, fmt.Errorf("%w: truncated TLS record", ErrMalformedClientHello)
		}
		record := data[5 : 5+recordLen]
		if len(record) < 4 || record[0] != handshakeTypeClientHello {
			return nil, fmt.Errorf("%w: TLS record does not start with a ClientHello", ErrMalformedClientHello)
		}
		msgLen := 4 + int(record[1])<<16 + int(record[2])<<8 + int(record[3])
		if msgLen > len(record) {
			return nil, fmt.Errorf("%w: fragmented ClientHello records are not supported", ErrMalformedClientHello)
		}
		ch.MessageOffset = 5
		ch.Message = data[5 : 5+msgLen]
		bodyStart = 5 + 4
		bodyEnd = 5 + msgLen

	case len(data) >= 4 && data[0] == handshakeTypeClientHello:
		msgLen := 4 + int(data[1])<<16 + int(data[2])<<8 + int(data[3])
		if msgLen > len(data) {
			return nil, fmt.Errorf("%w: truncated ClientHello handshake message", ErrMalformedClientHello)
		}
		ch.Message = data[:msgLen]
		bodyStart = 4
		bodyEnd = msgLen

	default:
		// Be liberal for callers that already stripped the handshake header.
		// The transcript is then exactly the bytes supplied by the caller.
		ch.Headerless = true
		ch.Message = data
		bodyStart = 0
		bodyEnd = len(data)
	}

	ch.BodyOffset = bodyStart
	ch.Body = data[bodyStart:bodyEnd]
	if err := ch.parseBody(); err != nil {
		return nil, err
	}
	return ch, nil
}

func (ch *ClientHello) parseBody() error {
	b := ch.Body
	base := ch.BodyOffset
	if len(b) < 2+32+1 {
		return fmt.Errorf("%w: truncated ClientHello body", ErrMalformedClientHello)
	}
	p := 2 + 32 // legacy_version + random

	sessionLen := int(b[p])
	p++
	if p+sessionLen > len(b) {
		return fmt.Errorf("%w: truncated legacy_session_id", ErrMalformedClientHello)
	}
	p += sessionLen

	if p+2 > len(b) {
		return fmt.Errorf("%w: missing cipher_suites length", ErrMalformedClientHello)
	}
	suitesLen := int(binary.BigEndian.Uint16(b[p : p+2]))
	p += 2
	if p+suitesLen > len(b) {
		return fmt.Errorf("%w: truncated cipher_suites", ErrMalformedClientHello)
	}
	p += suitesLen

	if p+1 > len(b) {
		return fmt.Errorf("%w: missing compression_methods length", ErrMalformedClientHello)
	}
	compressionLen := int(b[p])
	p++
	if p+compressionLen > len(b) {
		return fmt.Errorf("%w: truncated compression_methods", ErrMalformedClientHello)
	}
	p += compressionLen

	if p == len(b) {
		return nil
	}
	if p+2 > len(b) {
		return fmt.Errorf("%w: missing extensions length", ErrMalformedClientHello)
	}
	extensionsLen := int(binary.BigEndian.Uint16(b[p : p+2]))
	p += 2
	if p+extensionsLen > len(b) {
		return fmt.Errorf("%w: truncated extensions", ErrMalformedClientHello)
	}
	extensionsEnd := p + extensionsLen

	for p < extensionsEnd {
		if p+4 > extensionsEnd {
			return fmt.Errorf("%w: truncated extension header", ErrMalformedClientHello)
		}
		extType := binary.BigEndian.Uint16(b[p : p+2])
		extLen := int(binary.BigEndian.Uint16(b[p+2 : p+4]))
		dataOffset := base + p + 4
		if dataOffset+extLen > base+extensionsEnd {
			return fmt.Errorf("%w: truncated extension body", ErrMalformedClientHello)
		}
		ext := Extension{
			Type:       extType,
			Data:       ch.Raw[dataOffset : dataOffset+extLen],
			Offset:     base + p,
			DataOffset: dataOffset,
		}
		ch.Extensions = append(ch.Extensions, ext)
		p += 4 + extLen
	}
	if p != extensionsEnd {
		return fmt.Errorf("%w: extension length mismatch", ErrMalformedClientHello)
	}

	for i := range ch.Extensions {
		if ch.Extensions[i].Type != extensionPreSharedKey {
			continue
		}
		if ch.PSKExtension != nil {
			return fmt.Errorf("%w: multiple pre_shared_key extensions", ErrMalformedClientHello)
		}
		// RFC 8446 requires pre_shared_key to be the last extension. If it is
		// not last, a prefix ending at the identities vector would silently
		// omit the following extensions.
		if i != len(ch.Extensions)-1 {
			return fmt.Errorf("%w: pre_shared_key must be the last extension", ErrMalformedClientHello)
		}
		ch.PSKExtension = &ch.Extensions[i]
		if err := ch.parsePSK(); err != nil {
			return err
		}
	}
	return nil
}

func (ch *ClientHello) parsePSK() error {
	ext := ch.PSKExtension
	data := ext.Data
	base := ext.DataOffset
	if len(data) < 2 {
		return fmt.Errorf("%w: truncated PSK identities length", ErrMalformedClientHello)
	}
	p := 0
	identitiesLen := int(binary.BigEndian.Uint16(data[p : p+2]))
	p += 2
	if identitiesLen == 0 {
		return fmt.Errorf("%w: empty PSK identities vector", ErrMalformedClientHello)
	}
	if p+identitiesLen > len(data) {
		return fmt.Errorf("%w: truncated PSK identities", ErrMalformedClientHello)
	}
	identitiesEnd := p + identitiesLen
	for p < identitiesEnd {
		if p+2 > identitiesEnd {
			return fmt.Errorf("%w: truncated PSK identity length", ErrMalformedClientHello)
		}
		idLen := int(binary.BigEndian.Uint16(data[p : p+2]))
		dataOffset := base + p + 2
		if idLen == 0 || dataOffset+idLen+4 > base+identitiesEnd {
			return fmt.Errorf("%w: truncated PSK identity", ErrMalformedClientHello)
		}
		age := binary.BigEndian.Uint32(ch.Raw[dataOffset+idLen : dataOffset+idLen+4])
		ch.Identities = append(ch.Identities, Identity{
			Identity:            ch.Raw[dataOffset : dataOffset+idLen],
			ObfuscatedTicketAge: age,
			Offset:              base + p,
			DataOffset:          dataOffset,
		})
		p = dataOffset + idLen + 4 - base
	}
	if p != identitiesEnd {
		return fmt.Errorf("%w: PSK identities length mismatch", ErrMalformedClientHello)
	}
	if len(ch.Identities) == 0 {
		return fmt.Errorf("%w: empty PSK identities vector", ErrMalformedClientHello)
	}
	ch.IdentitiesEnd = base + identitiesEnd

	if p+2 > len(data) {
		return fmt.Errorf("%w: missing PSK binders length", ErrMalformedClientHello)
	}
	ch.BindersLengthOffset = base + p
	bindersLen := int(binary.BigEndian.Uint16(data[p : p+2]))
	p += 2
	if bindersLen == 0 {
		return fmt.Errorf("%w: empty PSK binders vector", ErrMalformedClientHello)
	}
	if p+bindersLen > len(data) {
		return fmt.Errorf("%w: truncated PSK binders", ErrMalformedClientHello)
	}
	bindersEnd := p + bindersLen
	for p < bindersEnd {
		if p+1 > bindersEnd {
			return fmt.Errorf("%w: truncated PSK binder length", ErrMalformedClientHello)
		}
		if len(ch.Binders) >= len(ch.Identities) {
			return fmt.Errorf("%w: more binders than identities", ErrMalformedClientHello)
		}
		binderLen := int(data[p])
		valueOffset := base + p + 1
		if binderLen == 0 || valueOffset+binderLen > base+bindersEnd {
			return fmt.Errorf("%w: truncated PSK binder", ErrMalformedClientHello)
		}
		ch.Binders = append(ch.Binders, BinderEntry{
			Index:        len(ch.Binders),
			Value:        ch.Raw[valueOffset : valueOffset+binderLen],
			Identity:     ch.Identities[len(ch.Binders)].Identity,
			Offset:       valueOffset,
			LengthOffset: base + p,
		})
		p += 1 + binderLen
	}
	if p != bindersEnd {
		return fmt.Errorf("%w: PSK binders length mismatch", ErrMalformedClientHello)
	}
	if len(ch.Binders) != len(ch.Identities) {
		return fmt.Errorf("%w: %d PSK identities but %d binders", ErrMalformedClientHello, len(ch.Identities), len(ch.Binders))
	}
	ch.BindersEnd = base + bindersEnd
	if p != len(data) {
		return fmt.Errorf("%w: trailing bytes after PSK binders", ErrMalformedClientHello)
	}
	return nil
}

// ExpectedBinders computes the binder value for every identity. With an
// unkeyed transcript hash, each binder covers the ClientHello through the byte
// immediately before that binder's value. With a binder key, the default is
// the RFC 8446 prefix through the identities vector. Pass StandardCoverage or
// PositionalCoverage to select the form explicitly.
func (ch *ClientHello) ExpectedBinders(opts ...any) ([][]byte, error) {
	if ch == nil || !ch.HasPreSharedKey() {
		return nil, ErrNoPreSharedKey
	}
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	// Positional binders include preceding binder entries, so fill a scratch
	// copy sequentially rather than hashing all original placeholders.
	scratch := append([]byte(nil), ch.Raw...)
	out := make([][]byte, len(ch.Binders))
	for i := range ch.Binders {
		var prefix []byte
		if usePositionalCoverage(o) {
			prefix = scratch[ch.MessageOffset:ch.Binders[i].Offset]
		} else {
			prefix = scratch[ch.MessageOffset:ch.IdentitiesEnd]
		}
		out[i] = ComputeBinder(prefix, o)
		if len(out[i]) != len(ch.Binders[i].Value) {
			return nil, fmt.Errorf("netsec: binder %d has length %d, need %d for selected hash", i, len(ch.Binders[i].Value), len(out[i]))
		}
		copy(scratch[ch.Binders[i].Offset:ch.Binders[i].Offset+len(out[i])], out[i])
	}
	return out, nil
}

func (ch *ClientHello) prefixForMode(mode CoverageMode, index int) ([]byte, error) {
	switch mode {
	case StandardCoverage:
		return ch.BinderPrefix(), nil
	case PositionalCoverage:
		if index < 0 || index >= len(ch.Binders) {
			return nil, fmt.Errorf("netsec: binder index %d out of range", index)
		}
		return ch.PositionalPrefix(index), nil
	case EitherCoverage:
		if index < 0 || index >= len(ch.Binders) {
			return nil, fmt.Errorf("netsec: binder index %d out of range", index)
		}
		return ch.PositionalPrefix(index), nil
	default:
		return nil, fmt.Errorf("netsec: unknown coverage mode %d", mode)
	}
}

// Verify checks every binder in ch. By default it accepts binders covering
// either the RFC 8446 prefix or a position-specific prefix that includes
// preceding binder entries. Both prefixes contain every extension and every
// identity. Pass StandardCoverage or PositionalCoverage to require one form.
func (ch *ClientHello) Verify(opts ...any) error {
	if ch == nil || !ch.HasPreSharedKey() {
		return ErrNoPreSharedKey
	}
	o, err := resolveOptions(opts)
	if err != nil {
		return err
	}
	if o.Mode == EitherCoverage || o.AcceptAnyCoverage {
		return ch.verifyAnyCoverage(o)
	}
	prefixes := make([][]byte, len(ch.Binders))
	for i := range ch.Binders {
		prefix, err := ch.prefixForMode(o.Mode, i)
		if err != nil {
			return err
		}
		prefixes[i] = prefix
	}
	return ch.verifyPrefixes(prefixes, o)
}

func (ch *ClientHello) verifyPrefixes(prefixes [][]byte, o Options) error {
	for i, b := range ch.Binders {
		expected := ComputeBinder(prefixes[i], o)
		if !constantTimeEqual(expected, b.Value) {
			return &BinderMismatchError{
				Index:    i,
				Expected: expected,
				Actual:   append([]byte(nil), b.Value...),
			}
		}
	}
	return nil
}

func (ch *ClientHello) verifyAnyCoverage(o Options) error {
	standard := ch.BinderPrefix()
	for i, b := range ch.Binders {
		candidates := [][]byte{
			standard,
			ch.Raw[ch.MessageOffset:ch.Binders[i].LengthOffset],
			ch.PositionalPrefix(i),
		}
		ok := false
		for _, prefix := range candidates {
			expected := ComputeBinder(prefix, o)
			if len(expected) == len(b.Value) && constantTimeEqual(expected, b.Value) {
				ok = true
				break
			}
		}
		if !ok {
			expected := ComputeBinder(standard, o)
			return &BinderMismatchError{
				Index:    i,
				Expected: expected,
				Actual:   append([]byte(nil), b.Value...),
			}
		}
	}
	return nil
}

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
