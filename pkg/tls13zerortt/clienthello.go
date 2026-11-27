package tls13zerortt

import (
	"crypto/hmac"
	"encoding/binary"
)

// TLS content type for the handshake record layer (RFC 8446 Section 5.1).
const contentTypeHandshake = 0x16

// HandshakeTypeClientHello is the ClientHello handshake message type.
const HandshakeTypeClientHello = 0x01

// ExtensionPreSharedKey is the pre_shared_key extension codepoint (41).
const ExtensionPreSharedKey = 41

// ExtensionEarlyData is the early_data extension codepoint (42).
const ExtensionEarlyData = 42

// cursor is a bounds-checked byte reader for adversarial wire input.
type cursor struct {
	buf []byte
	pos int
}

func (c *cursor) remaining() int { return len(c.buf) - c.pos }

func (c *cursor) readUint8() (byte, bool) {
	if c.remaining() < 1 {
		return 0, false
	}
	v := c.buf[c.pos]
	c.pos++
	return v, true
}

func (c *cursor) readUint16() (uint16, bool) {
	if c.remaining() < 2 {
		return 0, false
	}
	v := binary.BigEndian.Uint16(c.buf[c.pos:])
	c.pos += 2
	return v, true
}

func (c *cursor) readUint32() (uint32, bool) {
	if c.remaining() < 4 {
		return 0, false
	}
	v := binary.BigEndian.Uint32(c.buf[c.pos:])
	c.pos += 4
	return v, true
}

func (c *cursor) readBytes(n int) ([]byte, bool) {
	if n < 0 || c.remaining() < n {
		return nil, false
	}
	v := c.buf[c.pos : c.pos+n]
	c.pos += n
	return v, true
}

func (c *cursor) readVector8() ([]byte, bool) {
	n, ok := c.readUint8()
	if !ok {
		return nil, false
	}
	return c.readBytes(int(n))
}

func (c *cursor) readVector16() ([]byte, bool) {
	n, ok := c.readUint16()
	if !ok {
		return nil, false
	}
	return c.readBytes(int(n))
}

// binderRange records the [start,end) content offsets of one PskBinderEntry
// (excluding its one-byte length prefix) inside Raw.
type binderRange struct {
	start int
	end   int
}

// ParsedClientHello exposes the offsets and parsed fields needed to verify
// PSK binders and to evaluate 0-RTT policy. All offsets are relative to the
// start of the handshake message (type byte), excluding any record header.
type ParsedClientHello struct {
	// Raw is the handshake message bytes (TLSPlaintext.fragment).
	Raw []byte

	CipherSuite uint16

	// Length-field offsets rewritten when building the truncated message.
	bodyLengthOffset       int
	extensionsLengthOffset int
	pskExtLengthOffset     int

	// Binder list location: the uint16 binder-list length prefix and the
	// binder entries run to the end of the pre_shared_key extension.
	binderListLengthOffset int
	binderEntriesOffset    int
	binderEntriesEnd       int

	binderRanges []binderRange
	identities   []pskIdentity
	hasEarlyData bool
}

type pskIdentity struct {
	identity            []byte
	obfuscatedTicketAge uint32
}

// IdentityCount returns the number of offered PSK identities.
func (p *ParsedClientHello) IdentityCount() int { return len(p.identities) }

// ObfuscatedTicketAge returns the wire obfuscated_ticket_age for identity i.
func (p *ParsedClientHello) ObfuscatedTicketAge(i int) (uint32, bool) {
	if i < 0 || i >= len(p.identities) {
		return 0, false
	}
	return p.identities[i].obfuscatedTicketAge, true
}

// Identity returns the opaque PskIdentity identity bytes (ticket reference).
func (p *ParsedClientHello) Identity(i int) ([]byte, bool) {
	if i < 0 || i >= len(p.identities) {
		return nil, false
	}
	return p.identities[i].identity, true
}

// HasEarlyData reports whether the ClientHello contains the early_data
// extension (empty extension body in ClientHello).
func (p *ParsedClientHello) HasEarlyData() bool { return p.hasEarlyData }

