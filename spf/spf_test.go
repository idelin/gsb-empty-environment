package spf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

type fakeDNS struct {
	txt     map[string][]string
	ips     map[string][]net.IP
	mx      map[string][]*net.MX
	ptr     map[string][]string
	txtErr  map[string]error
	ipErr   map[string]error
	mxErr   map[string]error
	ptrErr  map[string]error
	txtSeen int
	ipSeen  int
	mxSeen  int
	ptrSeen int
}

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.txtSeen++
	if err, ok := f.txtErr[name]; ok {
		return nil, err
	}
	return f.txt[name], nil
}

func (f *fakeDNS) LookupIP(_ context.Context, _ string, name string) ([]net.IP, error) {
	f.ipSeen++
	if err, ok := f.ipErr[name]; ok {
		return nil, err
	}
	return f.ips[name], nil
}

func (f *fakeDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	f.mxSeen++
	if err, ok := f.mxErr[name]; ok {
		return nil, err
	}
	return f.mx[name], nil
}

func (f *fakeDNS) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.ptrSeen++
	if err, ok := f.ptrErr[addr]; ok {
		return nil, err
	}
	return f.ptr[addr], nil
}

func newFake() *fakeDNS {
	return &fakeDNS{
		txt:    make(map[string][]string),
		ips:    make(map[string][]net.IP),
		mx:     make(map[string][]*net.MX),
		ptr:    make(map[string][]string),
		txtErr: make(map[string]error),
		ipErr:  make(map[string]error),
		mxErr:  make(map[string]error),
		ptrErr: make(map[string]error),
	}
}

func evaluate(t *testing.T, resolver Resolver, ip, domain, record string) Evaluation {
	t.Helper()
	if record != "" {
		resolver.(*fakeDNS).txt[domain] = []string{record}
	}
	result, err := Evaluate(context.Background(), Options{
		IP:       net.ParseIP(ip),
		Domain:   domain,
		Resolver: resolver,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return result
}

func TestIP4AndAllDoNotCountAsDNSLookups(t *testing.T) {
	dns := newFake()
	result := evaluate(t, dns, "198.51.100.10", "example.com",
		"v=spf1 ip4:198.51.100.0/24 ip6:2001:db8::/32 -all")
	if result.Result != Pass {
		t.Fatalf("result = %s, want pass (%s)", result.Result, result.Explanation)
	}
	if result.DNSLookups != 0 || dns.txtSeen != 1 {
		t.Fatalf("lookups = %d, txt queries = %d; want 0 mechanism lookups", result.DNSLookups, dns.txtSeen)
	}
}

func TestTenDNSLookupsAllowedAndEleventhIsPermError(t *testing.T) {
	dns := newFake()
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("host%d.example.com", i)
		dns.ips[name] = []net.IP{net.ParseIP("192.0.2.1")}
	}
	record := "v=spf1"
	for i := 0; i < 10; i++ {
		record += fmt.Sprintf(" a:host%d.example.com", i)
	}
	record += " -all"
	result := evaluate(t, dns, "198.51.100.10", "example.com", record)
	if result.Result != Fail {
		t.Fatalf("10 lookups result = %s, want fail", result.Result)
	}
	if result.DNSLookups != 10 {
		t.Fatalf("lookups = %d, want 10", result.DNSLookups)
	}

	dns = newFake()
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("host%d.example.com", i)
		dns.ips[name] = []net.IP{net.ParseIP("192.0.2.1")}
	}
	record = "v=spf1"
	for i := 0; i < 11; i++ {
		record += fmt.Sprintf(" a:host%d.example.com", i)
	}
	record += " -all"
	result = evaluate(t, dns, "198.51.100.10", "example.com", record)
	if result.Result != PermError {
		t.Fatalf("11 lookups result = %s, want permerror", result.Result)
	}
	if result.DNSLookups != 11 {
		t.Fatalf("lookups = %d, want attempted count 11", result.DNSLookups)
	}
}

func TestThirdVoidLookupIsPermError(t *testing.T) {
	dns := newFake()
	result := evaluate(t, dns, "198.51.100.10", "example.com",
		"v=spf1 exists:void1.example exists:void2.example exists:void3.example -all")
	if result.Result != PermError {
		t.Fatalf("result = %s, want permerror (%s)", result.Result, result.Explanation)
	}
	if result.VoidLookups != 3 {
		t.Fatalf("void lookups = %d, want 3", result.VoidLookups)
	}
}

func TestIncludeFailSoftfailNeutralContinue(t *testing.T) {
	dns := newFake()
	dns.txt["fail.example"] = []string{"v=spf1 -all"}
	dns.txt["soft.example"] = []string{"v=spf1 ~all"}
	dns.txt["neutral.example"] = []string{"v=spf1 ?all"}
	result := evaluate(t, dns, "198.51.100.10", "example.com",
		"v=spf1 include:fail.example include:soft.example include:neutral.example ip4:198.51.100.10 -all")
	if result.Result != Pass {
		t.Fatalf("result = %s, want pass (%s)", result.Result, result.Explanation)
	}
}

