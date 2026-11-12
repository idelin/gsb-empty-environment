package netsec_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"

	netsec "example.org/netsec/q0263"
)

type fakeResolver struct {
	mu      sync.Mutex
	answers []netip.Addr
	err     error
	calls   int
	host    string
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.host = host
	if r.err != nil {
		return nil, r.err
	}
	return append([]netip.Addr(nil), r.answers...), nil
}

func (r *fakeResolver) setAnswers(answers ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers = make([]netip.Addr, len(answers))
	for i, answer := range answers {
		r.answers[i] = netip.MustParseAddr(answer)
	}
}

func (r *fakeResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestParseTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		host string
		port uint16
	}{
		{
			name: "plain",
			path: "/.well-known/masque/udp/example.com/443/",
			host: "example.com",
			port: 443,
		},
		{
			name: "encoded host",
			path: "/.well-known/masque/udp/%65xample.com/443/",
			host: "example.com",
			port: 443,
		},
		{
			name: "encoded port",
			path: "/.well-known/masque/udp/example.com/%34%34%33/",
			host: "example.com",
			port: 443,
		},
		{
			name: "IPv6 literal",
			path: "/.well-known/masque/udp/2001%3Adb8%3A%3A1/443/",
			host: "2001:db8::1",
			port: 443,
		},
		{
			name: "highest port",
			path: "/.well-known/masque/udp/example.com/65535/",
			host: "example.com",
			port: 65535,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := netsec.ParseTarget(tc.path)
			if err != nil {
				t.Fatalf("ParseTarget(%q): %v", tc.path, err)
			}
			if got.Host != tc.host || got.Port != tc.port {
				t.Fatalf("ParseTarget(%q) = %+v, want host=%q port=%d", tc.path, got, tc.host, tc.port)
			}
		})
	}
}

func TestParseTargetRejectsEncodingBypasses(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/.well-known/masque/udp//443/",
		"/.well-known/masque/udp/example.com//",
		"/.well-known/masque/udp/example.com/443",
		"/.well-known/masque/udp/example.com/443/extra/",
		"/.well-known/masque/udp/example.com/443//",
		"/.well-known%2fmasque/udp/example.com/443/",
		"/.well-known/masque/udp/exa%2Fmple.com/443/",
		"/.well-known/masque/udp/exa%5Cmple.com/443/",
		"/.well-known/masque/udp/example.com%2Ffoo/443/",
		"/.well-known/masque/udp/example.com%252Ffoo/443/",
		"/.well-known/masque/udp/example.com%25252Ffoo/443/",
		"/.well-known/masque/udp/example.com%253Ffoo/443/",
		"/.well-known/masque/udp/example.com%2523foo/443/",
		"/.well-known/masque/udp/example.com/4%32%33%252F/",
		"/.well-known/masque/udp/example.com/4%30%30%2F/",
		"/.well-known/masque/udp/example.com/44%5C3/",
		"/.well-known/masque/udp/example.com/443%253F/",
		"/.well-known/masque/udp/example.com/0/",
		"/.well-known/masque/udp/example.com/65536/",
		"/.well-known/masque/udp/example.com/+443/",
		"/.well-known/masque/udp/example.com/-443/",
		"/.well-known/masque/udp/example.com/%00/",
		"/.well-known/masque/udp/example.com/%ff/",
		"/.well-known/masque/udp/example.com/%2e%2e/443/",
		"/.well-known/masque/udp/example.com%40evil.example/443/",
		"/.well-known/masque/udp/example.com%3A443/443/",
		"/.well-known/masque/udp/./443/",
		"/.well-known/masque/udp/example.com/443/?target=evil",
	}

	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			_, err := netsec.ParseTarget(path)
			if err == nil {
				t.Fatalf("ParseTarget(%q) unexpectedly succeeded", path)
			}
			if !errors.Is(err, netsec.ErrInvalidTargetPath) {
				t.Fatalf("ParseTarget(%q) error = %v, want ErrInvalidTargetPath", path, err)
			}
		})
	}
}

