package dnssec

import "errors"

// DNS class and type constants used by the validator.
const (
	ClassIN = 1

	TypeA          = 1
	TypeNS         = 2
	TypeCNAME      = 5
	TypeSOA        = 6
	TypePTR        = 12
	TypeMX         = 15
	TypeTXT        = 16
	TypeAAAA       = 28
	TypeSRV        = 33
	TypeDNSKEY     = 48
	TypeRRSIG      = 46
	TypeNSEC       = 47
	TypeDS         = 43
	TypeNSEC3      = 50
	TypeNSEC3PARAM = 51
)

// Header flag bits.
const (
	FlagQR     = 1 << 15
	FlagAA     = 1 << 10
	FlagTC     = 1 << 9
	FlagRD     = 1 << 8
	FlagRA     = 1 << 7
	FlagAD     = 1 << 5
	RcodeMask  = 0xF
	RcodeOK    = 0
	RcodeNXDOM = 3
)

// RR is a generic parsed resource record. RDATA is kept in raw wire form;
// structured access is provided by the dedicated record parsers.
type RR struct {
	Name  Name
	Type  uint16
	Class uint16
	TTL   uint32
	Rdata []byte
}

// Question is a parsed question section entry.
type Question struct {
	Name  Name
	Type  uint16
	Class uint16
}

// Message is a minimally parsed DNS message covering all sections.
type Message struct {
	ID         uint16
	Flags      uint16
	Question   []Question
	Answer     []RR
	Authority  []RR
	Additional []RR
}

var ErrShortMessage = errors.New("dnssec: message too short")

// Rcode extracts the 4-bit RCODE from the flags field.
func (m *Message) Rcode() int { return int(m.Flags & RcodeMask) }

// AllRRs returns every record carried by the message.
func (m *Message) AllRRs() []RR {
	out := make([]RR, 0, len(m.Answer)+len(m.Authority)+len(m.Additional))
	out = append(out, m.Answer...)
	out = append(out, m.Authority...)
	out = append(out, m.Additional...)
	return out
}

// ParseMessage parses a full DNS wire message, transparently following name
// compression pointers.
func ParseMessage(wire []byte) (*Message, error) {
	if len(wire) < 12 {
		return nil, ErrShortMessage
	}
	m := &Message{
		ID:    uint16(wire[0])<<8 | uint16(wire[1]),
		Flags: uint16(wire[2])<<8 | uint16(wire[3]),
	}
	qdcount := int(uint16(wire[4])<<8 | uint16(wire[5]))
	ancount := int(uint16(wire[6])<<8 | uint16(wire[7]))
	nscount := int(uint16(wire[8])<<8 | uint16(wire[9]))
	arcount := int(uint16(wire[10])<<8 | uint16(wire[11]))

	off := 12
	var err error
	for i := 0; i < qdcount; i++ {
		q := Question{}
		if q.Name, off, err = readName(wire, off); err != nil {
			return nil, err
		}
		if off+4 > len(wire) {
			return nil, ErrShortMessage
		}
		q.Type = uint16(wire[off])<<8 | uint16(wire[off+1])
		q.Class = uint16(wire[off+2])<<8 | uint16(wire[off+3])
		off += 4
		m.Question = append(m.Question, q)
	}

	sections := [][]RR{m.Answer, m.Authority, m.Additional}
	counts := [3]int{ancount, nscount, arcount}
	for s := 0; s < 3; s++ {
		for i := 0; i < counts[s]; i++ {
			rr := RR{}
			if rr.Name, off, err = readName(wire, off); err != nil {
				return nil, err
			}
			if off+10 > len(wire) {
				return nil, ErrShortMessage
			}
			rr.Type = uint16(wire[off])<<8 | uint16(wire[off+1])
			rr.Class = uint16(wire[off+2])<<8 | uint16(wire[off+3])
			rr.TTL = uint32(wire[off+4])<<24 | uint32(wire[off+5])<<16 |
				uint32(wire[off+6])<<8 | uint32(wire[off+7])
			rdlen := int(uint16(wire[off+8])<<8 | uint16(wire[off+9]))
			off += 10
			if off+rdlen > len(wire) {
				return nil, ErrShortMessage
			}
			rr.Rdata = make([]byte, rdlen)
			copy(rr.Rdata, wire[off:off+rdlen])
			off += rdlen
			sections[s] = append(sections[s], rr)
		}
	}
	m.Answer, m.Authority, m.Additional = sections[0], sections[1], sections[2]
	return m, nil
}

// RRSetByNameType collects records with the given owner and type from the
// supplied records. RRSIG records are never included.
func RRSetByNameType(rrs []RR, owner Name, typ uint16) []RR {
	var out []RR
	for _, rr := range rrs {
		if rr.Type == typ && rr.Name.Equal(owner) {
			out = append(out, rr)
		}
	}
	return out
}

// RRSIGsFor collects RRSIG records covering typ owned by owner.
func RRSIGsFor(rrs []RR, owner Name, typ uint16) []RR {
	var out []RR
	for _, rr := range rrs {
		if rr.Type != TypeRRSIG || !rr.Name.Equal(owner) {
			continue
		}
		sig, err := ParseRRSIG(rr.Rdata)
		if err != nil {
			continue
		}
		if sig.TypeCovered == typ {
			out = append(out, rr)
		}
	}
	return out
}
