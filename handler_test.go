package netsec

import (
	"context"
	"encoding/binary"
	"net/netip"
	"testing"
)

var (
	testInternalClient = netip.MustParseAddr("10.1.2.3")
	testExternalClient = netip.MustParseAddr("198.51.100.7")
)

func testPolicy(t *testing.T) *Policy {
	t.Helper()
	policy, err := NewPolicyFromCIDRs([]string{"10.0.0.0/8", "2001:db8::/32"}, 77)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testLocalRecord(t *testing.T) Record {
	t.Helper()
	record, err := ARecord("www.internal.example", netip.MustParseAddr("10.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	record.TTL = 9
	return record
}

func testCachedRecord(t *testing.T) Record {
	t.Helper()
	record, err := ARecord("visited.example", netip.MustParseAddr("203.0.113.5"))
	if err != nil {
		t.Fatal(err)
	}
	record.TTL = 7200
	return record
}

func testZone(t *testing.T) *Zone {
	t.Helper()
	soa, err := SOARecord("internal.example", "ns.internal.example", "hostmaster.internal.example", 1, 3600, 600, 86400, 60)
	if err != nil {
		t.Fatal(err)
	}
	zone, err := NewZone("internal.example", testLocalRecord(t), soa)
	if err != nil {
		t.Fatal(err)
	}
	return zone
}

func testHandler(t *testing.T, cache Cache, upstream Upstream) *Handler {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{
		Policy:   testPolicy(t),
		Zones:    []*Zone{testZone(t)},
		Cache:    cache,
		Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestPolicyAdmission(t *testing.T) {
	policy := testPolicy(t)
	tests := []struct {
		name   string
		client netip.Addr
		want   Decision
	}{
		{"internal IPv4", testInternalClient, Allow},
		{"internal IPv6", netip.MustParseAddr("2001:db8::1"), Allow},
		{"external IPv4", testExternalClient, Refuse},
		{"invalid", netip.Addr{}, Refuse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := policy.Decide(test.client); got != test.want {
				t.Fatalf("Decide(%s) = %v, want %v", test.client, got, test.want)
			}
		})
	}
}

func TestExternalClientsAreRefusedWithoutData(t *testing.T) {
	cacheCalls := 0
	upstreamCalls := 0
	cache := CacheFunc(func(context.Context, Question) (Resolution, bool) {
		cacheCalls++
		return Resolution{Answer: []Record{testCachedRecord(t)}}, true
	})
	upstream := UpstreamFunc(func(context.Context, Question) (Resolution, error) {
		upstreamCalls++
		return Resolution{Answer: []Record{testCachedRecord(t)}}, nil
	})
	handler := testHandler(t, cache, upstream)

	for _, rd := range []bool{false, true} {
		response := handler.Resolve(context.Background(), Request{
			Client:           testExternalClient,
			Question:         Question{Name: "visited.example", Type: TypeA, Class: ClassINET},
			RecursionDesired: rd,
		})
		if response.RCode != RCodeRefused {
			t.Fatalf("RD=%v: RCode = %d, want REFUSED", rd, response.RCode)
		}
		if len(response.Answer)+len(response.Authority)+len(response.Additional) != 0 {
			t.Fatalf("RD=%v: REFUSED response contains resolver data", rd)
		}
	}
	if cacheCalls != 0 || upstreamCalls != 0 {
		t.Fatalf("external clients reached cache/upstream: cache=%d upstream=%d", cacheCalls, upstreamCalls)
	}
}

func TestRDZeroUsesOnlyLocalAuthority(t *testing.T) {
	cacheCalls := 0
	upstreamCalls := 0
	cache := CacheFunc(func(context.Context, Question) (Resolution, bool) {
		cacheCalls++
		return Resolution{Answer: []Record{testCachedRecord(t)}}, true
	})
	upstream := UpstreamFunc(func(context.Context, Question) (Resolution, error) {
		upstreamCalls++
		return Resolution{Answer: []Record{testCachedRecord(t)}}, nil
	})
	handler := testHandler(t, cache, upstream)

	probe := handler.Resolve(context.Background(), Request{
		Client:   testInternalClient,
		Question: Question{Name: "visited.example", Type: TypeA, Class: ClassINET},
	})
	if probe.RCode != RCodeRefused {
		t.Fatalf("RD=0 cache probe RCode = %d, want REFUSED", probe.RCode)
	}
	if len(probe.Answer)+len(probe.Authority)+len(probe.Additional) != 0 {
		t.Fatal("RD=0 cache probe returned cache data")
	}

	local := handler.Resolve(context.Background(), Request{
		Client:   testInternalClient,
		Question: Question{Name: "www.internal.example", Type: TypeA, Class: ClassINET},
	})
	if local.RCode != RCodeNoError || !local.Authoritative {
		t.Fatalf("local RD=0 response: rcode=%d authoritative=%v", local.RCode, local.Authoritative)
	}
	if len(local.Answer) != 1 {
		t.Fatalf("local RD=0 answers = %d, want 1", len(local.Answer))
	}
	if cacheCalls != 0 || upstreamCalls != 0 {
		t.Fatalf("RD=0 reached cache/upstream: cache=%d upstream=%d", cacheCalls, upstreamCalls)
	}
}

func TestRDZeroNegativeAnswersStayLocal(t *testing.T) {
	handler := testHandler(t, nil, nil)

	missing := handler.Resolve(context.Background(), Request{
		Client:   testInternalClient,
		Question: Question{Name: "missing.internal.example", Type: TypeA, Class: ClassINET},
	})
	if missing.RCode != RCodeNXDomain || !missing.Authoritative {
		t.Fatalf("missing name: rcode=%d authoritative=%v", missing.RCode, missing.Authoritative)
	}
	if len(missing.Answer) != 0 || len(missing.Authority) != 1 {
		t.Fatalf("negative answer sections: answer=%d authority=%d", len(missing.Answer), len(missing.Authority))
	}
	if got := missing.Authority[0].TTL; got != 77 {
		t.Fatalf("negative TTL = %d, want 77", got)
	}

	nodata := handler.Resolve(context.Background(), Request{
		Client:   testInternalClient,
		Question: Question{Name: "www.internal.example", Type: TypeAAAA, Class: ClassINET},
	})
	if nodata.RCode != RCodeNoError || !nodata.Authoritative || len(nodata.Answer) != 0 {
		t.Fatalf("NODATA response: rcode=%d authoritative=%v answers=%d", nodata.RCode, nodata.Authoritative, len(nodata.Answer))
	}
}

func TestCacheHitAndMissExposeSameTTL(t *testing.T) {
	cacheCalls := 0
	upstreamCalls := 0
	handler := testHandler(t,
		CacheFunc(func(_ context.Context, question Question) (Resolution, bool) {
			cacheCalls++
			if question.Name != "visited.example" {
				return Resolution{}, false
			}
			record := testCachedRecord(t)
			record.TTL = 6500
			return Resolution{Answer: []Record{record}, RCode: RCodeNoError}, true
		}),
		UpstreamFunc(func(context.Context, Question) (Resolution, error) {
			upstreamCalls++
			record := testCachedRecord(t)
			record.Name = "miss.example"
			record.TTL = 3
			return Resolution{Answer: []Record{record}, RCode: RCodeNoError}, nil
		}),
	)

	hit := handler.Resolve(context.Background(), Request{
		Client:           testInternalClient,
		RecursionDesired: true,
		Question:         Question{Name: "visited.example", Type: TypeA, Class: ClassINET},
	})
	if hit.Authoritative || len(hit.Answer) != 1 {
		t.Fatalf("cache hit: authoritative=%v answers=%d", hit.Authoritative, len(hit.Answer))
	}
	if hit.Answer[0].TTL != 77 {
		t.Fatalf("cache-hit TTL = %d, want 77", hit.Answer[0].TTL)
	}

	miss := handler.Resolve(context.Background(), Request{
		Client:           testInternalClient,
		RecursionDesired: true,
		Question:         Question{Name: "miss.example", Type: TypeA, Class: ClassINET},
	})
	if miss.Authoritative || len(miss.Answer) != 1 {
		t.Fatalf("cache miss: authoritative=%v answers=%d", miss.Authoritative, len(miss.Answer))
	}
	if miss.Answer[0].TTL != 77 {
		t.Fatalf("cache-miss TTL = %d, want 77", miss.Answer[0].TTL)
	}
	if cacheCalls != 2 || upstreamCalls != 1 {
		t.Fatalf("unexpected backend calls: cache=%d upstream=%d", cacheCalls, upstreamCalls)
	}
}

func TestInternalRecursiveQueryAndWireTTL(t *testing.T) {
	handler := testHandler(t,
		CacheFunc(func(context.Context, Question) (Resolution, bool) {
			return Resolution{Answer: []Record{testCachedRecord(t)}, RCode: RCodeNoError}, true
		}), nil)

	packet := queryPacket(t, 0x4242, "visited.example", TypeA, true)
	wire, err := handler.HandlePacket(packet, testInternalClient)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseResponse(t, wire)
	if parsed.id != 0x4242 || parsed.rcode != RCodeNoError || !parsed.recursion {
		t.Fatalf("wire response: id=%#x rcode=%d rd=%v", parsed.id, parsed.rcode, parsed.recursion)
	}
	if len(parsed.answers) != 1 {
		t.Fatalf("wire answers = %d, want 1", len(parsed.answers))
	}
	if parsed.answers[0].TTL != 77 {
		t.Fatalf("wire TTL = %d, want 77", parsed.answers[0].TTL)
	}
}

func TestWireRDZeroProbeAndExternalRefusal(t *testing.T) {
	handler := testHandler(t,
		CacheFunc(func(context.Context, Question) (Resolution, bool) {
			return Resolution{Answer: []Record{testCachedRecord(t)}, RCode: RCodeNoError}, true
		}), nil)

	probe := queryPacket(t, 0x1111, "visited.example", TypeA, false)
	probeWire, err := handler.HandlePacket(probe, testInternalClient)
	if err != nil {
		t.Fatal(err)
	}
	probeResponse := parseResponse(t, probeWire)
	if probeResponse.rcode != RCodeRefused {
		t.Fatalf("internal RD=0 probe rcode = %d, want REFUSED", probeResponse.rcode)
	}
	if len(probeResponse.answers)+len(probeResponse.authority)+len(probeResponse.additional) != 0 {
		t.Fatal("internal RD=0 probe returned cached data")
	}

	external := queryPacket(t, 0x2222, "visited.example", TypeA, true)
	externalWire, err := handler.HandlePacket(external, testExternalClient)
	if err != nil {
		t.Fatal(err)
	}
	externalResponse := parseResponse(t, externalWire)
	if externalResponse.rcode != RCodeRefused {
		t.Fatalf("external rcode = %d, want REFUSED", externalResponse.rcode)
	}
	if len(externalResponse.answers)+len(externalResponse.authority)+len(externalResponse.additional) != 0 {
		t.Fatal("external REFUSED response contained data")
	}

	malformed := make([]byte, 12)
	binary.BigEndian.PutUint16(malformed[0:2], 0x4444)
	malformedWire, err := handler.HandlePacket(malformed, testExternalClient)
	if err != nil {
		t.Fatal(err)
	}
	malformedResponse := parseResponse(t, malformedWire)
	if malformedResponse.rcode != RCodeRefused {
		t.Fatalf("malformed external rcode = %d, want REFUSED", malformedResponse.rcode)
	}
	if len(malformedResponse.answers)+len(malformedResponse.authority)+len(malformedResponse.additional) != 0 {
		t.Fatal("malformed external REFUSED response contained data")
	}

	local := queryPacket(t, 0x3333, "www.internal.example", TypeA, false)
	localWire, err := handler.HandlePacket(local, testInternalClient)
	if err != nil {
		t.Fatal(err)
	}
	localResponse := parseResponse(t, localWire)
	if localResponse.rcode != RCodeNoError || !localResponse.authoritative || len(localResponse.answers) != 1 {
		t.Fatalf("local RD=0 wire response: rcode=%d aa=%v answers=%d", localResponse.rcode, localResponse.authoritative, len(localResponse.answers))
	}
	if localResponse.answers[0].TTL != 77 {
		t.Fatalf("local wire TTL = %d, want 77", localResponse.answers[0].TTL)
	}
}

func queryPacket(t *testing.T, id uint16, name string, qtype uint16, rd bool) []byte {
	t.Helper()
	encoded, err := encodeName(name)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 12, 12+len(encoded)+4)
	binary.BigEndian.PutUint16(packet[0:2], id)
	if rd {
		binary.BigEndian.PutUint16(packet[2:4], flagRecursion)
	}
	binary.BigEndian.PutUint16(packet[4:6], 1)
	packet = append(packet, encoded...)
	packet = append(packet, byte(qtype>>8), byte(qtype), 0, 1)
	return packet
}

type parsedResponse struct {
	id            uint16
	rcode         uint16
	authoritative bool
	recursion     bool
	answers       []Record
	authority     []Record
	additional    []Record
}

func parseResponse(t *testing.T, wire []byte) parsedResponse {
	t.Helper()
	if len(wire) < 12 {
		t.Fatal("short response")
	}
	flags := binary.BigEndian.Uint16(wire[2:4])
	response := parsedResponse{
		id:            binary.BigEndian.Uint16(wire[0:2]),
		rcode:         flags & 0x0f,
		authoritative: flags&flagAuthoritive != 0,
		recursion:     flags&flagRecursion != 0,
	}
	qd := int(binary.BigEndian.Uint16(wire[4:6]))
	an := int(binary.BigEndian.Uint16(wire[6:8]))
	ns := int(binary.BigEndian.Uint16(wire[8:10]))
	ar := int(binary.BigEndian.Uint16(wire[10:12]))
	offset := 12
	for i := 0; i < qd; i++ {
		_, next, err := parseName(wire, offset)
		if err != nil {
			t.Fatal(err)
		}
		offset = next + 4
	}
	readSection := func(count int) []Record {
		records := make([]Record, 0, count)
		for i := 0; i < count; i++ {
			name, next, err := parseName(wire, offset)
			if err != nil {
				t.Fatal(err)
			}
			if next+10 > len(wire) {
				t.Fatal("short record")
			}
			record := Record{
				Name:  name,
				Type:  binary.BigEndian.Uint16(wire[next : next+2]),
				Class: binary.BigEndian.Uint16(wire[next+2 : next+4]),
				TTL:   binary.BigEndian.Uint32(wire[next+4 : next+8]),
			}
			rdlen := int(binary.BigEndian.Uint16(wire[next+8 : next+10]))
			offset = next + 10
			if offset+rdlen > len(wire) {
				t.Fatal("short RDATA")
			}
			record.Data = append([]byte(nil), wire[offset:offset+rdlen]...)
			offset += rdlen
			records = append(records, record)
		}
		return records
	}
	response.answers = readSection(an)
	response.authority = readSection(ns)
	response.additional = readSection(ar)
	if offset != len(wire) {
		t.Fatalf("trailing response bytes: parsed=%d total=%d", offset, len(wire))
	}
	return response
}