func TestAdmitRequiresEveryDNSAnswerToPassAllowlist(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{}
	resolver.setAnswers("2001:db8:ffff::1", "8.8.8.8")
	allow := netsec.AllowPrefixes(netip.MustParsePrefix("8.8.8.0/24"))
	admitter := netsec.NewAdmitter(resolver, allow)

	_, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/rebind.example/443/")
	if err == nil {
		t.Fatal("target with one allowlisted and one unlisted address was admitted")
	}
	if !errors.Is(err, netsec.ErrTargetNotAllowed) {
		t.Fatalf("error = %v, want ErrTargetNotAllowed", err)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver called %d times, want 1", resolver.callCount())
	}
}

func TestAdmitRejectsInternalAnswersEvenWhenCallerAllowsAll(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"127.0.0.1",
		"127.0.0.2",
		"::1",
		"169.254.0.1",
		"fe80::1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.0.1",
		"fc00::1",
		"fd12:3456::1",
		"::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
		"100.64.0.1",
		"0.0.0.0",
		"::",
		"224.0.0.1",
		"ff02::1",
	}

	for _, answer := range blocked {
		answer := answer
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			resolver := &fakeResolver{}
			resolver.setAnswers(answer)
			admitter := netsec.NewAdmitter(resolver, func(netip.Addr) bool { return true })

			_, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/attacker.example/443/")
			if err == nil {
				t.Fatalf("admitted internal DNS answer %s", answer)
			}
			if !errors.Is(err, netsec.ErrTargetNotAllowed) {
				t.Fatalf("error = %v, want ErrTargetNotAllowed", err)
			}
		})
	}
}

func TestAdmitPinsPublicAddresses(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{}
	resolver.setAnswers("8.8.8.8", "2001:4860:4860::8888", "8.8.8.8")
	allow := netsec.AllowPrefixes(
		netip.MustParsePrefix("8.8.8.0/24"),
		netip.MustParsePrefix("2001:4860::/32"),
	)
	admitter := netsec.NewAdmitter(resolver, allow)

	got, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/dns.example/443/")
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver called %d times, want 1", resolver.callCount())
	}
	if resolver.host != "dns.example" {
		t.Fatalf("resolved host %q, want dns.example", resolver.host)
	}
	if got.Host != "dns.example" || got.Port != 443 {
		t.Fatalf("target = %+v, want dns.example:443", got)
	}
	if len(got.Addresses) != 2 {
		t.Fatalf("addresses = %v, want two deduplicated addresses", got.Addresses)
	}
	ports := got.AddrPorts()
	if len(ports) != 2 || ports[0] != netip.MustParseAddrPort("8.8.8.8:443") ||
		ports[1] != netip.AddrPortFrom(netip.MustParseAddr("2001:4860:4860::8888"), 443) {
		t.Fatalf("AddrPorts = %v", ports)
	}
}

func TestAdmitRejectsDNSRebinding(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{}
	resolver.setAnswers("8.8.8.8")
	admitter := netsec.NewAdmitter(resolver, netsec.AllowPrefixes(netip.MustParsePrefix("8.8.8.0/24")))

	first, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/rebind.example/443/")
	if err != nil {
		t.Fatalf("first Admit: %v", err)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver calls after first admission = %d, want 1", resolver.callCount())
	}

	resolver.setAnswers("10.0.0.1")

	// The admitted value contains a fixed IP. Dialing it cannot cause a second
	// DNS lookup that the attacker could rebind to an internal address.
	if got := first.AddrPorts()[0]; got != netip.MustParseAddrPort("8.8.8.8:443") {
		t.Fatalf("first admitted destination = %v, want fixed 8.8.8.8:443", got)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("using admitted destination caused a DNS lookup; calls = %d", resolver.callCount())
	}

	second, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/rebind.example/443/")
	if err == nil {
		t.Fatalf("second admission after rebinding returned %+v", second)
	}
	if !errors.Is(err, netsec.ErrTargetNotAllowed) {
		t.Fatalf("second admission error = %v, want ErrTargetNotAllowed", err)
	}
}

