package netsec

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestParseHostPattern(t *testing.T) {
	valid := map[string]string{
		"push.example.com": "push.example.com",
		"PUSH.Example.COM": "push.example.com",
		"*.push.example.":  "*.push.example",
	}
	for input, want := range valid {
		pattern, err := ParseHostPattern(input)
		if err != nil {
			t.Fatalf("ParseHostPattern(%q): %v", input, err)
		}
		if pattern.String() != want {
			t.Fatalf("pattern = %q, want %q", pattern.String(), want)
		}
	}

	invalid := []string{
		"",
		"10.0.0.1",
		"2001:db8::1",
		"*.example..com",
		"-bad.example.com",
		"bad-.example.com",
	}
	for _, input := range invalid {
		if _, err := ParseHostPattern(input); err == nil {
			t.Fatalf("ParseHostPattern(%q) expected error", input)
		}
	}
}

func TestHostPatternMatches(t *testing.T) {
	wildcard := mustPattern(t, "*.example.com")
	cases := map[string]bool{
		"a.example.com":   true,
		"a.b.example.com": false,
		"example.com":     false,
		"evilxample.com":  false,
	}
	for host, want := range cases {
		if got := wildcard.matches(host); got != want {
			t.Fatalf("wildcard.matches(%q) = %v, want %v", host, got, want)
		}
	}

	exact := mustPattern(t, "push.example.com")
	if exact.matches("push.example.com") != true {
		t.Fatal("exact pattern should match itself")
	}
	if exact.matches("notpush.example.com") != false {
		t.Fatal("exact pattern must not match other hosts")
	}
}

func TestIsPublicIP(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "20.205.243.166", "2606:4700:4700::1111"}
	for _, raw := range public {
		if !IsPublicIP(net.ParseIP(raw)) {
			t.Fatalf("%s should be public", raw)
		}
	}
	reserved := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255",
		"192.168.1.1", "169.254.1.1", "100.64.0.1", "0.0.0.0",
		"224.0.0.1", "240.0.0.1", "192.0.2.10", "203.0.113.7",
		"::1", "fc00::1", "fe80::1", "ff02::1", "::",
		"::ffff:192.168.0.1", // IPv4 映射地址同样按保留处理
	}
	for _, raw := range reserved {
		if IsPublicIP(net.ParseIP(raw)) {
			t.Fatalf("%s should be reserved", raw)
		}
	}
}

func TestValidateEndpointAcceptsWhitelistedHTTPS(t *testing.T) {
	resolver := fakeResolver{hosts: map[string][]net.IPAddr{
		"push.example.com": {mustIPAddr(t, "93.184.216.34")},
	}}
	guard := testGuard(t, resolver, "push.example.com")

	endpoint, err := guard.ValidateEndpoint(context.Background(), "https://push.example.com/push/abc?x=1")
	if err != nil {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
	if endpoint.Hostname() != "push.example.com" {
		t.Fatalf("host = %q", endpoint.Hostname())
	}

	// 通配白名单只放行一级子域。
	wildGuard := testGuard(t, resolver, "*.example.com")
	if _, err := wildGuard.ValidateEndpoint(context.Background(), "https://push.example.com/"); err != nil {
		t.Fatalf("wildcard subdomain rejected: %v", err)
	}
	if _, err := wildGuard.ValidateEndpoint(context.Background(), "https://a.b.example.com/"); !errors.Is(err, ErrNotWhitelisted) {
		t.Fatalf("deep subdomain: error = %v, want ErrNotWhitelisted", err)
	}
}

func TestValidateEndpointRejectsDangerousInputs(t *testing.T) {
	resolver := fakeResolver{hosts: map[string][]net.IPAddr{
		"push.example.com":  {mustIPAddr(t, "93.184.216.34")},
		"intranet.local":    {mustIPAddr(t, "10.0.0.5")},
		"mixed.example.com": {mustIPAddr(t, "93.184.216.34"), mustIPAddr(t, "169.254.169.254")},
	}}
	guard := testGuard(t, resolver, "push.example.com", "mixed.example.com", "intranet.local")

	cases := []struct {
		name     string
		endpoint string
		target   error
	}{
		{"plain http", "http://push.example.com/push", ErrNonHTTPS},
		{"http on intranet", "http://10.0.0.5/", ErrNonHTTPS},
		{"userinfo", "https://user:pass@push.example.com/", ErrEndpointUserInfo},
		{"ip literal", "https://10.0.0.5/push", ErrIPLiteral},
		{"non-whitelisted host", "https://evil.example.org/push", ErrNotWhitelisted},
		{"reserved resolved IP", "https://intranet.local/push", ErrReservedIP},
		{"mixed A records", "https://mixed.example.com/push", ErrReservedIP},
		{"non-443 port", "https://push.example.com:8443/push", ErrPortNotAllowed},
		{"empty host", "https:///push", ErrEmptyHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := guard.ValidateEndpoint(context.Background(), tc.endpoint)
			assertErrorIs(t, err, tc.target)
		})
	}
}

func TestDialContextEnforcesIPPolicy(t *testing.T) {
	resolver := fakeResolver{hosts: map[string][]net.IPAddr{
		"intranet.local": {mustIPAddr(t, "10.0.0.7")},
	}}
	guard := testGuard(t, resolver, "intranet.local")
	// 静态校验与拨号都应拒绝内网 IP（即便该主机被错误加入白名单）。
	if _, err := guard.ValidateEndpoint(context.Background(), "https://intranet.local/"); !errors.Is(err, ErrReservedIP) {
		t.Fatalf("validate: error = %v, want ErrReservedIP", err)
	}
	_, err := guard.DialContext(context.Background(), "tcp", "intranet.local:443")
	assertErrorIs(t, err, ErrReservedIP)

	// 未在白名单中的主机在拨号阶段同样被拦截，防止重定向绕过。
	_, err = guard.DialContext(context.Background(), "tcp", "evil.example.org:443")
	assertErrorIs(t, err, ErrNotWhitelisted)
}

func mustIPAddr(t *testing.T, raw string) net.IPAddr {
	t.Helper()
	ip := net.ParseIP(raw)
	if ip == nil {
		t.Fatalf("bad test IP %q", raw)
	}
	return net.IPAddr{IP: ip}
}