// StripRecordLayer removes a single TLS 1.3 record layer header when present.
// It accepts either a bare handshake message or one TLSPlaintext fragment of
// type handshake(22). Multi-fragment inputs are rejected: the host reassembles
// records before calling this package.
func StripRecordLayer(record []byte) ([]byte, error) {
	if len(record) == 0 {
		return nil, ErrMalformedClientHello
	}
	if record[0] != contentTypeHandshake {
		// Assume a bare handshake message.
		return record, nil
	}
	if len(record) < 5 {
		return nil, ErrMalformedClientHello
	}
	fragLen := int(binary.BigEndian.Uint16(record[3:5]))
	if fragLen == 0 || 5+fragLen != len(record) {
		return nil, ErrMalformedClientHello
	}
	frag := record[5:]
	if len(frag) < 4 || frag[0] != HandshakeTypeClientHello {
		return nil, ErrMalformedClientHello
	}
	return frag, nil
}

// ParseClientHello parses a TLS 1.3 ClientHello handshake message and locates
// the pre_shared_key binder list. raw may include a single record header.
func ParseClientHello(raw []byte) (*ParsedClientHello, error) {
	msg, err := StripRecordLayer(raw)
	if err != nil {
		return nil, err
	}
	if len(msg) < 4 || msg[0] != HandshakeTypeClientHello {
		return nil, ErrMalformedClientHello
	}

	p := &ParsedClientHello{Raw: msg}
	p.bodyLengthOffset = 1
	declaredLen := int(uint32(msg[1])<<16 | uint32(msg[2])<<8 | uint32(msg[3]))
	if 4+declaredLen != len(msg) {
		return nil, ErrMalformedClientHello
	}

	c := &cursor{buf: msg, pos: 4}

	// legacy_version (2) + random (32).
	if _, ok := c.readBytes(34); !ok {
		return nil, ErrMalformedClientHello
	}
	// legacy_session_id<0..32>.
	sessionID, ok := c.readVector8()
	if !ok || len(sessionID) > 32 {
		return nil, ErrMalformedClientHello
	}
	// cipher_suites<2..2^16-2>.
	cipherSuites, ok := c.readVector16()
	if !ok || len(cipherSuites) < 2 || len(cipherSuites)%2 != 0 {
		return nil, ErrMalformedClientHello
	}
	p.CipherSuite = binary.BigEndian.Uint16(cipherSuites[:2])
	// legacy_compression_methods<1..2^8-1>.
	compMethods, ok := c.readVector8()
	if !ok || len(compMethods) < 1 {
		return nil, ErrMalformedClientHello
	}
	// extensions<8..2^16-1>.
	if c.remaining() < 2 {
		return nil, ErrMalformedClientHello
	}
	p.extensionsLengthOffset = c.pos
	extBlockLen := int(binary.BigEndian.Uint16(msg[c.pos:]))
	c.pos += 2
	extBlockEnd := c.pos + extBlockLen
	if extBlockEnd != len(msg) {
		return nil, ErrMalformedClientHello
	}

	var lastExtType uint16
	extCount := 0
	for c.pos < extBlockEnd {
		if c.remaining() < 4 {
			return nil, ErrMalformedClientHello
		}
		extType := binary.BigEndian.Uint16(msg[c.pos:])
		extLen := int(binary.BigEndian.Uint16(msg[c.pos+2:]))
		extDataOff := c.pos + 4
		extDataEnd := extDataOff + extLen
		if extDataEnd > extBlockEnd {
			return nil, ErrMalformedClientHello
		}
		lastExtType = extType
		extCount++

		switch extType {
		case ExtensionEarlyData:
			// In ClientHello the body is empty; max_early_data_size lives
			// only in NewSessionTicket.
			if extLen != 0 {
				return nil, ErrMalformedClientHello
			}
			p.hasEarlyData = true
		case ExtensionPreSharedKey:
			if err := p.parsePreSharedKey(msg, extDataOff, extDataEnd); err != nil {
				return nil, err
			}
		}
		c.pos = extDataEnd
	}

	if extCount == 0 {
		return nil, ErrMalformedClientHello
	}
	if p.binderListLengthOffset == 0 {
		return nil, ErrNoPreSharedKey
	}
	if lastExtType != ExtensionPreSharedKey {
		return nil, ErrPreSharedKeyNotLast
	}
	return p, nil
}

