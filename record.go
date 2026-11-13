package netsec

import (
	"fmt"
	"strings"
)

// DNS type and class constants used by the admission package.
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypePTR   uint16 = 12
	TypeMX    uint16 = 15
	TypeTXT   uint16 = 16
	TypeAAAA  uint16 = 28

	ClassINET uint16 = 1
)

// SOA contains the RDATA of a DNS SOA record.
type SOA struct {
	MName   string
	RName   string
	Serial  uint32
	Refresh uint32
	Retry   uint32
	Expire  uint32
	Minimum uint32
}

// Equal reports whether two SOA RDATAs are equivalent. Domain names are
// compared case-insensitively because DNS names are case-insensitive.
func (s SOA) Equal(other SOA) bool {
	return nameEqual(s.MName, other.MName) &&
		nameEqual(s.RName, other.RName) &&
		s.Serial == other.Serial &&
		s.Refresh == other.Refresh &&
		s.Retry == other.Retry &&
		s.Expire == other.Expire &&
		s.Minimum == other.Minimum
}

// Record is a simplified DNS resource record. SOA records carry a non-nil
// SOA pointer; other record types use Data as their opaque RDATA.
type Record struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  string
	SOA   *SOA
}

// IsSOA reports whether r is a SOA record with RDATA attached.
func (r Record) IsSOA() bool {
	return r.Type == TypeSOA && r.SOA != nil
}

// Serial returns the SOA serial number when r is a SOA record.
func (r Record) Serial() (uint32, bool) {
	if !r.IsSOA() {
		return 0, false
	}
	return r.SOA.Serial, true
}

// Equal reports whether two records are identical for admission purposes.
// Names are compared case-insensitively; other fields are compared exactly.
func (r Record) Equal(other Record) bool {
	if r.Type != other.Type ||
		r.Class != other.Class ||
		r.TTL != other.TTL ||
		!nameEqual(r.Name, other.Name) {
		return false
	}

	if r.Type == TypeSOA {
		if r.SOA == nil || other.SOA == nil {
			return r.SOA == nil && other.SOA == nil
		}
		return r.SOA.Equal(*other.SOA)
	}

	return r.Data == other.Data
}

// Clone returns a deep copy of r.
func (r Record) Clone() Record {
	out := r
	if r.SOA != nil {
		soa := *r.SOA
		out.SOA = &soa
	}
	return out
}

func (r Record) String() string {
	if r.IsSOA() {
		return fmt.Sprintf("%s %d IN SOA serial=%d", r.Name, r.TTL, r.SOA.Serial)
	}
	return fmt.Sprintf("%s %d IN %d %s", r.Name, r.TTL, r.Type, r.Data)
}

// NewSOA creates an IN-class SOA record with the given serial. The remaining
// SOA RDATA fields are zero; use NewSOAWith for full control.
func NewSOA(name string, serial uint32) Record {
	return Record{
		Name:  name,
		Type:  TypeSOA,
		Class: ClassINET,
		SOA:   &SOA{Serial: serial},
	}
}

// NewSOAWith creates an IN-class SOA record with a complete RDATA value.
func NewSOAWith(name string, ttl uint32, soa SOA) Record {
	soaCopy := soa
	return Record{
		Name:  name,
		Type:  TypeSOA,
		Class: ClassINET,
		TTL:   ttl,
		SOA:   &soaCopy,
	}
}

// NewRR creates an IN-class resource record.
func NewRR(name string, typ uint16, ttl uint32, data string) Record {
	return Record{
		Name:  name,
		Type:  typ,
		Class: ClassINET,
		TTL:   ttl,
		Data:  data,
	}
}

func cloneRecords(records []Record) []Record {
	if records == nil {
		return nil
	}
	out := make([]Record, len(records))
	for i, record := range records {
		out[i] = record.Clone()
	}
	return out
}

func nameEqual(a, b string) bool {
	return normalizeName(a) == normalizeName(b)
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}
