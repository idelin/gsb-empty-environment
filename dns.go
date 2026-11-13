package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// DNS TYPE and CLASS values used by the package.
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypePTR   uint16 = 12
	TypeMX    uint16 = 15
	TypeTXT   uint16 = 16
	TypeAAAA  uint16 = 28
	TypeOPT   uint16 = 41
	TypeANY   uint16 = 255

	ClassINET uint16 = 1
)

// DNS response codes.
const (
	RCodeNoError  uint16 = 0
	RCodeFormErr  uint16 = 1
	RCodeServFail uint16 = 2
	RCodeNXDomain uint16 = 3
	RCodeNotImp   uint16 = 4
	RCodeRefused  uint16 = 5
)

const (
	headerLen       = 12
	maxNameLen      = 255
	maxLabelLen     = 63
	maxDNSMessage   = 65535
	flagResponse    = 1 << 15
	flagAuthoritive = 1 << 10
	flagRecursion   = 1 << 8
	flagRecAvail    = 1 << 7
)

var (
	// ErrMalformed is returned for packets that are not valid DNS queries.
	ErrMalformed = errors.New("netsec: malformed DNS message")
	// ErrNotQuery is returned when a packet is not a query (QR is set).
	ErrNotQuery = errors.New("netsec: DNS message is not a query")
)

// Question is a single DNS question.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// Record is a DNS resource record. Data is the RDATA in wire format. Domain
// names embedded in RDATA must be uncompressed, which is what the helpers in
// this package produce.
//
// TTL is kept for the resolver's internal cache bookkeeping. Responses built
// by Handler always replace it with the configured response TTL.
type Record struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  []byte
}

// ARecord builds an IN A record. addr must be a valid IPv4 address.
func ARecord(name string, addr netip.Addr) (Record, error) {
	if !addr.Is4() {
		return Record{}, fmt.Errorf("netsec: %q is not an IPv4 address", addr)
	}
	value := addr.As4()
	return Record{
		Name:  name,
		Type:  TypeA,
		Class: ClassINET,
		Data:  value[:],
	}, nil
}

// AAAARecord builds an IN AAAA record. addr must be a valid IPv6 address.
func AAAARecord(name string, addr netip.Addr) (Record, error) {
	if !addr.Is6() || addr.Is4() {
		return Record{}, fmt.Errorf("netsec: %q is not an IPv6 address", addr)
	}
	value := addr.As16()
	return Record{
		Name:  name,
		Type:  TypeAAAA,
		Class: ClassINET,
		Data:  value[:],
	}, nil
}

// CNAME records use a domain name in RDATA. This helper encodes target
// without compression.
func CNAMERecord(name, target string) (Record, error) {
	data, err := encodeName(target)
	if err != nil {
		return Record{}, err
	}
	return Record{Name: name, Type: TypeCNAME, Class: ClassINET, Data: data}, nil
}

// NSRecord builds an IN NS record.
func NSRecord(name, target string) (Record, error) {
	data, err := encodeName(target)
	if err != nil {
		return Record{}, err
	}
	return Record{Name: name, Type: TypeNS, Class: ClassINET, Data: data}, nil
}

// PTRRecord builds an IN PTR record.
func PTRRecord(name, target string) (Record, error) {
	data, err := encodeName(target)
	if err != nil {
		return Record{}, err
	}
	return Record{Name: name, Type: TypePTR, Class: ClassINET, Data: data}, nil
}