// parsePreSharedKey parses the pre_shared_key extension body and records every
// length-field offset needed for binder truncation.
//
//	struct {
//	  PskIdentity identities<7..2^16-1>;
//	  PskBinderEntry binders<33..2^16-1>;
//	} OfferedPsks;
//
// Extension data wire layout:
//
//	identities_len(2) identities... binders_len(2) binders...
func (p *ParsedClientHello) parsePreSharedKey(msg []byte, dataOff, dataEnd int) error {
	// Extension overall length sits two bytes before the data region.
	p.pskExtLengthOffset = dataOff - 2

	c := &cursor{buf: msg, pos: dataOff}
	identitiesBlock, ok := c.readVector16()
	if !ok {
		return ErrMalformedClientHello
	}
	identitiesEnd := dataOff + 2 + len(identitiesBlock)

	// Walk the PskIdentity entries inside the block [dataOff+2,
	// identitiesEnd) using an independent cursor.
	ic := &cursor{buf: msg, pos: dataOff + 2}
	for ic.pos < identitiesEnd {
		identity, ok := ic.readVector16()
		if !ok || len(identity) == 0 {
			return ErrMalformedClientHello
		}
		age, ok := ic.readUint32()
		if !ok {
			return ErrMalformedClientHello
		}
		p.identities = append(p.identities, pskIdentity{
			identity:            identity,
			obfuscatedTicketAge: age,
		})
	}
	if ic.pos != identitiesEnd {
		return ErrMalformedClientHello
	}
	if len(p.identities) == 0 {
		return ErrMalformedClientHello
	}

	// c.pos now sits at the binders length prefix (identities consumed via a
	// separate cursor, so re-derive it deterministically).
	if identitiesEnd+2 > dataEnd {
		return ErrMalformedClientHello
	}
	p.binderListLengthOffset = identitiesEnd
	binderBlockLen := int(binary.BigEndian.Uint16(msg[identitiesEnd:]))
	p.binderEntriesOffset = identitiesEnd + 2
	if p.binderEntriesOffset+binderBlockLen != dataEnd {
		return ErrMalformedClientHello
	}

	bc := &cursor{buf: msg, pos: p.binderEntriesOffset}
	for bc.pos < dataEnd {
		entryStart := bc.pos
		binder, ok := bc.readVector8()
		if !ok {
			return ErrMalformedClientHello
		}
		if len(binder) != HashLength {
			return ErrBinderLength
		}
		p.binderRanges = append(p.binderRanges, binderRange{
			start: entryStart + 1,
			end:   entryStart + 1 + len(binder),
		})
	}
	if len(p.binderRanges) != len(p.identities) {
		return ErrBinderIndex
	}
	p.binderEntriesEnd = dataEnd
	return nil
}

// TruncatedClientHello returns a freshly allocated copy of the ClientHello
// handshake message with the binders list removed, implementing method 1 of
// RFC 8446 Section 4.2.11.2:
//
//	"the client computes the transcript over the ClientHello with the
//	 ClientHello being truncated to the start of the binders list ... the
//	 length fields ... are adjusted accordingly"
//
// The adjusted length fields are the uint24 handshake length, the uint16
// extensions length and the uint16 pre_shared_key extension_data length. The
// uint16 binders-list length prefix is retained (now describing a zero-length
// list); only the binder entry bytes are discarded. That makes method 1 byte
// for byte identical to method 2 (ZeroedClientHello), whose binder entry
// lengths are retained with their contents zeroed.
func (p *ParsedClientHello) TruncatedClientHello() ([]byte, error) {
	if p.binderListLengthOffset == 0 {
		return nil, ErrNoPreSharedKey
	}
	removed := p.binderEntriesEnd - p.binderEntriesOffset
	truncated := make([]byte, 0, len(p.Raw)-removed)
	truncated = append(truncated, p.Raw[:p.binderEntriesOffset]...)

	old24 := uint32(truncated[p.bodyLengthOffset])<<16 |
		uint32(truncated[p.bodyLengthOffset+1])<<8 |
		uint32(truncated[p.bodyLengthOffset+2])
	new24 := old24 - uint32(removed)
	truncated[p.bodyLengthOffset] = byte(new24 >> 16)
	truncated[p.bodyLengthOffset+1] = byte(new24 >> 8)
	truncated[p.bodyLengthOffset+2] = byte(new24)

	old16 := binary.BigEndian.Uint16(truncated[p.extensionsLengthOffset:])
	binary.BigEndian.PutUint16(truncated[p.extensionsLengthOffset:], old16-uint16(removed))

	old16 = binary.BigEndian.Uint16(truncated[p.pskExtLengthOffset:])
	binary.BigEndian.PutUint16(truncated[p.pskExtLengthOffset:], old16-uint16(removed))

	return truncated, nil
}

