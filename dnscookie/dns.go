package dnscookie

import (
	"encoding/binary"
	"errors"
	"strings"
)

const (
	// RCodeBadCookie is the extended RCODE (23) signalling a missing
	// or invalid Server Cookie (RFC 7873 Section 5.2.3).
	RCodeBadCookie = 23
	RCodeFormErr   = 1
	RCodeNoError   = 0

	optRecordType    = 41
	cookieOptionCode = 10

	headerLen = 12
)

var (
	ErrMessageShort = errors.New("dnscookie: DNS message too short")
	ErrBadName      = errors.New("dnscookie: invalid or compressed-loop name")
)

// Message is a minimal parsed view of a DNS wire message, sufficient
// for cookie policy decisions: header flags, the first question and
// the EDNS OPT record with its COOKIE option.
type Message struct {
	ID                 uint16
	Response           bool
	Opcode             uint8
	Authoritative      bool
	Truncated          bool
	RecursionDesired   bool
	RecursionAvailable bool
	RCODE              uint16 // full extended RCODE

	QName  string
	QType  uint16
	QClass uint16

	AnswerCount     int
	AuthorityCount  int
	AdditionalCount int

	HasOPT      bool
	UDPSize     uint16
	EDNSVersion uint8
	DNSSECOK    bool

	HasCookieOption bool
	ClientCookie    []byte
	ServerCookie    []byte

	Raw         []byte
	questionEnd int
}

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }

// decodeName decodes a possibly compressed domain name at off. It
// returns the presentation-format name (with trailing dot) and the
// offset immediately after the name in the original stream.
func decodeName(msg []byte, off int) (string, int, error) {
	var labels []string
	next := -1
	steps := 0
	for {
		if off >= len(msg) {
			return "", 0, ErrBadName
		}
		steps++
		if steps > 128 {
			return "", 0, ErrBadName
		}
		l := int(msg[off])
		switch {
		case l&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, ErrBadName
			}
			ptr := (l&0x3F)<<8 | int(msg[off+1])
			if next < 0 {
				next = off + 2
			}
			off = ptr
		case l == 0:
			if next < 0 {
				next = off + 1
			}
			if len(labels) == 0 {
				return ".", next, nil
			}
			return strings.Join(labels, ".") + ".", next, nil
		case l&0xC0 == 0:
			if off+1+l > len(msg) {
				return "", 0, ErrBadName
			}
			labels = append(labels, string(msg[off+1:off+1+l]))
			off += 1 + l
		default:
			return "", 0, ErrBadName
		}
	}
}

// encodeName converts a presentation-format name to wire format.
func encodeName(name string) ([]byte, error) {
	if name == "" || name == "." {
		return []byte{0}, nil
	}
	name = strings.TrimSuffix(name, ".")
	var out []byte
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, ErrBadName
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0), nil
}

func skipRR(msg []byte, off int) (int, error) {
	_, next, err := decodeName(msg, off)
	if err != nil {
		return 0, err
	}
	off = next
	if off+10 > len(msg) {
		return 0, ErrMessageShort
	}
	rdlen := int(be16(msg[off+8:]))
	off += 10
	if off+rdlen > len(msg) {
		return 0, ErrMessageShort
	}
	return off + rdlen, nil
}

// ParseMessage parses a DNS wire message into a Message view.
func ParseMessage(msg []byte) (*Message, error) {
	if len(msg) < headerLen {
		return nil, ErrMessageShort
	}
	m := &Message{Raw: msg}
	m.ID = be16(msg[0:])
	flags := be16(msg[2:])
	m.Response = flags&0x8000 != 0
	m.Opcode = uint8(flags>>11) & 0xF
	m.Authoritative = flags&0x0400 != 0
	m.Truncated = flags&0x0200 != 0
	m.RecursionDesired = flags&0x0100 != 0
	m.RecursionAvailable = flags&0x0080 != 0
	headerRCODE := flags & 0xF

	qd, an, ns, ar := int(be16(msg[4:])), int(be16(msg[6:])), int(be16(msg[8:])), int(be16(msg[10:]))
	m.AnswerCount, m.AuthorityCount, m.AdditionalCount = an, ns, ar

	off := headerLen
	for i := 0; i < qd; i++ {
		name, next, err := decodeName(msg, off)
		if err != nil {
			return nil, err
		}
		off = next
		if off+4 > len(msg) {
			return nil, ErrMessageShort
		}
		if i == 0 {
			m.QName = name
			m.QType = be16(msg[off:])
			m.QClass = be16(msg[off+2:])
		}
		off += 4
	}
	m.questionEnd = off

	var err error
	for i := 0; i < an+ns; i++ {
		if off, err = skipRR(msg, off); err != nil {
			return nil, err
		}
	}
	for i := 0; i < ar; i++ {
		_, next, err := decodeName(msg, off)
		if err != nil {
			return nil, err
		}
		off = next
		if off+10 > len(msg) {
			return nil, ErrMessageShort
		}
		typ := be16(msg[off:])
		class := be16(msg[off+2:])
		ttl := binary.BigEndian.Uint32(msg[off+4:])
		rdlen := int(be16(msg[off+8:]))
		off += 10
		if off+rdlen > len(msg) {
			return nil, ErrMessageShort
		}
		rdata := msg[off : off+rdlen]
		off += rdlen

		if typ != optRecordType {
			continue
		}
		m.HasOPT = true
		m.UDPSize = class
		m.EDNSVersion = uint8(ttl >> 16)
		m.DNSSECOK = ttl&0x8000 != 0
		m.RCODE = uint16(ttl>>24)<<4 | headerRCODE

		for len(rdata) >= 4 {
			code := be16(rdata[0:])
			ln := int(be16(rdata[2:]))
			rdata = rdata[4:]
			if ln > len(rdata) {
				return nil, ErrMessageShort
			}
			data := rdata[:ln]
			rdata = rdata[ln:]
			if code == cookieOptionCode {
				m.HasCookieOption = true
				if len(data) >= ClientCookieLen {
					m.ClientCookie = append([]byte(nil), data[:ClientCookieLen]...)
					m.ServerCookie = append([]byte(nil), data[ClientCookieLen:]...)
				} else {
					m.ClientCookie = append([]byte(nil), data...)
				}
			}
		}
	}
	if !m.HasOPT {
		m.RCODE = headerRCODE
	}
	return m, nil
}