// SOARecord builds an IN SOA record.
func SOARecord(name, primary, mailbox string, serial, refresh, retry, expire, minimum uint32) (Record, error) {
	primaryName, err := encodeName(primary)
	if err != nil {
		return Record{}, err
	}
	mailboxName, err := encodeName(mailbox)
	if err != nil {
		return Record{}, err
	}
	data := append(append([]byte(nil), primaryName...), mailboxName...)
	for _, value := range []uint32{serial, refresh, retry, expire, minimum} {
		data = append(data, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
	}
	return Record{Name: name, Type: TypeSOA, Class: ClassINET, Data: data}, nil
}

// TXTRecord builds an IN TXT record from one or more character strings.
func TXTRecord(name string, values ...string) (Record, error) {
	var data []byte
	for _, value := range values {
		if len(value) > 255 {
			return Record{}, fmt.Errorf("netsec: TXT string exceeds 255 bytes")
		}
		data = append(data, byte(len(value)))
		data = append(data, value...)
	}
	if len(data) == 0 {
		data = []byte{0}
	}
	return Record{Name: name, Type: TypeTXT, Class: ClassINET, Data: data}, nil
}

// Resolution is the result of a cache, upstream, or authoritative lookup.
type Resolution struct {
	RCode         uint16
	Authoritative bool
	Answer        []Record
	Authority     []Record
	Additional    []Record
}

// Request is a DNS query together with the source address observed by the
// server. Client must never be taken from an untrusted DNS header field.
type Request struct {
	ID               uint16
	Opcode           uint8
	Client           netip.Addr
	Question         Question
	RecursionDesired bool
}

// Response is a complete DNS response.
type Response struct {
	ID                 uint16
	Opcode             uint8
	RCode              uint16
	Authoritative      bool
	RecursionDesired   bool
	RecursionAvailable bool
	Question           []Question
	Answer             []Record
	Authority          []Record
	Additional         []Record
}

// ParseQuery decodes one DNS question and the flags needed for admission.
// It intentionally does not decode the answer, authority, or additional
// sections of the query.
func ParseQuery(packet []byte) (Request, error) {
	if len(packet) < headerLen {
		return Request{}, ErrMalformed
	}
	flags := binary.BigEndian.Uint16(packet[2:4])
	if flags&flagResponse != 0 {
		return Request{}, ErrNotQuery
	}
	if qdCount := binary.BigEndian.Uint16(packet[4:6]); qdCount != 1 {
		return Request{}, fmt.Errorf("%w: expected one question, got %d", ErrMalformed, qdCount)
	}
	name, offset, err := parseName(packet, headerLen)
	if err != nil {
		return Request{}, err
	}
	if offset+4 > len(packet) {
		return Request{}, ErrMalformed
	}
	qtype := binary.BigEndian.Uint16(packet[offset : offset+2])
	qclass := binary.BigEndian.Uint16(packet[offset+2 : offset+4])
	return Request{
		ID:               binary.BigEndian.Uint16(packet[0:2]),
		Opcode:           uint8(flags>>11) & 0x0f,
		Question:         Question{Name: name, Type: qtype, Class: qclass},
		RecursionDesired: flags&flagRecursion != 0,
	}, nil
}

// EncodeResponse serializes a response. All names are written without
// compression; this is valid DNS and keeps the codec independent of resolver
// data structures.
func EncodeResponse(response Response) ([]byte, error) {
	if len(response.Question) > 65535 || len(response.Answer) > 65535 ||
		len(response.Authority) > 65535 || len(response.Additional) > 65535 {
		return nil, fmt.Errorf("netsec: too many DNS sections")
	}
	body := make([]byte, 0, 128)
	for _, question := range response.Question {
		name, err := encodeName(question.Name)
		if err != nil {
			return nil, err
		}
		body = append(body, name...)
		body = append(body,
			byte(question.Type>>8), byte(question.Type),
			byte(question.Class>>8), byte(question.Class),
		)
	}
	for _, section := range [][]Record{response.Answer, response.Authority, response.Additional} {
		for _, record := range section {
			if len(record.Data) > 65535 {
				return nil, fmt.Errorf("netsec: RDATA exceeds 65535 bytes")
			}
			name, err := encodeName(record.Name)
			if err != nil {
				return nil, err
			}
			body = append(body, name...)
			body = append(body,
				byte(record.Type>>8), byte(record.Type),
				byte(record.Class>>8), byte(record.Class),
				byte(record.TTL>>24), byte(record.TTL>>16), byte(record.TTL>>8), byte(record.TTL),
				byte(len(record.Data)>>8), byte(len(record.Data)),
			)
			body = append(body, record.Data...)
		}
	}
	if headerLen+len(body) > maxDNSMessage {
		return nil, fmt.Errorf("netsec: DNS response exceeds 65535 bytes")
	}

	flags := uint16(flagResponse)
	flags |= (uint16(response.Opcode) & 0x0f) << 11
	if response.Authoritative {
		flags |= flagAuthoritive
	}
	if response.RecursionDesired {
		flags |= flagRecursion
	}
	if response.RecursionAvailable {
		flags |= flagRecAvail
	}
	flags |= response.RCode & 0x0f

	wire := make([]byte, headerLen, headerLen+len(body))
	binary.BigEndian.PutUint16(wire[0:2], response.ID)
	binary.BigEndian.PutUint16(wire[2:4], flags)
	binary.BigEndian.PutUint16(wire[4:6], uint16(len(response.Question)))
	binary.BigEndian.PutUint16(wire[6:8], uint16(len(response.Answer)))
	binary.BigEndian.PutUint16(wire[8:10], uint16(len(response.Authority)))
	binary.BigEndian.PutUint16(wire[10:12], uint16(len(response.Additional)))
	wire = append(wire, body...)
	return wire, nil
}

func encodeName(name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("netsec: empty DNS name")
	}
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return []byte{0}, nil
	}
	if len(name) > maxNameLen {
		return nil, fmt.Errorf("netsec: DNS name exceeds 255 bytes")
	}
	labels := strings.Split(name, ".")
	out := make([]byte, 0, len(name)+2)
	for _, label := range labels {
		if label == "" {
			return nil, fmt.Errorf("netsec: empty label in %q", name)
		}
		if len(label) > maxLabelLen {
			return nil, fmt.Errorf("netsec: label exceeds 63 bytes")
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	out = append(out, 0)
	if len(out) > maxNameLen {
		return nil, fmt.Errorf("netsec: encoded DNS name exceeds 255 bytes")
	}
	return out, nil
}

func parseName(message []byte, start int) (string, int, error) {
	if start < 0 || start >= len(message) {
		return "", 0, ErrMalformed
	}
	var labels []string
	offset := start
	next := -1
	steps := 0
	nameLength := 0
	seen := make(map[int]struct{})
	for {
		if offset >= len(message) {
			return "", 0, ErrMalformed
		}
		length := int(message[offset])
		switch {
		case length == 0:
			offset++
			if next < 0 {
				next = offset
			}
			if len(labels) == 0 {
				return ".", next, nil
			}
			return strings.Join(labels, ".") + ".", next, nil
		case length&0xc0 == 0xc0:
			if offset+2 > len(message) {
				return "", 0, ErrMalformed
			}
			pointer := int(message[offset]&0x3f)<<8 | int(message[offset+1])
			if pointer >= len(message) {
				return "", 0, ErrMalformed
			}
			if next < 0 {
				next = offset + 2
			}
			if _, ok := seen[pointer]; ok {
				return "", 0, fmt.Errorf("%w: DNS compression loop", ErrMalformed)
			}
			seen[pointer] = struct{}{}
			offset = pointer
			steps++
			if steps > 128 {
				return "", 0, fmt.Errorf("%w: too many DNS compression pointers", ErrMalformed)
			}
		case length&0xc0 != 0:
			return "", 0, ErrMalformed
		default:
			if length > maxLabelLen || offset+1+length > len(message) {
				return "", 0, ErrMalformed
			}
			nameLength += length + 1
			if nameLength+1 > maxNameLen {
				return "", 0, fmt.Errorf("%w: DNS name exceeds 255 bytes", ErrMalformed)
			}
			label := string(message[offset+1 : offset+1+length])
			labels = append(labels, label)
			offset += 1 + length
			if len(labels) > 128 {
				return "", 0, ErrMalformed
			}
		}
	}
}

func decodeRDataName(data []byte) (string, error) {
	name, next, err := parseName(data, 0)
	if err != nil {
		return "", err
	}
	if next != len(data) {
		return "", fmt.Errorf("netsec: trailing bytes after domain name in RDATA")
	}
	return name, nil
}

func canonicalName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("netsec: empty DNS name")
	}
	if name == "." {
		return ".", nil
	}
	trimmed := strings.TrimSuffix(name, ".")
	if trimmed == "" {
		return ".", nil
	}
	encoded, err := encodeName(trimmed)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxNameLen {
		return "", fmt.Errorf("netsec: DNS name exceeds 255 bytes")
	}
	return asciiLower(trimmed) + ".", nil
}

func asciiLower(value string) string {
	var out []byte
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'A' && ch <= 'Z' {
			if out == nil {
				out = []byte(value)
			}
			out[i] = ch + ('a' - 'A')
		}
	}
	if out == nil {
		return value
	}
	return string(out)
}

func cloneRecords(records []Record) []Record {
	if records == nil {
		return nil
	}
	out := make([]Record, len(records))
	for i, record := range records {
		out[i] = record
		out[i].Data = append([]byte(nil), record.Data...)
	}
	return out
}
