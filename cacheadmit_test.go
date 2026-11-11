package cacheadmit

import (
	"errors"
	"testing"
)

func testQuery() *Query {
	return &Query{
		ID:    0xBEEF,
		Port:  15353,
		Name:  "WwW.eXaMpLe.CoM",
		Type:  TypeA,
		Class: ClassIN,
		Zone:  "example.com",
	}
}

// referral builds a referral response from the example.com servers for
// the test query, delegating to ns1.example.com with the given
// additional-section records.
func referral(q *Query, additional ...Record) *Message {
	return &Message{
		ID:         q.ID,
		SourcePort: q.Port,
		Questions:  []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		Authority: []Record{
			{Name: "example.com", Type: TypeNS, Class: ClassIN, TTL: 3600, Data: "ns1.example.com"},
		},
		Additional: additional,
	}
}

func rejectionsFor(a *Admission, name string) int {
	n := 0
	for _, r := range a.Rejected {
		if canon(r.Record.Name) == canon(name) {
			n++
		}
	}
	return n
}

func TestAdditionalSectionPoisoning(t *testing.T) {
	q := testQuery()
	m := referral(q,
		Record{Name: "ns1.example.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "192.0.2.1"},
		// Poison: an answer for the qname smuggled into the additional section.
		Record{Name: "www.example.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "6.6.6.6"},
		// Poison: a completely unrelated name.
		Record{Name: "evil.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "6.6.6.7"},
	)
	a, err := NewCache().Admit(q, m)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(a.Stored) != 2 { // NS record + ns1 glue
		t.Fatalf("stored %d records, want 2: %+v", len(a.Stored), a.Stored)
	}
	if len(a.Rejected) != 2 {
		t.Fatalf("rejected %d records, want 2: %+v", len(a.Rejected), a.Rejected)
	}
	if rejectionsFor(a, "www.example.com") != 1 {
		t.Error("smuggled qname record was not rejected")
	}
	if rejectionsFor(a, "evil.com") != 1 {
		t.Error("out-of-bailiwick record was not rejected")
	}
}

func TestSiblingGlueRejected(t *testing.T) {
	q := testQuery()
	m := referral(q,
		// Legitimate glue for the delegated nameserver.
		Record{Name: "ns1.example.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "192.0.2.1"},
		// Sibling host in-bailiwick but not referenced by any NS record.
		Record{Name: "ns2.example.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "192.0.2.2"},
	)
	c := NewCache()
	a, err := c.Admit(q, m)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if _, _, ok := c.Lookup("ns1.example.com", TypeA); !ok {
		t.Error("referenced glue was not cached")
	}
	if _, _, ok := c.Lookup("ns2.example.com", TypeA); ok {
		t.Error("sibling glue was cached")
	}
	if rejectionsFor(a, "ns2.example.com") != 1 {
		t.Error("sibling glue was not rejected")
	}
}

func TestLowTrustCannotOverwriteHighTrust(t *testing.T) {
	q := testQuery()
	c := NewCache()

	// Authoritative answer for the qname.
	aa := &Message{
		ID:            q.ID,
		SourcePort:    q.Port,
		Authoritative: true,
		Questions:     []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		Answers: []Record{
			{Name: "www.example.com", Type: TypeA, Class: ClassIN, TTL: 300, Data: "192.0.2.10"},
		},
	}
	if _, err := c.Admit(q, aa); err != nil {
		t.Fatalf("Admit authoritative: %v", err)
	}

	// A later referral tries to overwrite it via additional-section
	// glue (the NS target happens to be the qname).
	poison := &Message{
		ID:         q.ID,
		SourcePort: q.Port,
		Questions:  []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		Authority: []Record{
			{Name: "example.com", Type: TypeNS, Class: ClassIN, TTL: 3600, Data: "www.example.com"},
		},
		Additional: []Record{
			{Name: "www.example.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "6.6.6.6"},
		},
	}
	a, err := c.Admit(q, poison)
	if err != nil {
		t.Fatalf("Admit poison: %v", err)
	}
	recs, trust, ok := c.Lookup("www.example.com", TypeA)
	if !ok {
		t.Fatal("cached answer disappeared")
	}
	if trust != TrustAuthAnswer {
		t.Errorf("cached trust = %s, want %s", trust, TrustAuthAnswer)
	}
	if len(recs) != 1 || recs[0].Data != "192.0.2.10" {
		t.Errorf("cached data = %+v, want original authoritative answer", recs)
	}
	if rejectionsFor(a, "www.example.com") != 1 {
		t.Error("low-trust overwrite attempt was not reported as rejected")
	}
}

func TestHighTrustOverwritesLowTrust(t *testing.T) {
	q := testQuery()
	c := NewCache()

	// Glue lands first.
	m := referral(q, Record{Name: "ns1.example.com", Type: TypeA, Class: ClassIN, TTL: 3600, Data: "192.0.2.1"})
	if _, err := c.Admit(q, m); err != nil {
		t.Fatalf("Admit glue: %v", err)
	}

	// An authoritative answer for the same name replaces it.
	q2 := &Query{ID: 7, Port: 15353, Name: "Ns1.ExAmPlE.cOm", Type: TypeA, Class: ClassIN, Zone: "example.com"}
	aa := &Message{
		ID:            q2.ID,
		SourcePort:    q2.Port,
		Authoritative: true,
		Questions:     []Question{{Name: q2.Name, Type: q2.Type, Class: q2.Class}},
		Answers: []Record{
			{Name: "ns1.example.com", Type: TypeA, Class: ClassIN, TTL: 300, Data: "192.0.2.99"},
		},
	}
	if _, err := c.Admit(q2, aa); err != nil {
		t.Fatalf("Admit authoritative: %v", err)
	}
	recs, trust, ok := c.Lookup("ns1.example.com", TypeA)
	if !ok || trust != TrustAuthAnswer || len(recs) != 1 || recs[0].Data != "192.0.2.99" {
		t.Errorf("cache = %+v (%s, %v), want authoritative overwrite", recs, trust, ok)
	}
}

func TestCNAMEChainLoop(t *testing.T) {
	q := testQuery()
	m := &Message{
		ID:            q.ID,
		SourcePort:    q.Port,
		Authoritative: true,
		Questions:     []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		Answers: []Record{
			{Name: "www.example.com", Type: TypeCNAME, Class: ClassIN, TTL: 300, Data: "alias.example.com"},
			{Name: "alias.example.com", Type: TypeCNAME, Class: ClassIN, TTL: 300, Data: "www.example.com"},
			// Poison riding behind the loop: never on a verified name.
			{Name: "www.example.com", Type: TypeA, Class: ClassIN, TTL: 300, Data: "6.6.6.6"},
		},
	}
	c := NewCache()
	a, err := c.Admit(q, m)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if !a.Loop {
		t.Error("CNAME loop was not detected")
	}
	if len(a.Chain) != 2 || a.Chain[0] != "www.example.com." || a.Chain[1] != "alias.example.com." {
		t.Errorf("chain = %v, want [www.example.com. alias.example.com.]", a.Chain)
	}
	if _, _, ok := c.Lookup("www.example.com", TypeA); ok {
		t.Error("A record behind the loop was cached")
	}
	if rejectionsFor(a, "www.example.com") != 1 {
		t.Error("A record behind the loop was not rejected")
	}
}

func TestCNAMEChainOnlyFollowsVerifiedNames(t *testing.T) {
	q := testQuery()
	m := &Message{
		ID:            q.ID,
		SourcePort:    q.Port,
		Authoritative: true,
		Questions:     []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		Answers: []Record{
			{Name: "www.example.com", Type: TypeCNAME, Class: ClassIN, TTL: 300, Data: "cdn.example.com"},
			{Name: "cdn.example.com", Type: TypeA, Class: ClassIN, TTL: 300, Data: "192.0.2.5"},
			// Off-chain record: never referenced by the verified chain.
			{Name: "mail.example.com", Type: TypeA, Class: ClassIN, TTL: 300, Data: "6.6.6.6"},
		},
	}
	c := NewCache()
	a, err := c.Admit(q, m)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if _, _, ok := c.Lookup("cdn.example.com", TypeA); !ok {
		t.Error("verified chain target was not cached")
	}
	if _, _, ok := c.Lookup("mail.example.com", TypeA); ok {
		t.Error("off-chain record was cached")
	}
	if rejectionsFor(a, "mail.example.com") != 1 {
		t.Error("off-chain record was not rejected")
	}
}

func TestOutOfBailiwickAnswerRejected(t *testing.T) {
	q := testQuery()
	m := &Message{
		ID:         q.ID,
		SourcePort: q.Port,
		Questions:  []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		Answers: []Record{
			{Name: "www.other.com", Type: TypeA, Class: ClassIN, TTL: 300, Data: "6.6.6.6"},
		},
	}
	a, err := NewCache().Admit(q, m)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(a.Stored) != 0 || len(a.Rejected) != 1 {
		t.Errorf("stored=%d rejected=%d, want 0/1", len(a.Stored), len(a.Rejected))
	}
}

func TestEnvelopeValidation(t *testing.T) {
	q := testQuery()
	base := func() *Message {
		return &Message{
			ID:         q.ID,
			SourcePort: q.Port,
			Questions:  []Question{{Name: q.Name, Type: q.Type, Class: q.Class}},
		}
	}

	t.Run("transaction ID", func(t *testing.T) {
		m := base()
		m.ID++
		if _, err := NewCache().Admit(q, m); !errors.Is(err, ErrEnvelope) {
			t.Errorf("err = %v, want ErrEnvelope", err)
		}
	})

	t.Run("source port", func(t *testing.T) {
		m := base()
		m.SourcePort++
		if _, err := NewCache().Admit(q, m); !errors.Is(err, ErrEnvelope) {
			t.Errorf("err = %v, want ErrEnvelope", err)
		}
	})

	t.Run("0x20 case echo", func(t *testing.T) {
		m := base()
		// Same letters, wrong capitalization: the 0x20 echo fails.
		m.Questions[0].Name = "www.example.com"
		if _, err := NewCache().Admit(q, m); !errors.Is(err, ErrEnvelope) {
			t.Errorf("err = %v, want ErrEnvelope", err)
		}
	})

	t.Run("question count", func(t *testing.T) {
		m := base()
		m.Questions = nil
		if _, err := NewCache().Admit(q, m); !errors.Is(err, ErrEnvelope) {
			t.Errorf("err = %v, want ErrEnvelope", err)
		}
	})
}
