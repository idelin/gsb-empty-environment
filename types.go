package netsec

// DNS record types used by the admission logic.
const (
	TypeA      uint16 = 1
	TypeNS     uint16 = 2
	TypeCNAME  uint16 = 5
	TypeSOA    uint16 = 6
	TypePTR    uint16 = 12
	TypeMX     uint16 = 15
	TypeTXT    uint16 = 16
	TypeAAAA   uint16 = 28
	TypeSRV    uint16 = 33
	TypeDNAME  uint16 = 39
	TypeDS     uint16 = 43
	TypeRRSIG  uint16 = 46
	TypeDNSKEY uint16 = 48
)

// ClassIN is the Internet class; the only class this component handles.
const ClassIN uint16 = 1

// Question is the question section of a DNS message.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// RR is a resource record in a resolver-friendly, already decompressed
// form. Owner and RData are canonicalized on admission, so callers may
// use any case and may omit the trailing dot.
type RR struct {
	Owner string
	Type  uint16
	Class uint16
	TTL   uint32
	RData string // presentation form; names inside are canonicalized
}

// Response is a parsed DNS response from an authoritative server.
type Response struct {
	Authoritative bool
	RCode         int
	// DNSSECValid reports that the resolver has validated the response's
	// in-zone data against the answering zone's DNSSEC keys. Validation
	// never extends the bailiwick: out-of-scope records stay hints.
	DNSSECValid bool
	Question    Question
	Answer      []RR
	Authority   []RR
	Additional  []RR
}
