package netsec

// Protocol selects which multicast name-resolution protocol an admission
// decision is being made for.
type Protocol int

const (
	// MDNS is Multicast DNS (RFC 6762). Queries and responses use UDP
	// port 5353 and the 224.0.0.251 / ff02::fb multicast group.
	MDNS Protocol = iota
	// LLMNR is Link-Local Multicast Name Resolution (RFC 4795). Queries
	// use UDP port 5355; unicast responses come back from the same port.
	LLMNR
)

// Standard ports for the supported multicast protocols.
const (
	MDNSPort  = 5353
	LLMNRPort = 5355
	MDNSTTL   = 255 // hop limit a genuine on-link mDNS packet must carry
)

// DNS record type numbers this package understands. Only the types that can
// introduce a name (and therefore participate in additional-section
// relevance tracking) are enumerated explicitly; unknown types are treated
// as opaque data and never followed.
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypePTR   uint16 = 12
	TypeHINFO uint16 = 13
	TypeMX    uint16 = 15
	TypeTXT   uint16 = 16
	TypeAAAA  uint16 = 28
	TypeSRV   uint16 = 33
)

// Question is a single DNS question section entry.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// RecordData holds the RDATA of a resource record. Concrete implementations
// expose the names they reference so that the admission layer can decide
// whether an additional-section record is reachable from the question.
type RecordData interface {
	// targets returns every domain name embedded in the RDATA. Record
	// types without embedded names return nil.
	targets() []string
}

// AData carries an A record (IPv4 address).
type AData struct{ Addr [4]byte }

func (AData) targets() []string { return nil }

// AAAAData carries an AAAA record (IPv6 address).
type AAAAData struct{ Addr [16]byte }

func (AAAAData) targets() []string { return nil }

// CNAMEData carries a CNAME record.
type CNAMEData struct{ Target string }

func (d CNAMEData) targets() []string { return []string{d.Target} }

// PTRData carries a PTR record.
type PTRData struct{ Ptr string }

func (d PTRData) targets() []string { return []string{d.Ptr} }

// SRVData carries an SRV record.
type SRVData struct {
	Priority uint16
	Weight   uint16
	Port     uint16
	Target   string
}

func (d SRVData) targets() []string { return []string{d.Target} }

// MXData carries an MX record.
type MXData struct {
	Pref uint16
	Name string
}

func (d MXData) targets() []string { return []string{d.Name} }

// NSData carries an NS record.
type NSData struct{ Name string }

func (d NSData) targets() []string { return []string{d.Name} }

// SOAData carries the name-bearing fields of an SOA record.
type SOAData struct {
	NS      string
	Mbox    string
	Serial  uint32
	Refresh uint32
	Retry   uint32
	Expire  uint32
	Minimum uint32
}

func (d SOAData) targets() []string { return []string{d.NS, d.Mbox} }

// TXTData carries a TXT record; its text never contains followed names.
type TXTData struct{ Txt [][]byte }

func (TXTData) targets() []string { return nil }

// RawData is the fallback for record types this package does not model.
// Its opaque bytes are never interpreted as names.
type RawData struct{ Bytes []byte }

func (RawData) targets() []string { return nil }

// Record is a single DNS resource record from any message section.
type Record struct {
	Name  string
	Type  uint16
	Class uint16
	// TTL is the DNS record TTL carried in the resource record header,
	// expressed in seconds. It is distinct from the packet-level IP TTL
	// inspected in PacketMeta.
	TTL  uint32
	Data RecordData
}

// Message is the parsed DNS message handed to the multicast response entry
// point. Wire parsing happens in the socket layer; admission only reasons
// about this structured representation.
type Message struct {
	ID         uint16
	Questions  []Question
	Answers    []Record
	Authority  []Record
	Additional []Record
}
