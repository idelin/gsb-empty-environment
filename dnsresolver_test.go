package netsec

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func encodeTestName(name string) []byte {
	name = strings.TrimSuffix(name, ".")
	if name == "" || name == "." {
		return []byte{0}
	}
	var result []byte
	for _, label := range strings.Split(name, ".") {
		result = append(result, byte(len(label)))
		result = append(result, label...)
	}
	return append(result, 0)
}

func testQuestion(name string) []byte {
	result := encodeTestName(name)
	result = binary.BigEndian.AppendUint16(result, dnsTypeCAA)
	result = binary.BigEndian.AppendUint16(result, dnsClassIN)
	return result
}

func testRR(owner string, rrType uint16, rdata []byte) []byte {
	result := encodeTestName(owner)
	result = binary.BigEndian.AppendUint16(result, rrType)
	result = binary.BigEndian.AppendUint16(result, dnsClassIN)
	result = binary.BigEndian.AppendUint32(result, 300)
	result = binary.BigEndian.AppendUint16(result, uint16(len(rdata)))
	return append(result, rdata...)
}

func testResponse(id uint16, flags uint16, qd, an, ns, ar uint16, sections ...[]byte) []byte {
	result := make([]byte, 12)
	binary.BigEndian.PutUint16(result[0:2], id)
	binary.BigEndian.PutUint16(result[2:4], flags)
	binary.BigEndian.PutUint16(result[4:6], qd)
	binary.BigEndian.PutUint16(result[6:8], an)
	binary.BigEndian.PutUint16(result[8:10], ns)
	binary.BigEndian.PutUint16(result[10:12], ar)
	for _, section := range sections {
		result = append(result, section...)
	}
	return result
}

func testCAARData(flags uint8, tag, value string) []byte {
	result := []byte{flags, byte(len(tag))}
	result = append(result, tag...)
	return append(result, value...)
}

func TestDNSResponseCAA(t *testing.T) {
	id := uint16(0x1234)
	response := testResponse(id, 0x81a0, 1, 1, 0, 0,
		testQuestion("example.com"),
		testRR("example.com", dnsTypeCAA, testCAARData(0, "issue", "good-ca.example")),
	)
	resolver := &DNSResolver{}
	result, err := resolver.parseResponse(response, id, "example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("Records = %#v", result.Records)
	}
	if result.Records[0].Tag != "issue" || result.Records[0].Value != "good-ca.example" {
		t.Fatalf("record = %#v", result.Records[0])
	}
	if result.DNSSEC != DNSSECSecure {
		t.Fatalf("DNSSEC = %s, want secure", result.DNSSEC)
	}
	if result.AnswerName != "example.com" {
		t.Fatalf("AnswerName = %q", result.AnswerName)
	}
}

func TestDNSResponseWithoutAD(t *testing.T) {
	id := uint16(0x4321)
	response := testResponse(id, 0x8180, 1, 1, 0, 0,
		testQuestion("example.com"),
		testRR("example.com", dnsTypeCAA, testCAARData(0, "issue", "good-ca.example")),
	)
	result, err := (&DNSResolver{}).parseResponse(response, id, "example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if result.DNSSEC != DNSSECUnknown {
		t.Fatalf("DNSSEC = %s, want unknown", result.DNSSEC)
	}

	result, err = (&DNSResolver{Validating: true}).parseResponse(response, id, "example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if result.DNSSEC != DNSSECInsecure {
		t.Fatalf("DNSSEC = %s, want insecure", result.DNSSEC)
	}
}

func TestDNSResponseCNAME(t *testing.T) {
	id := uint16(0x2222)
	response := testResponse(id, 0x8180, 1, 1, 0, 0,
		testQuestion("www.example.com"),
		testRR("www.example.com", 5, encodeTestName("alias.other.test")),
	)
	result, err := (&DNSResolver{}).parseResponse(response, id, "www.example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if result.CNAME != "alias.other.test" {
		t.Fatalf("CNAME = %q", result.CNAME)
	}
}

