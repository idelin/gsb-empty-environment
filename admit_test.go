package netsec

import (
	"net/netip"
	"reflect"
	"sort"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testAdmitter() (*Admitter, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	links := LinkConfig{Prefixes: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParsePrefix("fd00:1::/64"),
	}}
	return NewAdmitter(links, clock.now), clock
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func validMDNSMeta(src string) PacketMeta {
	return PacketMeta{
		Proto:      MDNS,
		SourceIP:   mustAddr(src),
		SourcePort: MDNSPort,
		IPTTL:      MDNSTTL,
	}
}

func aRecord(name string, ttl uint32, addr [4]byte) Record {
	return Record{Name: name, Type: TypeA, Class: 1, TTL: ttl, Data: AData{Addr: addr}}
}

func cachedNameSet(a *Admitter) map[string]bool {
	names := a.CachedNames()
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// A legitimate .local mDNS response with a conforming envelope is accepted
// and its answer reaches the cache.
func TestMDNSLegalLocalAccepted(t *testing.T) {
	admitter, _ := testAdmitter()
	msg := &Message{
		Questions: []Question{{Name: "printer.local", Type: TypeA}},
		Answers:   []Record{aRecord("printer.local", 120, [4]byte{10, 0, 0, 20})},
	}

	dec := admitter.Admit(validMDNSMeta("10.0.0.53"), msg)
	if !dec.Accepted {
		t.Fatalf("expected acceptance, got reason %q", dec.Reason)
	}
	recs, ok := admitter.Lookup("printer.local", TypeA)
	if !ok || len(recs) != 1 {
		t.Fatalf("expected one cached A record, got %v (ok=%v)", recs, ok)
	}
	if !reflect.DeepEqual(recs[0].Data, AData{Addr: [4]byte{10, 0, 0, 20}}) {
		t.Fatalf("cached address mismatch: %#v", recs[0].Data)
	}
}

// Every mDNS envelope violation must reject the packet and leave the cache
// untouched: off-prefix source, TTL below 255, and wrong source port.
func TestMDNSEnvelopeViolationsRejected(t *testing.T) {
	cases := []struct {
		name   string
		meta   PacketMeta
		reason string
	}{
		{
			name:   "cross_prefix_source",
			meta:   validMDNSMeta("10.0.1.53"), // 10.0.1.0/24 is not on-link
			reason: ReasonMDNSOffLink,
		},
		{
			name:   "routed_ttl_254",
			meta:   PacketMeta{Proto: MDNS, SourceIP: mustAddr("10.0.0.53"), SourcePort: MDNSPort, IPTTL: 254},
			reason: ReasonMDNSPacketTTL,
		},
		{
			name:   "wrong_source_port",
			meta:   PacketMeta{Proto: MDNS, SourceIP: mustAddr("10.0.0.53"), SourcePort: 40000, IPTTL: MDNSTTL},
			reason: ReasonMDNSSourcePort,
		},
		{
			name:   "non_local_name",
			meta:   validMDNSMeta("10.0.0.53"),
			reason: ReasonMDNSNotLocal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			admitter, _ := testAdmitter()
			name := "printer.local"
			if tc.reason == ReasonMDNSNotLocal {
				name = "printer.corp.example"
			}
			msg := &Message{
				Questions: []Question{{Name: name, Type: TypeA}},
				Answers:   []Record{aRecord(name, 120, [4]byte{10, 0, 0, 20})},
			}

			dec := admitter.Admit(tc.meta, msg)
			if dec.Accepted || dec.Reason != tc.reason {
				t.Fatalf("expected reject %q, got accepted=%v reason=%q", tc.reason, dec.Accepted, dec.Reason)
			}
			if len(admitter.CachedNames()) != 0 {
				t.Fatalf("rejected packet poisoned the cache: %v", admitter.CachedNames())
			}
		})
	}
}

// Link-local addresses are on-link even without a matching configured
// prefix, and IPv6 on-link sources work the same as IPv4.
func TestMDNSLinkLocalAndIPv6Accepted(t *testing.T) {
	admitter, _ := testAdmitter()
	msg := &Message{
		Questions: []Question{{Name: "host.local", Type: TypeAAAA}},
		Answers: []Record{{Name: "host.local", Type: TypeAAAA, Class: 1, TTL: 60,
			Data: AAAAData{Addr: [16]byte{0xfd, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x20}}}},
	}

	for _, src := range []string{"fe80::1", "fd00:1::5"} {
		dec := admitter.Admit(validMDNSMeta(src), msg)
		if !dec.Accepted {
			t.Fatalf("src %s rejected: %s", src, dec.Reason)
		}
	}
	if _, ok := admitter.Lookup("host.local", TypeAAAA); !ok {
		t.Fatal("AAAA record not cached from on-link IPv6 source")
	}
}

// wpad and isatap must never be resolved via multicast, no matter how clean
// the packet envelope is.
func TestBlockedNamesRejected(t *testing.T) {
	for _, name := range []string{"wpad.local", "isatap.local", "wpad.corp.example", "WPAD.LOCAL"} {
		admitter, _ := testAdmitter()
		msg := &Message{
			Questions: []Question{{Name: name, Type: TypeA}},
			Answers:   []Record{aRecord(name, 120, [4]byte{10, 0, 0, 66})},
		}
		dec := admitter.Admit(validMDNSMeta("10.0.0.53"), msg)
		if dec.Accepted || dec.Reason != ReasonBlockedName {
			t.Fatalf("%s: expected block, got accepted=%v reason=%q", name, dec.Accepted, dec.Reason)
		}
		if len(admitter.CachedNames()) != 0 {
			t.Fatalf("%s: blocked name reached cache: %v", name, admitter.CachedNames())
		}
	}
}

// A blocked name hiding in the additional section is rejected as well.
func TestBlockedNameInAdditionalRejected(t *testing.T) {
	admitter, _ := testAdmitter()
	msg := &Message{
		Questions: []Question{{Name: "printer.local", Type: TypeA}},
		Answers:   []Record{aRecord("printer.local", 120, [4]byte{10, 0, 0, 20})},
		Additional: []Record{
			aRecord("wpad.local", 120, [4]byte{10, 0, 0, 66}),
		},
	}
	dec := admitter.Admit(validMDNSMeta("10.0.0.53"), msg)
	if dec.Accepted || dec.Reason != ReasonBlockedName {
		t.Fatalf("expected block on additional wpad, got accepted=%v reason=%q", dec.Accepted, dec.Reason)
	}
	if len(admitter.CachedNames()) != 0 {
		t.Fatalf("packet with blocked additional record poisoned cache: %v", admitter.CachedNames())
	}
}

// Records unrelated to the question, including attacker-injected additional
// records, must be dropped instead of cached. Records reachable through a
// legitimate name chain (a CNAME target) stay.
func TestAdditionalSectionPoisoningDropped(t *testing.T) {
	admitter, _ := testAdmitter()
	msg := &Message{
		Questions: []Question{{Name: "app.local", Type: TypeA}},
		Answers: []Record{
			{Name: "app.local", Type: TypeCNAME, Class: 1, TTL: 120, Data: CNAMEData{Target: "cdn.local"}},
			aRecord("app.local", 120, [4]byte{10, 0, 0, 21}),
		},
		Additional: []Record{
			aRecord("cdn.local", 120, [4]byte{10, 0, 0, 22}),           // CNAME target: keep
			aRecord("evil.attacker.local", 120, [4]byte{10, 0, 0, 66}), // unrelated: drop
			aRecord("gateway.local", 120, [4]byte{10, 0, 0, 1}),        // unrelated: drop
		},
	}

	dec := admitter.Admit(validMDNSMeta("10.0.0.53"), msg)
	if !dec.Accepted {
		t.Fatalf("expected acceptance, got %q", dec.Reason)
	}

	got := cachedNameSet(admitter)
	want := map[string]bool{"app.local.": true, "cdn.local.": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached names = %v, want %v", got, want)
	}
	if _, ok := admitter.Lookup("evil.attacker.local", TypeA); ok {
		t.Fatal("attacker additional record was cached")
	}
	if _, ok := admitter.Lookup("gateway.local", TypeA); ok {
		t.Fatal("unrelated gateway record was cached")
	}
	if _, ok := admitter.Lookup("cdn.local", TypeA); !ok {
		t.Fatal("legitimate CNAME target was not cached")
	}

	var cachedOwners []string
	for _, r := range dec.Cached {
		cachedOwners = append(cachedOwners, r.Name)
	}
	sort.Strings(cachedOwners)
	wantOwners := []string{"app.local", "app.local", "cdn.local"}
	if !reflect.DeepEqual(cachedOwners, wantOwners) {
		t.Fatalf("Decision.Cached owners = %v, want %v", cachedOwners, wantOwners)
	}
}

// LLMNR responses are accepted only while an identical transaction is
// outstanding, from the LLMNR port and an on-link source; the transaction
// is consumed so replays fail too.
func TestLLMNRTxidMatching(t *testing.T) {
	admitter, clock := testAdmitter()
	admitter.RegisterPending(PendingQuery{
		Proto:     LLMNR,
		ID:        0x1234,
		Name:      "printer",
		ExpiresAt: clock.now().Add(time.Second),
	})

	llmnrMeta := func(src string, port uint16) PacketMeta {
		return PacketMeta{Proto: LLMNR, SourceIP: mustAddr(src), SourcePort: port, IPTTL: 64}
	}
	msg := &Message{
		ID:        0x1234,
		Questions: []Question{{Name: "printer", Type: TypeA}},
		Answers:   []Record{aRecord("printer", 30, [4]byte{10, 0, 0, 30})},
	}

	// Unknown transaction ID: reject.
	unknown := *msg
	unknown.ID = 0x9999
	if dec := admitter.Admit(llmnrMeta("10.0.0.77", LLMNRPort), &unknown); dec.Accepted || dec.Reason != ReasonLLMNRNoTransaction {
		t.Fatalf("unknown txid: got accepted=%v reason=%q", dec.Accepted, dec.Reason)
	}

	// Off-link responder: reject even with the right txid.
	if dec := admitter.Admit(llmnrMeta("10.0.1.77", LLMNRPort), msg); dec.Accepted || dec.Reason != ReasonLLMNROffLink {
		t.Fatalf("off-link: got accepted=%v reason=%q", dec.Accepted, dec.Reason)
	}

	// Wrong source port: reject.
	if dec := admitter.Admit(llmnrMeta("10.0.0.77", 40000), msg); dec.Accepted || dec.Reason != ReasonLLMNRSourcePort {
		t.Fatalf("wrong port: got accepted=%v reason=%q", dec.Accepted, dec.Reason)
	}

	// Matching outstanding query: accept and cache.
	if dec := admitter.Admit(llmnrMeta("10.0.0.77", LLMNRPort), msg); !dec.Accepted {
		t.Fatalf("matching response rejected: %s", dec.Reason)
	}
	if _, ok := admitter.Lookup("printer", TypeA); !ok {
		t.Fatal("LLMNR answer not cached")
	}

	// Replay: the transaction was consumed, so the same response fails.
	if dec := admitter.Admit(llmnrMeta("10.0.0.77", LLMNRPort), msg); dec.Accepted || dec.Reason != ReasonLLMNRNoTransaction {
		t.Fatalf("replay: got accepted=%v reason=%q", dec.Accepted, dec.Reason)
	}
}

// Expired pending queries no longer validate a response.
func TestLLMNRPendingExpiry(t *testing.T) {
	admitter, clock := testAdmitter()
	admitter.RegisterPending(PendingQuery{
		Proto:     LLMNR,
		ID:        0x2222,
		Name:      "printer",
		ExpiresAt: clock.now(),
	})
	clock.advance(time.Second)

	msg := &Message{
		ID:        0x2222,
		Questions: []Question{{Name: "printer", Type: TypeA}},
		Answers:   []Record{aRecord("printer", 30, [4]byte{10, 0, 0, 30})},
	}
	dec := admitter.Admit(PacketMeta{
		Proto: LLMNR, SourceIP: mustAddr("10.0.0.77"), SourcePort: LLMNRPort, IPTTL: 64,
	}, msg)
	if dec.Accepted || dec.Reason != ReasonLLMNRNoTransaction {
		t.Fatalf("expired query: got accepted=%v reason=%q", dec.Accepted, dec.Reason)
	}
}

// Cache entries expire once their DNS TTL elapses and are then invisible.
func TestCacheTTLExpiry(t *testing.T) {
	admitter, clock := testAdmitter()
	msg := &Message{
		Questions: []Question{{Name: "printer.local", Type: TypeA}},
		Answers:   []Record{aRecord("printer.local", 10, [4]byte{10, 0, 0, 20})},
	}
	if dec := admitter.Admit(validMDNSMeta("10.0.0.53"), msg); !dec.Accepted {
		t.Fatalf("accept failed: %s", dec.Reason)
	}
	if _, ok := admitter.Lookup("printer.local", TypeA); !ok {
		t.Fatal("record missing before TTL expiry")
	}

	clock.advance(11 * time.Second)
	if recs, ok := admitter.Lookup("printer.local", TypeA); ok || recs != nil {
		t.Fatalf("record survived TTL expiry: %v", recs)
	}
	if names := admitter.CachedNames(); len(names) != 0 {
		t.Fatalf("expired owner name still listed: %v", names)
	}
}

// A zero-TTL mDNS goodbye removes a previously cached entry instead of
// refreshing it.
func TestZeroTTLRemovesEntry(t *testing.T) {
	admitter, _ := testAdmitter()
	first := &Message{
		Questions: []Question{{Name: "printer.local", Type: TypeA}},
		Answers:   []Record{aRecord("printer.local", 120, [4]byte{10, 0, 0, 20})},
	}
	if dec := admitter.Admit(validMDNSMeta("10.0.0.53"), first); !dec.Accepted {
		t.Fatalf("first accept failed: %s", dec.Reason)
	}

	goodbye := &Message{
		Questions: []Question{{Name: "printer.local", Type: TypeA}},
		Answers:   []Record{aRecord("printer.local", 0, [4]byte{10, 0, 0, 20})},
	}
	if dec := admitter.Admit(validMDNSMeta("10.0.0.53"), goodbye); !dec.Accepted {
		t.Fatalf("goodbye rejected: %s", dec.Reason)
	}
	if _, ok := admitter.Lookup("printer.local", TypeA); ok {
		t.Fatal("goodbye did not evict the cached record")
	}
}

// Invalid envelopes (multicast source, nil message) fail without touching
// any state.
func TestInvalidEnvelope(t *testing.T) {
	admitter, _ := testAdmitter()
	if dec := admitter.Admit(validMDNSMeta("224.0.0.251"), &Message{}); dec.Accepted || dec.Reason != ReasonInvalidPacket {
		t.Fatalf("multicast source: %+v", dec)
	}
	if dec := admitter.Admit(validMDNSMeta("10.0.0.53"), nil); dec.Accepted || dec.Reason != ReasonInvalidPacket {
		t.Fatalf("nil message: %+v", dec)
	}
}
