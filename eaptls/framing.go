package eaptls

import "errors"

// EAP packet codes (RFC 3748, section 4).
const (
	CodeRequest  byte = 1
	CodeResponse byte = 2
	CodeSuccess  byte = 3
	CodeFailure  byte = 4
)

// EAP type for TLS (RFC 5216).
const TypeEAPTLS byte = 13

// EAP-TLS flags (RFC 5216, section 3.1).
const (
	flagLengthIncluded byte = 0x80
	flagMoreFragments  byte = 0x40
	flagStart          byte = 0x20
)

const (
	eapHeaderLen             = 4
	eapTLSDataOffset         = 6  // header + type + flags, no TLS message length
	eapTLSDataOffsetLen      = 10 // header + type + flags + 4-byte TLS message length
	tlsRecordHeaderLen       = 5
	tlsHandshakeTypeLen      = 4 // handshake header without the variable body
	handshakeFinished   byte = 20
)

// Packet is a parsed EAP packet relevant to EAP-TLS.
type Packet struct {
	Code       byte
	Identifier byte
	// Type is the EAP type for Request/Response packets; zero otherwise.
	Type byte
	// Flags is the EAP-TLS flags byte; zero for non-EAP-TLS packets.
	Flags byte
	// Data is the carried TLS record stream (after fragment reassembly by
	// the caller's transport when needed) for EAP-TLS packets; nil otherwise.
	Data []byte
}

// ErrMalformedPacket reports an EAP packet that cannot be parsed.
var ErrMalformedPacket = errors.New("eaptls: malformed EAP packet")

// ParsePacket parses an EAP packet. EAP-TLS fragments are returned as-is;
// fragment reassembly is performed by [State.HandleRequest].
func ParsePacket(buf []byte) (*Packet, error) {
	if len(buf) < eapHeaderLen {
		return nil, ErrMalformedPacket
	}
	p := &Packet{Code: buf[0], Identifier: buf[1]}
	declaredLen := int(buf[2])<<8 | int(buf[3])
	if declaredLen < eapHeaderLen || declaredLen > len(buf) {
		return nil, ErrMalformedPacket
	}

	switch p.Code {
	case CodeSuccess, CodeFailure:
		if declaredLen != eapHeaderLen {
			return nil, ErrMalformedPacket
		}
		return p, nil
	case CodeRequest, CodeResponse:
		if declaredLen < eapTLSDataOffset {
			return nil, ErrMalformedPacket
		}
		p.Type = buf[4]
		if p.Type != TypeEAPTLS {
			return p, nil
		}
		p.Flags = buf[5]
		off := eapTLSDataOffset
		if p.Flags&flagLengthIncluded != 0 {
			if declaredLen < eapTLSDataOffsetLen {
				return nil, ErrMalformedPacket
			}
			off = eapTLSDataOffsetLen
		}
		p.Data = append([]byte(nil), buf[off:declaredLen]...)
		return p, nil
	default:
		return nil, ErrMalformedPacket
	}
}

// CommitmentFrame builds the empty EAP-TLS response (the commitment message
// of RFC 8996, section 2.5) using the given identifier, which must be the
// identifier of the request that carried the final handshake message.
func CommitmentFrame(identifier byte) []byte {
	return []byte{CodeResponse, identifier, 0x00, 0x06, TypeEAPTLS, 0x00}
}

// containsPlaintextFinished reports whether data contains a TLS record stream
// carrying a handshake Finished message in the clear. Real TLS 1.3 server
// flights are encrypted, so the EAP method normally signals the final
// handshake explicitly via [State.MarkFinalHandshake]; this helper covers
// stacks that expose handshake records before encryption (and is used by the
// state machine's own tests).
func containsPlaintextFinished(data []byte) bool {
	for len(data) >= tlsRecordHeaderLen {
		recordLen := int(data[3])<<8 | int(data[4])
		if tlsRecordHeaderLen+recordLen > len(data) {
			return false
		}
		body := data[tlsRecordHeaderLen : tlsRecordHeaderLen+recordLen]
		if data[0] == 0x16 { // handshake record
			for len(body) >= tlsHandshakeTypeLen {
				msgLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
				if tlsHandshakeTypeLen+msgLen > len(body) {
					break
				}
				if body[0] == handshakeFinished {
					return true
				}
				body = body[tlsHandshakeTypeLen+msgLen:]
			}
		}
		data = data[tlsRecordHeaderLen+recordLen:]
	}
	return false
}
