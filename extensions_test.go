package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func extBytes(extType uint16, data []byte) []byte {
	out := []byte{byte(extType >> 8), byte(extType), byte(len(data) >> 8), byte(len(data))}
	return append(out, data...)
}

func sniData(names ...string) []byte {
	var body []byte
	for _, n := range names {
		body = append(body, 0, byte(len(n)>>8), byte(len(n)))
		body = append(body, n...)
	}
	return append([]byte{byte(len(body) >> 8), byte(len(body))}, body...)
}

func alpnData(protos ...string) []byte {
	var body []byte
	for _, p := range protos {
		body = append(body, byte(len(p)))
		body = append(body, p...)
	}
	return append([]byte{byte(len(body) >> 8), byte(len(body))}, body...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Nested lists: SNI and ALPN carry their own inner length prefixes, and an
// unknown extension sits between them. Wire order must be preserved and
// the unknown type must stay out of the typed accessors.
func TestNestedListsOrderAndUnknown(t *testing.T) {
	const unknownType = 0xFF01
	block := concat(
		extBytes(unknownType, []byte{1, 2, 3}),
		extBytes(ExtServerName, sniData("example.com")),
		extBytes(ExtALPN, alpnData("h2", "http/1.1")),
		extBytes(unknownType, []byte{4, 5}), // duplicate unknown is allowed
	)
	tab, err := ParseExtensions(block)
	if err != nil {
		t.Fatalf("ParseExtensions: %v", err)
	}

	all := tab.All()
	wantTypes := []uint16{unknownType, ExtServerName, ExtALPN, unknownType}
	if len(all) != len(wantTypes) {
		t.Fatalf("All() has %d extensions, want %d", len(all), len(wantTypes))
	}
	for i, wt := range wantTypes {
		if all[i].Type != wt {
			t.Errorf("All()[%d].Type = %d, want %d (order not preserved)", i, all[i].Type, wt)
		}
	}

	if _, ok := tab.Get(unknownType); ok {
		t.Error("Get returned an unknown extension; unknown types must not join known semantics")
	}
	if got, ok := tab.Get(ExtServerName); !ok || got.Type != ExtServerName {
		t.Errorf("Get(ExtServerName) = %+v, %v", got, ok)
	}
	if name, ok := tab.ServerName(); !ok || name != "example.com" {
		t.Errorf("ServerName() = %q, %v; want %q, true", name, ok, "example.com")
	}
	protos, ok := tab.ALPNProtocols()
	if !ok || !reflect.DeepEqual(protos, []string{"h2", "http/1.1"}) {
		t.Errorf("ALPNProtocols() = %v, %v", protos, ok)
	}
}

// A duplicated security-critical extension (ALPN here) must be rejected
// outright, so parser and policy can never disagree on which copy to use.
func TestDuplicateALPNRejected(t *testing.T) {
	block := concat(
		extBytes(ExtALPN, alpnData("h2")),
		extBytes(ExtALPN, alpnData("http/1.1")),
	)
	if _, err := ParseExtensions(block); !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("duplicate ALPN: err = %v, want ErrDuplicateExtension", err)
	}

	block = concat(
		extBytes(ExtServerName, sniData("a.example")),
		extBytes(ExtServerName, sniData("b.example")),
	)
	if _, err := ParseExtensions(block); !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("duplicate SNI: err = %v, want ErrDuplicateExtension", err)
	}
}

// Empty SNI: an empty server_name_list is tolerated and simply yields no
// name, while a zero-length entry inside the list is malformed.
func TestEmptySNI(t *testing.T) {
	tab, err := ParseExtensions(extBytes(ExtServerName, sniData()))
	if err != nil {
		t.Fatalf("empty server_name_list: %v", err)
	}
	if name, ok := tab.ServerName(); ok || name != "" {
		t.Errorf("ServerName() = %q, %v; want empty, false", name, ok)
	}
	if _, ok := tab.Get(ExtServerName); !ok {
		t.Error("Get(ExtServerName): extension present but not reported")
	}

	zeroEntry := []byte{0, 3, 0, 0, 0} // list_len=3, name_type=0, name_len=0
	if _, err := ParseExtensions(extBytes(ExtServerName, zeroEntry)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("zero-length SNI entry: err = %v, want ErrMalformed", err)
	}
}