func TestDNSResponseCAAFiltersUnrelatedOwners(t *testing.T) {
	id := uint16(0x2323)
	response := testResponse(id, 0x8180, 1, 3, 0, 0,
		testQuestion("www.example.com"),
		testRR("www.example.com", 5, encodeTestName("alias.other.test")),
		testRR("alias.other.test", dnsTypeCAA, testCAARData(0, "issue", "good-ca.example")),
		testRR("evil.example", dnsTypeCAA, testCAARData(0, "issue", "attacker.example")),
	)
	result, err := (&DNSResolver{}).parseResponse(response, id, "www.example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Value != "good-ca.example" {
		t.Fatalf("Records = %#v", result.Records)
	}
	if result.AnswerName != "alias.other.test" {
		t.Fatalf("AnswerName = %q", result.AnswerName)
	}
}

func TestDNSResponseDNAME(t *testing.T) {
	id := uint16(0x3333)
	response := testResponse(id, 0x8180, 1, 1, 0, 0,
		testQuestion("www.sub.example.com"),
		testRR("sub.example.com", 39, encodeTestName("other.test")),
	)
	result, err := (&DNSResolver{}).parseResponse(response, id, "www.sub.example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if result.DNAME == nil || result.DNAME.Owner != "sub.example.com" || result.DNAME.Target != "other.test" {
		t.Fatalf("DNAME = %#v", result.DNAME)
	}
}

func TestDNSResponseReferral(t *testing.T) {
	id := uint16(0x4444)
	response := testResponse(id, 0x8180, 1, 0, 1, 0,
		testQuestion("www.example.com"),
		testRR("example.com", 2, encodeTestName("ns1.example.com")),
	)
	result, err := (&DNSResolver{}).parseResponse(response, id, "www.example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if result.Delegation == nil {
		t.Fatal("Delegation is nil")
	}
	if result.Delegation.Zone != "example.com" {
		t.Fatalf("Zone = %q", result.Delegation.Zone)
	}
	if len(result.Delegation.NameServers) != 1 || result.Delegation.NameServers[0] != "ns1.example.com" {
		t.Fatalf("NameServers = %#v", result.Delegation.NameServers)
	}
}

func TestDNSResponseNXDOMAIN(t *testing.T) {
	id := uint16(0x5555)
	response := testResponse(id, 0x8183, 1, 0, 0, 0, testQuestion("missing.example.com"))
	_, err := (&DNSResolver{}).parseResponse(response, id, "missing.example.com")
	var rcodeError *DNSRcodeError
	if !errors.As(err, &rcodeError) || rcodeError.Rcode != 3 {
		t.Fatalf("error = %v, want NXDOMAIN", err)
	}
}

func TestDNSCompressionPointer(t *testing.T) {
	id := uint16(0x6666)
	question := testQuestion("example.com")
	ownerPointer := []byte{0xc0, 0x0c}
	ownerPointer = binary.BigEndian.AppendUint16(ownerPointer, dnsTypeCAA)
	ownerPointer = binary.BigEndian.AppendUint16(ownerPointer, dnsClassIN)
	ownerPointer = binary.BigEndian.AppendUint32(ownerPointer, 300)
	rdata := testCAARData(0, "issue", "good-ca.example")
	ownerPointer = binary.BigEndian.AppendUint16(ownerPointer, uint16(len(rdata)))
	ownerPointer = append(ownerPointer, rdata...)

	response := testResponse(id, 0x8180, 1, 1, 0, 0, question, ownerPointer)
	result, err := (&DNSResolver{}).parseResponse(response, id, "example.com")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("Records = %#v", result.Records)
	}
}

func TestDNSCompressionLoop(t *testing.T) {
	packet := make([]byte, 14)
	packet[12] = 0xc0
	packet[13] = 0x0c
	_, _, err := decodeDNSName(packet, 12)
	if err == nil {
		t.Fatal("compression loop was not rejected")
	}
}