func TestAdmitRejectsLiteralInternalIPWithoutDNS(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/.well-known/masque/udp/127.0.0.1/443/",
		"/.well-known/masque/udp/%31%32%37.0.0.1/443/",
		"/.well-known/masque/udp/::ffff%3A127.0.0.1/443/",
		"/.well-known/masque/udp/fe80%3A%3A1/443/",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			resolver := &fakeResolver{}
			admitter := netsec.NewAdmitter(resolver, func(netip.Addr) bool { return true })

			_, err := admitter.Admit(context.Background(), path)
			if err == nil {
				t.Fatalf("admitted literal internal target %q", path)
			}
			if !errors.Is(err, netsec.ErrTargetNotAllowed) {
				t.Fatalf("error = %v, want ErrTargetNotAllowed", err)
			}
			if resolver.callCount() != 0 {
				t.Fatalf("literal IP path used DNS %d times", resolver.callCount())
			}
		})
	}
}

func TestAdmitFailsClosed(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{}
	resolver.setAnswers("8.8.8.8")

	t.Run("nil allowlist", func(t *testing.T) {
		t.Parallel()
		admitter := netsec.NewAdmitter(resolver, nil)
		if _, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/example.com/443/"); !errors.Is(err, netsec.ErrTargetNotAllowed) {
			t.Fatalf("error = %v, want ErrTargetNotAllowed", err)
		}
	})

	t.Run("empty DNS answer", func(t *testing.T) {
		t.Parallel()
		empty := &fakeResolver{}
		admitter := netsec.NewAdmitter(empty, func(netip.Addr) bool { return true })
		if _, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/example.com/443/"); !errors.Is(err, netsec.ErrTargetNotAllowed) {
			t.Fatalf("error = %v, want ErrTargetNotAllowed", err)
		}
	})

	t.Run("resolver error", func(t *testing.T) {
		t.Parallel()
		failing := &fakeResolver{err: errors.New("lookup failed")}
		admitter := netsec.NewAdmitter(failing, func(netip.Addr) bool { return true })
		if _, err := admitter.Admit(context.Background(), "/.well-known/masque/udp/example.com/443/"); !errors.Is(err, netsec.ErrTargetNotAllowed) {
			t.Fatalf("error = %v, want ErrTargetNotAllowed", err)
		}
	})
}

func TestAdmitRequestUsesEscapedPath(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{}
	admitter := netsec.NewAdmitter(resolver, func(netip.Addr) bool { return true })
	r := httptest.NewRequest("GET", "https://proxy.example/.well-known/masque/udp/evil%2Finternal/443/", nil)

	if _, err := admitter.AdmitRequest(r); !errors.Is(err, netsec.ErrInvalidTargetPath) {
		t.Fatalf("AdmitRequest error = %v, want ErrInvalidTargetPath", err)
	}
	if resolver.callCount() != 0 {
		t.Fatalf("invalid escaped path reached DNS %d times", resolver.callCount())
	}
}

func TestAdmitRequestAcceptsCanonicalPath(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{}
	resolver.setAnswers("8.8.8.8")
	admitter := netsec.NewAdmitter(resolver, netsec.AllowPrefixes(netip.MustParsePrefix("8.8.8.0/24")))
	r := httptest.NewRequest("GET", "https://proxy.example/.well-known/masque/udp/example.com/443/", nil)

	got, err := admitter.AdmitRequest(r)
	if err != nil {
		t.Fatalf("AdmitRequest: %v", err)
	}
	if got.Host != "example.com" || got.Port != 443 || len(got.Addresses) != 1 {
		t.Fatalf("target = %+v", got)
	}
}
