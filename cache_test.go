package netsec

import (
	"testing"
	"time"
)

func a(owner, ip string) RR {
	return RR{Owner: owner, Type: TypeA, Class: ClassIN, TTL: 300, RData: ip}
}

func ns(owner, target string) RR {
	return RR{Owner: owner, Type: TypeNS, Class: ClassIN, TTL: 300, RData: target}
}

func hintOwners(res *AdmitResult) map[string]bool {
	m := make(map[string]bool)
	for _, rr := range res.Hints {
		m[rr.Owner+"|"+rr.RData] = true
	}
	return m
}

func mustNotCache(t *testing.T, c *Cache, name string, typ uint16) {
	t.Helper()
	if _, _, ok := c.Lookup(name, typ, ClassIN); ok {
		t.Fatalf("%s type %d must not be cached", name, typ)
	}
}

func mustCache(t *testing.T, c *Cache, name string, typ uint16) []RR {
	t.Helper()
	recs, _, ok := c.Lookup(name, typ, ClassIN)
	if !ok {
		t.Fatalf("%s type %d should be cached", name, typ)
	}
	return recs
}

// 附加段投毒: an authoritative server for attacker.com stuffs a victim
// A record into the additional section.
func TestAdditionalSectionPoisoning(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "www.attacker.com", Type: TypeA, Class: ClassIN},
		Answer:        []RR{a("www.attacker.com", "192.0.2.1")},
		Authority:     []RR{ns("attacker.com", "ns1.attacker.com")},
		Additional: []RR{
			a("ns1.attacker.com", "198.51.100.1"), // legitimate in-zone glue
			a("www.victim.com", "203.0.113.66"),   // poison attempt
		},
	}
	res := c.Admit(resp, "attacker.com")

	mustNotCache(t, c, "www.victim.com", TypeA)
	if !hintOwners(res)["www.victim.com.|203.0.113.66"] {
		t.Fatal("poison record should be returned as a one-time hint")
	}
	mustCache(t, c, "www.attacker.com", TypeA)
	mustCache(t, c, "ns1.attacker.com", TypeA)
}

// CNAME跨区: a CNAME points out of the answering zone; the target's
// address record in the same response is out of bailiwick.
func TestCrossZoneCNAME(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "www.attacker.com", Type: TypeA, Class: ClassIN},
		Answer: []RR{
			{Owner: "www.attacker.com", Type: TypeCNAME, Class: ClassIN, TTL: 300, RData: "www.victim.com"},
			a("www.victim.com", "203.0.113.9"),
		},
	}
	res := c.Admit(resp, "attacker.com")

	mustCache(t, c, "www.attacker.com", TypeCNAME)
	mustNotCache(t, c, "www.victim.com", TypeA)
	if !hintOwners(res)["www.victim.com.|203.0.113.9"] {
		t.Fatal("cross-zone CNAME target data should be a hint")
	}
}

// DNAME跨区: same rule for DNAME rewrites.
func TestCrossZoneDNAME(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "www.ads.attacker.com", Type: TypeA, Class: ClassIN},
		Answer: []RR{
			{Owner: "ads.attacker.com", Type: TypeDNAME, Class: ClassIN, TTL: 300, RData: "ads.victim.com"},
			a("www.ads.victim.com", "203.0.113.10"),
		},
	}
	c.Admit(resp, "attacker.com")

	mustCache(t, c, "ads.attacker.com", TypeDNAME)
	mustNotCache(t, c, "www.ads.victim.com", TypeA)
}

// 通配展开: a wildcard-synthesized answer carries the qname as owner and
// stays inside the zone; a wildcard record for another zone does not.
func TestWildcardExpansion(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "foo.attacker.com", Type: TypeA, Class: ClassIN},
		Answer: []RR{
			// Synthesized from *.attacker.com; owner is the qname.
			a("foo.attacker.com", "192.0.2.5"),
			{Owner: "foo.attacker.com", Type: TypeRRSIG, Class: ClassIN, TTL: 300,
				RData: "1 1 2 300 20270101000000 20261101000000 12345 attacker.com. AAAA"},
		},
		Additional: []RR{
			a("*.victim.com", "203.0.113.77"), // wildcard poison attempt
		},
	}
	res := c.Admit(resp, "attacker.com")

	mustCache(t, c, "FOO.attacker.com", TypeA) // case-insensitive lookup
	mustNotCache(t, c, "*.victim.com", TypeA)
	if !hintOwners(res)["*.victim.com.|203.0.113.77"] {
		t.Fatal("out-of-zone wildcard record should be a hint")
	}
}

// 越界NS: authority-section NS records for unrelated zones are hints;
// a genuine referral narrows the scope so data below it is not
// authoritative from this server either.
func TestOutOfBailiwickNS(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "www.attacker.com", Type: TypeA, Class: ClassIN},
		Answer: []RR{
			a("www.attacker.com", "192.0.2.1"),
			a("www.sub.attacker.com", "192.0.2.99"), // below the referral: not authoritative here
		},
		Authority: []RR{
			ns("victim.com", "ns1.attacker.com"),           // out of bailiwick
			ns("sub.attacker.com", "ns1.sub.attacker.com"), // referral
		},
		Additional: []RR{
			a("ns1.sub.attacker.com", "198.51.100.20"),   // glue for the referral
			a("other.sub.attacker.com", "198.51.100.21"), // below referral, not a nameserver
		},
	}
	res := c.Admit(resp, "attacker.com")

	mustNotCache(t, c, "victim.com", TypeNS)
	if !hintOwners(res)["victim.com.|ns1.attacker.com."] {
		t.Fatal("out-of-bailiwick NS should be a hint")
	}
	mustCache(t, c, "sub.attacker.com", TypeNS)         // referral itself
	mustCache(t, c, "ns1.sub.attacker.com", TypeA)      // matching glue
	mustNotCache(t, c, "www.sub.attacker.com", TypeA)   // below referral
	mustNotCache(t, c, "other.sub.attacker.com", TypeA) // not glue
}