func TestParseCAARData(t *testing.T) {
	tests := []struct {
		name    string
		rdata   []byte
		wantErr bool
	}{
		{name: "valid", rdata: testCAARData(0, "issuewild", "ca.example"), wantErr: false},
		{name: "short", rdata: []byte{0}, wantErr: true},
		{name: "zero tag", rdata: []byte{0, 0}, wantErr: true},
		{name: "tag beyond rdata", rdata: []byte{0, 5, 'a'}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, err := parseCAARData(test.rdata)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, test.wantErr)
			}
			if !test.wantErr && (record.Tag != "issuewild" || record.Value != "ca.example") {
				t.Fatalf("record = %#v", record)
			}
		})
	}
}

func TestBuildCAAQuery(t *testing.T) {
	query, _, err := buildCAAQuery("example.com")
	if err != nil {
		t.Fatalf("buildCAAQuery returned error: %v", err)
	}
	if len(query) < 12 {
		t.Fatal("query too short")
	}
	if binary.BigEndian.Uint16(query[4:6]) != 1 {
		t.Fatalf("QDCOUNT = %d", binary.BigEndian.Uint16(query[4:6]))
	}
	qtype := binary.BigEndian.Uint16(query[len(query)-4 : len(query)-2])
	qclass := binary.BigEndian.Uint16(query[len(query)-2:])
	if qtype != dnsTypeCAA || qclass != dnsClassIN {
		t.Fatalf("question type/class = %d/%d", qtype, qclass)
	}
}

func TestAddressWithPort(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1":          "127.0.0.1:53",
		"127.0.0.1:5353":     "127.0.0.1:5353",
		"2001:db8::1":        "[2001:db8::1]:53",
		"[2001:db8::1]":      "[2001:db8::1]:53",
		"[2001:db8::1]:5353": "[2001:db8::1]:5353",
		"ns.example.com":     "ns.example.com:53",
	}
	for input, want := range tests {
		if got := addressWithPort(input, "53"); got != want {
			t.Fatalf("addressWithPort(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDNSResponseQuestionMismatch(t *testing.T) {
	id := uint16(0x7777)
	response := testResponse(id, 0x8180, 1, 0, 0, 0, testQuestion("other.example.com"))
	_, err := (&DNSResolver{}).parseResponse(response, id, "example.com")
	if err == nil {
		t.Fatal("question mismatch was not rejected")
	}
}

func TestDNSResolverUDPIntegration(t *testing.T) {
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local UDP sockets are unavailable: %v", err)
	}
	t.Cleanup(func() { _ = packetConn.Close() })

	serverErr := make(chan error, 1)
	go func() {
		buffer := make([]byte, 65535)
		n, address, err := packetConn.ReadFrom(buffer)
		if err != nil {
			serverErr <- err
			return
		}
		query := buffer[:n]
		if len(query) < 12 {
			serverErr <- errors.New("short query")
			return
		}
		id := binary.BigEndian.Uint16(query[0:2])
		response := testResponse(id, 0x81a0, 1, 1, 0, 0,
			query[12:],
			testRR("example.com", dnsTypeCAA, testCAARData(0, "issue", "good-ca.example")),
		)
		_, err = packetConn.WriteTo(response, address)
		serverErr <- err
	}()

	resolver := &DNSResolver{
		Servers: []string{packetConn.LocalAddr().String()},
		Timeout: time.Second,
	}
	result, err := resolver.LookupCAA(t.Context(), "example.com")
	if err != nil {
		t.Fatalf("LookupCAA returned error: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Value != "good-ca.example" {
		t.Fatalf("Records = %#v", result.Records)
	}
	if result.DNSSEC != DNSSECSecure {
		t.Fatalf("DNSSEC = %s", result.DNSSEC)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test DNS server: %v", err)
	}
}

func TestDNSResolverTimeout(t *testing.T) {
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local UDP sockets are unavailable: %v", err)
	}
	t.Cleanup(func() { _ = packetConn.Close() })

	resolver := &DNSResolver{
		Servers: []string{packetConn.LocalAddr().String()},
		Timeout: 20 * time.Millisecond,
	}
	result, err := resolver.LookupCAA(t.Context(), "example.com")
	if err == nil {
		t.Fatalf("LookupCAA succeeded without a DNS response: %#v", result)
	}
}
