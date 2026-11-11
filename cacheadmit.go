// Package cacheadmit implements cache admission control for a recursive
// DNS resolver. It decides which records of a received response may enter
// the cache, defending against cache poisoning: every record is checked
// against the bailiwick of the queried delegation, the message envelope
// (transaction ID, source port, 0x20 case echo) is verified, cached data
// is only replaced by equally or more trustworthy data, and CNAME chains
// are only followed along verified names.
package cacheadmit

import (
	"errors"
	"fmt"
	"strings"
)

// DNS record types relevant to admission decisions.
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypeAAAA  uint16 = 28
)

// ClassIN is the internet class.
const ClassIN uint16 = 1

// Trust ranks how credible a cached record is. Higher wins: an entry is
// only replaced by data of equal or greater trust.
type Trust int

const (
	TrustNone Trust = iota
	// TrustGlue is in-bailiwick glue from the additional section of a
	// referral, referenced by an NS record of the same response.
	TrustGlue
	// TrustAnswer is data from the answer section of a
	// non-authoritative response.
	TrustAnswer
	// TrustAuthAuthority is delegation data (NS/SOA) from the
	// authority section.
	TrustAuthAuthority
	// TrustAuthAnswer is data from the answer section of a response
	// with the authoritative-answer bit set.
	TrustAuthAnswer
)

func (t Trust) String() string {
	switch t {
	case TrustNone:
		return "none"
	case TrustGlue:
		return "glue"
	case TrustAnswer:
		return "answer"
	case TrustAuthAuthority:
		return "auth-authority"
	case TrustAuthAnswer:
		return "auth-answer"
	}
	return "unknown"
}

// Record is a resource record. Data holds the rdata in text form: a
// domain name for NS/CNAME, an address for A/AAAA.
type Record struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  string
}

// Question is the question section of a DNS message.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// Message is a received DNS response.
type Message struct {
	ID            uint16
	SourcePort    uint16
	Authoritative bool
	Questions     []Question
	Answers       []Record
	Authority     []Record
	Additional    []Record
}

// Query describes an outstanding query a response is matched against.
type Query struct {
	ID   uint16
	Port uint16
	// Name is the qname exactly as sent on the wire, including the
	// randomized 0x20 capitalization.
	Name  string
	Type  uint16
	Class uint16
	// Zone is the zone of the authority server being queried; it
	// defines the bailiwick every admitted record must fall into.
	Zone string
}

// Rejection records why a single record was refused.
type Rejection struct {
	Record Record
	Reason string
}

// Admission is the outcome of admitting one response into the cache.
type Admission struct {
	// Stored records made it into the cache.
	Stored []Record
	// Rejected records were refused, with reasons.
	Rejected []Rejection
	// Chain holds the verified CNAME chain as canonical owner names,
	// starting at the qname.
	Chain []string
	// Loop is true when the CNAME chain loops back to a name already
	// on the chain; following stops there.
	Loop bool
}

// ErrEnvelope is returned when a response does not match the outstanding
// query (transaction ID, source port, question, or 0x20 case echo) and
// must be discarded wholesale.
var ErrEnvelope = errors.New("cacheadmit: response does not match outstanding query")

type cacheKey struct {
	name  string
	typ   uint16
	class uint16
}

// Entry is a cached record set with its trust level.
type Entry struct {
	Records []Record
	Trust   Trust
}

// Cache is a trust-aware DNS cache.
type Cache struct {
	entries map[cacheKey]*Entry
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[cacheKey]*Entry)}
}

// Lookup returns the cached record set for name/typ (class IN).
func (c *Cache) Lookup(name string, typ uint16) ([]Record, Trust, bool) {
	e, ok := c.entries[cacheKey{name: canon(name), typ: typ, class: ClassIN}]
	if !ok {
		return nil, TrustNone, false
	}
	return e.Records, e.Trust, true
}

// canon lowercases a domain name and appends the root dot.
func canon(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "."
}

// inBailiwick reports whether canonical name is at or below canonical zone.
func inBailiwick(name, zone string) bool {
	if zone == "." {
		return true
	}
	return name == zone || strings.HasSuffix(name, "."+zone)
}

// checkEnvelope verifies the response matches the outstanding query:
// transaction ID, source port, question count/type/class, and the 0x20
// case echo of the qname.
func checkEnvelope(q *Query, m *Message) error {
	if m.ID != q.ID {
		return fmt.Errorf("%w: transaction ID %d, want %d", ErrEnvelope, m.ID, q.ID)
	}
	if m.SourcePort != q.Port {
		return fmt.Errorf("%w: source port %d, want %d", ErrEnvelope, m.SourcePort, q.Port)
	}
	if len(m.Questions) != 1 {
		return fmt.Errorf("%w: %d questions, want 1", ErrEnvelope, len(m.Questions))
	}
	qs := m.Questions[0]
	if qs.Type != q.Type || qs.Class != q.Class {
		return fmt.Errorf("%w: question type/class mismatch", ErrEnvelope)
	}
	// 0x20: the echoed qname must reproduce our randomized
	// capitalization exactly, byte for byte.
	if qs.Name != q.Name {
		return fmt.Errorf("%w: question name %q does not echo 0x20 pattern %q", ErrEnvelope, qs.Name, q.Name)
	}
	return nil
}