// ZeroedClientHello returns a copy of the full message with every binder
// entry's content zeroed but its one-byte length retained, and with the three
// enclosing length fields rewritten as if the binder entry bytes were removed
// (method 2 of Section 4.2.11.2). The RFC states the two transcript
// constructions are equivalent; the rewrite is what makes their byte encoding
// identical apart from the retained zero-length PskBinderEntry placeholders.
func (p *ParsedClientHello) ZeroedClientHello() []byte {
	out := append([]byte(nil), p.Raw...)

	removed := p.binderEntriesEnd - p.binderEntriesOffset

	old24 := uint32(out[p.bodyLengthOffset])<<16 |
		uint32(out[p.bodyLengthOffset+1])<<8 |
		uint32(out[p.bodyLengthOffset+2])
	new24 := old24 - uint32(removed)
	out[p.bodyLengthOffset] = byte(new24 >> 16)
	out[p.bodyLengthOffset+1] = byte(new24 >> 8)
	out[p.bodyLengthOffset+2] = byte(new24)

	old16 := binary.BigEndian.Uint16(out[p.extensionsLengthOffset:])
	binary.BigEndian.PutUint16(out[p.extensionsLengthOffset:], old16-uint16(removed))

	old16 = binary.BigEndian.Uint16(out[p.pskExtLengthOffset:])
	binary.BigEndian.PutUint16(out[p.pskExtLengthOffset:], old16-uint16(removed))

	// The uint16 binders-list length prefix is retained with its original
	// value (it lies outside the removed region); only the binder entry
	// content bytes are zeroed and the logical message truncated at the
	// entries offset, matching method 1 byte for byte.
	for _, r := range p.binderRanges {
		for i := r.start; i < r.end; i++ {
			out[i] = 0
		}
	}
	return out[:p.binderEntriesOffset]
}

// RecomputeBinder recomputes the expected PSK binder for identityIndex over
// the truncated ClientHello transcript:
//
//	binder = HMAC(finished_key,
//	              Transcript-Hash(TruncatedClientHello))
func (p *ParsedClientHello) RecomputeBinder(ks *KeySchedule, identityIndex int) ([]byte, error) {
	if identityIndex < 0 || identityIndex >= len(p.binderRanges) {
		return nil, ErrBinderIndex
	}
	truncated, err := p.TruncatedClientHello()
	if err != nil {
		return nil, err
	}
	return ks.binderMAC(truncated), nil
}

// VerifyBinder recomputes the binder for identityIndex over the truncated
// ClientHello bytes and compares it against the wire value using hmac.Equal,
// which performs a constant-time comparison. It returns ErrBinderMismatch on
// any difference; no material from the expected value leaks through timing.
func (p *ParsedClientHello) VerifyBinder(ks *KeySchedule, identityIndex int) error {
	if identityIndex < 0 || identityIndex >= len(p.binderRanges) {
		return ErrBinderIndex
	}
	expected, err := p.RecomputeBinder(ks, identityIndex)
	if err != nil {
		return err
	}
	r := p.binderRanges[identityIndex]
	actual := p.Raw[r.start:r.end]
	if len(actual) != HashLength || !hmac.Equal(expected, actual) {
		return ErrBinderMismatch
	}
	return nil
}

// VerifyBinderFromPSK is a convenience that builds the KeySchedule for psk and
// verifies the binder at identityIndex in one call.
func (p *ParsedClientHello) VerifyBinderFromPSK(psk []byte, mode PSKMode, identityIndex int) error {
	ks, err := NewKeySchedule(psk, mode)
	if err != nil {
		return err
	}
	return p.VerifyBinder(ks, identityIndex)
}