// 重复记录: duplicates that differ only in case collapse into one RRset.
func TestDuplicateRecords(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "www.attacker.com", Type: TypeA, Class: ClassIN},
		Answer: []RR{
			a("WWW.attacker.com.", "192.0.2.1"),
			a("www.Attacker.COM", "192.0.2.1"), // duplicate, different case
			a("www.attacker.com", "192.0.2.2"),
			{Owner: "alias.attacker.com", Type: TypeCNAME, Class: ClassIN, TTL: 300, RData: "WWW.attacker.com"},
			{Owner: "ALIAS.attacker.com", Type: TypeCNAME, Class: ClassIN, TTL: 300, RData: "www.attacker.com."},
		},
	}
	c.Admit(resp, "attacker.com")

	recs := mustCache(t, c, "www.attacker.com", TypeA)
	if len(recs) != 2 {
		t.Fatalf("expected 2 deduplicated A records, got %d", len(recs))
	}
	cnames := mustCache(t, c, "alias.attacker.com", TypeCNAME)
	if len(cnames) != 1 || cnames[0].RData != "www.attacker.com." {
		t.Fatalf("CNAME duplicates not canonicalized: %+v", cnames)
	}
}

// 合法同区附加: additional-section data inside the zone is cached.
func TestLegitimateInZoneAdditional(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		Question:      Question{Name: "attacker.com", Type: TypeMX, Class: ClassIN},
		Answer: []RR{
			{Owner: "attacker.com", Type: TypeMX, Class: ClassIN, TTL: 300, RData: "10 mail.attacker.com"},
		},
		Additional: []RR{
			a("mail.attacker.com", "192.0.2.25"),
			{Owner: "mail.attacker.com", Type: TypeAAAA, Class: ClassIN, TTL: 300, RData: "2001:db8::25"},
		},
	}
	c.Admit(resp, "attacker.com")

	mustCache(t, c, "attacker.com", TypeMX)
	mustCache(t, c, "mail.attacker.com", TypeA)
	mustCache(t, c, "mail.attacker.com", TypeAAAA)
}

// DNSSEC验证只覆盖签名区: a validated response still must not cache
// unsigned out-of-bailiwick records, and hints carry no validation.
func TestDNSSECDoesNotExtendBailiwick(t *testing.T) {
	c := NewCache()
	resp := &Response{
		Authoritative: true,
		DNSSECValid:   true,
		Question:      Question{Name: "www.attacker.com", Type: TypeA, Class: ClassIN},
		Answer:        []RR{a("www.attacker.com", "192.0.2.1")},
		Additional: []RR{
			a("ns1.attacker.com", "198.51.100.1"),
			a("www.victim.com", "203.0.113.66"), // unsigned, out of zone
		},
	}
	c.Admit(resp, "attacker.com")

	mustNotCache(t, c, "www.victim.com", TypeA)
	if _, secure, _ := c.Lookup("www.attacker.com", TypeA, ClassIN); !secure {
		t.Fatal("in-zone record from a validated response should be secure")
	}
	if _, secure, _ := c.Lookup("ns1.attacker.com", TypeA, ClassIN); !secure {
		t.Fatal("in-zone additional record from a validated response should be secure")
	}
}

// 委托变更使旧范围失效: a referral evicts data below the delegation
// point that was cached under the parent zone's wider scope.
func TestDelegationInvalidatesOldScope(t *testing.T) {
	c := NewCache()
	// Earlier response: the example.com server answered directly for a
	// name below what turns out to be a delegated subzone.
	c.Admit(&Response{
		Authoritative: true,
		Question:      Question{Name: "www.sub.example.com", Type: TypeA, Class: ClassIN},
		Answer:        []RR{a("www.sub.example.com", "192.0.2.99")},
	}, "example.com")
	mustCache(t, c, "www.sub.example.com", TypeA)

	// Referral: sub.example.com is delegated elsewhere.
	c.Admit(&Response{
		Authoritative: false,
		Question:      Question{Name: "www.sub.example.com", Type: TypeA, Class: ClassIN},
		Authority:     []RR{ns("sub.example.com", "ns1.sub.example.com")},
		Additional:    []RR{a("ns1.sub.example.com", "198.51.100.53")},
	}, "example.com")

	mustNotCache(t, c, "www.sub.example.com", TypeA) // old scope invalidated
	mustCache(t, c, "sub.example.com", TypeNS)
	mustCache(t, c, "ns1.sub.example.com", TypeA) // glue survives
}

// TTLs are enforced.
func TestTTLExpiry(t *testing.T) {
	c := NewCache()
	clock := time.Now()
	c.SetClock(func() time.Time { return clock })
	c.Admit(&Response{
		Authoritative: true,
		Question:      Question{Name: "www.attacker.com", Type: TypeA, Class: ClassIN},
		Answer:        []RR{a("www.attacker.com", "192.0.2.1")},
	}, "attacker.com")

	mustCache(t, c, "www.attacker.com", TypeA)
	clock = clock.Add(301 * time.Second)
	mustNotCache(t, c, "www.attacker.com", TypeA)
}