// Admit verifies a response against its outstanding query and admits the
// trustworthy, in-bailiwick records into the cache.
func (c *Cache) Admit(q *Query, m *Message) (*Admission, error) {
	if err := checkEnvelope(q, m); err != nil {
		return nil, err
	}
	zone := canon(q.Zone)
	if !inBailiwick(canon(q.Name), zone) {
		return nil, fmt.Errorf("%w: qname %q outside bailiwick %q", ErrEnvelope, q.Name, q.Zone)
	}

	a := &Admission{}
	var accepted []Record
	var acceptedTrust []Trust
	accept := func(rr Record, t Trust) {
		accepted = append(accepted, rr)
		acceptedTrust = append(acceptedTrust, t)
	}
	reject := func(rr Record, reason string) {
		a.Rejected = append(a.Rejected, Rejection{Record: rr, Reason: reason})
	}

	answerTrust := TrustAnswer
	if m.Authoritative {
		answerTrust = TrustAuthAnswer
	}

	// Answer section: only records on the verified CNAME chain are
	// admitted. The chain starts at the qname and advances along
	// CNAME targets; a loop back to a visited name ends it.
	expected := canon(q.Name)
	visited := map[string]bool{expected: true}
	a.Chain = append(a.Chain, expected)
	chainBroken := false
	for _, rr := range m.Answers {
		owner := canon(rr.Name)
		if !inBailiwick(owner, zone) {
			reject(rr, "answer outside bailiwick")
			continue
		}
		if chainBroken || owner != expected {
			reject(rr, "answer not on verified CNAME chain")
			continue
		}
		if rr.Type == TypeCNAME {
			accept(rr, answerTrust)
			target := canon(rr.Data)
			if visited[target] {
				a.Loop = true
				chainBroken = true
				continue
			}
			visited[target] = true
			expected = target
			a.Chain = append(a.Chain, target)
			continue
		}
		accept(rr, answerTrust)
	}

	// Authority section: delegation data in-bailiwick. NS target
	// names are collected to decide which additional-section records
	// count as glue.
	nsNames := make(map[string]bool)
	for _, rr := range m.Authority {
		owner := canon(rr.Name)
		if !inBailiwick(owner, zone) {
			reject(rr, "authority record outside bailiwick")
			continue
		}
		switch rr.Type {
		case TypeNS:
			nsNames[canon(rr.Data)] = true
			accept(rr, TrustAuthAuthority)
		case TypeSOA:
			accept(rr, TrustAuthAuthority)
		default:
			reject(rr, "unexpected record type in authority section")
		}
	}

	// Additional section: only address records referenced by an
	// in-bailiwick NS name are glue; everything else is a poisoning
	// attempt (unrelated names, sibling hosts, out-of-bailiwick data).
	for _, rr := range m.Additional {
		owner := canon(rr.Name)
		if rr.Type != TypeA && rr.Type != TypeAAAA {
			reject(rr, "unexpected record type in additional section")
			continue
		}
		if !inBailiwick(owner, zone) {
			reject(rr, "additional record outside bailiwick")
			continue
		}
		if !nsNames[owner] {
			reject(rr, "additional record not referenced by any NS record")
			continue
		}
		accept(rr, TrustGlue)
	}

	// Commit: group accepted records into sets and store each set only
	// if its trust is at least the trust of the cached entry.
	type group struct {
		records []Record
		trust   Trust
	}
	groups := make(map[cacheKey]*group)
	var order []cacheKey
	for i, rr := range accepted {
		key := cacheKey{name: canon(rr.Name), typ: rr.Type, class: rr.Class}
		g, ok := groups[key]
		if !ok {
			g = &group{trust: acceptedTrust[i]}
			groups[key] = g
			order = append(order, key)
		}
		g.records = append(g.records, rr)
		if acceptedTrust[i] > g.trust {
			g.trust = acceptedTrust[i]
		}
	}
	for _, key := range order {
		g := groups[key]
		if old, ok := c.entries[key]; ok && old.Trust > g.trust {
			for _, rr := range g.records {
				reject(rr, fmt.Sprintf("trust %s does not displace cached %s", g.trust, old.Trust))
			}
			continue
		}
		c.entries[key] = &Entry{Records: g.records, Trust: g.trust}
		a.Stored = append(a.Stored, g.records...)
	}
	return a, nil
}