// Truncation at every byte boundary of a single-extension block must fail
// before any table is published.
func TestTruncation(t *testing.T) {
	full := extBytes(ExtALPN, alpnData("h2", "http/1.1"))
	for cut := 1; cut < len(full); cut++ {
		if tab, err := ParseExtensions(full[:cut]); err == nil {
			t.Fatalf("cut at %d: expected error, got table %+v", cut, tab)
		} else if !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut at %d: err = %v, want ErrTruncated", cut, err)
		}
	}

	// Nested length prefixes lying about their contents.
	sniShort := []byte{0, 20, 0, 0, 3, 'a', 'b', 'c'} // declares 20, carries 5
	if _, err := ParseExtensions(extBytes(ExtServerName, sniShort)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("SNI list length mismatch: err = %v, want ErrMalformed", err)
	}
	sniEntryCut := []byte{0, 4, 0, 0, 10, 'x'} // entry declares 10, carries 1
	if _, err := ParseExtensions(extBytes(ExtServerName, sniEntryCut)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("SNI entry truncated: err = %v, want ErrTruncated", err)
	}
	alpnCut := []byte{0, 1, 5} // proto declares 5 bytes, carries 0
	if _, err := ParseExtensions(extBytes(ExtALPN, alpnCut)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("ALPN proto truncated: err = %v, want ErrTruncated", err)
	}
}

// The policy layer must observe fields decoded from the same unique
// instance the parser stored: Get and All share backing data, and the
// typed accessors agree with a manual decode of that instance.
func TestPolicySeesUniqueInstance(t *testing.T) {
	block := concat(
		extBytes(ExtServerName, sniData("unique.example")),
		extBytes(ExtALPN, alpnData("h2")),
	)
	tab, err := ParseExtensions(block)
	if err != nil {
		t.Fatalf("ParseExtensions: %v", err)
	}

	got, ok := tab.Get(ExtServerName)
	if !ok {
		t.Fatal("Get(ExtServerName) missing")
	}
	var fromAll *Extension
	for i, e := range tab.All() {
		if e.Type == ExtServerName {
			fromAll = &tab.All()[i]
		}
	}
	if fromAll == nil {
		t.Fatal("server_name missing from All()")
	}
	if &got.Data[0] != &fromAll.Data[0] {
		t.Error("Get and All expose different copies of server_name data")
	}

	name, ok := tab.ServerName()
	if !ok || name != "unique.example" {
		t.Fatalf("ServerName() = %q, %v", name, ok)
	}
	reparsed, err := parseSNI(got.Data)
	if err != nil || reparsed != name {
		t.Errorf("policy view %q differs from stored instance %q (err %v)", name, reparsed, err)
	}

	protos, ok := tab.ALPNProtocols()
	if !ok || !reflect.DeepEqual(protos, []string{"h2"}) {
		t.Errorf("ALPNProtocols() = %v, %v", protos, ok)
	}
	alpnExt, _ := tab.Get(ExtALPN)
	reparsedALPN, err := parseALPN(alpnExt.Data)
	if err != nil || !reflect.DeepEqual(reparsedALPN, protos) {
		t.Errorf("policy ALPN %v differs from stored instance %v (err %v)", protos, reparsedALPN, err)
	}
}

// Mutating the caller's input buffer after parsing must not change the
// published table.
func TestInputCopiedBeforePublish(t *testing.T) {
	block := extBytes(ExtServerName, sniData("stable.example"))
	tab, err := ParseExtensions(block)
	if err != nil {
		t.Fatalf("ParseExtensions: %v", err)
	}
	for i := range block {
		block[i] = 0
	}
	if name, ok := tab.ServerName(); !ok || name != "stable.example" {
		t.Errorf("ServerName() = %q, %v after input mutation", name, ok)
	}
}