// cookieOptionData builds the COOKIE option payload.
func cookieOptionData(clientCookie, serverCookie []byte) []byte {
	data := make([]byte, 0, len(clientCookie)+len(serverCookie))
	data = append(data, clientCookie...)
	data = append(data, serverCookie...)
	return data
}

func appendOPTRecord(out []byte, udpSize uint16, extRCODE uint8, options []byte) []byte {
	out = append(out, 0) // root name
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], optRecordType)
	out = append(out, tmp[:]...)
	binary.BigEndian.PutUint16(tmp[:], udpSize)
	out = append(out, tmp[:]...)
	var ttl [4]byte
	binary.BigEndian.PutUint32(ttl[:], uint32(extRCODE)<<24)
	out = append(out, ttl[:]...)
	binary.BigEndian.PutUint16(tmp[:], uint16(len(options)))
	out = append(out, tmp[:]...)
	return append(out, options...)
}

func appendCookieOption(out, clientCookie, serverCookie []byte) []byte {
	data := cookieOptionData(clientCookie, serverCookie)
	var tmp [4]byte
	binary.BigEndian.PutUint16(tmp[0:], cookieOptionCode)
	binary.BigEndian.PutUint16(tmp[2:], uint16(len(data)))
	out = append(out, tmp[:]...)
	return append(out, data...)
}

// BuildQuery serializes a DNS query carrying the given cookies.
func BuildQuery(id uint16, qname string, qtype uint16, clientCookie, serverCookie []byte, udpSize uint16) ([]byte, error) {
	name, err := encodeName(qname)
	if err != nil {
		return nil, err
	}
	out := make([]byte, headerLen)
	binary.BigEndian.PutUint16(out[0:], id)
	binary.BigEndian.PutUint16(out[2:], 0x0100) // RD
	binary.BigEndian.PutUint16(out[4:], 1)      // QDCOUNT
	binary.BigEndian.PutUint16(out[10:], 1)     // ARCOUNT (OPT)
	out = append(out, name...)
	var tmp [4]byte
	binary.BigEndian.PutUint16(tmp[0:], qtype)
	binary.BigEndian.PutUint16(tmp[2:], 1) // IN
	out = append(out, tmp[:]...)

	opts := appendCookieOption(nil, clientCookie, serverCookie)
	out = appendOPTRecord(out, udpSize, 0, opts)
	return out, nil
}

// ResponseOptions controls NewResponse serialization.
type ResponseOptions struct {
	RCODE        uint16 // full extended RCODE (e.g. RCodeBadCookie)
	Truncated    bool
	ServerCookie []byte // appended to the echoed Client Cookie
	Answer       []byte // raw answer-section bytes, may be nil
	AnswerCount  uint16
}

// NewResponse builds a response to the parsed query q, echoing the
// question section and the Client Cookie, and attaching the EDNS OPT
// record with the (possibly extended) RCODE.
func (q *Message) NewResponse(opts ResponseOptions) []byte {
	out := make([]byte, headerLen)
	binary.BigEndian.PutUint16(out[0:], q.ID)
	flags := uint16(0x8000) | uint16(q.Opcode&0xF)<<11 // QR
	if q.RecursionDesired {
		flags |= 0x0100
	}
	flags |= 0x0080 // RA
	if opts.Truncated {
		flags |= 0x0200
	}
	flags |= opts.RCODE & 0xF
	binary.BigEndian.PutUint16(out[2:], flags)
	qd := uint16(0)
	if q.questionEnd > headerLen {
		qd = 1
	}
	binary.BigEndian.PutUint16(out[4:], qd)
	binary.BigEndian.PutUint16(out[6:], opts.AnswerCount)
	binary.BigEndian.PutUint16(out[10:], 1) // OPT
	if qd == 1 {
		out = append(out, q.Raw[headerLen:q.questionEnd]...)
	}
	out = append(out, opts.Answer...)

	var optData []byte
	if q.HasCookieOption && len(q.ClientCookie) == ClientCookieLen {
		optData = appendCookieOption(nil, q.ClientCookie, opts.ServerCookie)
	}
	out = appendOPTRecord(out, q.UDPSize, uint8(opts.RCODE>>4), optData)
	return out
}