func TestIncludePassAppliesQualifier(t *testing.T) {
	cases := []struct {
		mechanism string
		want      Result
	}{
		{"include:pass.example", Pass},
		{"-include:pass.example", Fail},
		{"~include:pass.example", SoftFail},
		{"?include:pass.example", Neutral},
	}
	for _, test := range cases {
		t.Run(test.mechanism, func(t *testing.T) {
			dns := newFake()
			dns.txt["pass.example"] = []string{"v=spf1 +all"}
			result := evaluate(t, dns, "198.51.100.10", "example.com",
				"v=spf1 "+test.mechanism+" -all")
			if result.Result != test.want {
				t.Fatalf("result = %s, want %s", result.Result, test.want)
			}
		})
	}
}

func TestIncludeTemporaryErrorPropagates(t *testing.T) {
	dns := newFake()
	dns.txt["include.example"] = []string{"v=spf1 a:timeout.example -all"}
	dns.ipErr["timeout.example"] = &net.DNSError{Err: "timeout", IsTimeout: true}
	result := evaluate(t, dns, "198.51.100.10", "example.com",
		"v=spf1 include:include.example ip4:198.51.100.10 -all")
	if result.Result != TempError {
		t.Fatalf("result = %s, want temperror (%s)", result.Result, result.Explanation)
	}
}

func TestRedirectOnlyWithoutAll(t *testing.T) {
	dns := newFake()
	dns.txt["target.example"] = []string{"v=spf1 +all"}
	result := evaluate(t, dns, "198.51.100.10", "example.com", "v=spf1 redirect=target.example")
	if result.Result != Pass {
		t.Fatalf("redirect result = %s, want pass", result.Result)
	}

	dns = newFake()
	dns.txt["target.example"] = []string{"v=spf1 +all"}
	result = evaluate(t, dns, "198.51.100.10", "example.com",
		"v=spf1 -all redirect=target.example")
	if result.Result != Fail {
		t.Fatalf("redirect-after-all result = %s, want fail", result.Result)
	}
	if dns.txtSeen != 1 {
		t.Fatalf("redirect target was queried despite all: %d TXT queries", dns.txtSeen)
	}
}

func TestMXAndPTRMechanisms(t *testing.T) {
	dns := newFake()
	dns.mx["example.com"] = []*net.MX{{Host: "mail.example.com."}}
	dns.ips["mail.example.com"] = []net.IP{net.ParseIP("198.51.100.10")}
	result := evaluate(t, dns, "198.51.100.10", "example.com", "v=spf1 mx -all")
	if result.Result != Pass {
		t.Fatalf("mx result = %s, want pass", result.Result)
	}

	dns = newFake()
	dns.ptr["198.51.100.10"] = []string{"smtp.example.com."}
	result = evaluate(t, dns, "198.51.100.10", "example.com", "v=spf1 ptr -all")
	if result.Result != Pass {
		t.Fatalf("ptr result = %s, want pass", result.Result)
	}
}

func TestACIDRDualForms(t *testing.T) {
	cases := []struct {
		name      string
		mechanism string
	}{
		{"colon", "a:mail.example.com/24"},
		{"bare-v4", "a/24"},
		{"dual", "a:mail.example.com/24//64"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dns := newFake()
			dns.ips["mail.example.com"] = []net.IP{net.ParseIP("198.51.100.77")}
			if test.name == "bare-v4" {
				dns.ips["example.com"] = []net.IP{net.ParseIP("198.51.100.77")}
			}
			result := evaluate(t, dns, "198.51.100.10", "example.com", "v=spf1 "+test.mechanism+" -all")
			if result.Result != Pass {
				t.Fatalf("result = %s (%s)", result.Result, result.Explanation)
			}
		})
	}
}

func TestMultipleSPFRecordsPermError(t *testing.T) {
	dns := newFake()
	dns.txt["example.com"] = []string{"v=spf1 +all", "v=spf1 -all"}
	result := evaluate(t, dns, "198.51.100.10", "example.com", "")
	if result.Result != PermError {
		t.Fatalf("result = %s, want permerror", result.Result)
	}
}

func TestTemporaryTopLevelDNSIsTempError(t *testing.T) {
	dns := newFake()
	dns.txtErr["example.com"] = errors.New("temporary DNS failure")
	dns.ipErr["example.com"] = &net.DNSError{Err: "timeout", IsTimeout: true}
	result, err := Evaluate(context.Background(), Options{
		IP: net.ParseIP("198.51.100.10"), Domain: "example.com", Resolver: dns,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != TempError {
		t.Fatalf("result = %s, want temperror", result.Result)
	}
}
