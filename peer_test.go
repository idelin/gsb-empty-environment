package netsec

import (
	"errors"
	"net"
	"testing"
)

func TestPeerAddressClassification(t *testing.T) {
	tests := []struct {
		ip     string
		unsafe bool
	}{
		{"127.0.0.1", true},
		{"127.255.255.255", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"169.254.1.1", true},
		{"224.0.0.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"240.0.0.1", true},
		{"::", true},
		{"::1", true},
		{"fc00::1", true},
		{"fd12:3456:789a::1", true},
		{"fe80::1", true},
		{"ff00::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"::ffff:192.168.1.1", true},
		{"::10.0.0.1", true},
		{"::c0a8:101", true},
		{"2002:7f00:0001::1", true},                    // 6to4 embedding 127.0.0.1
		{"2002:0a00:0001::1", true},                    // 6to4 embedding 10.0.0.1
		{"2001:0:4136:e378:8000:63bf:f5ff:fffe", true}, // Teredo embedding 10.0.0.1
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"192.169.0.1", false},
		{"8.8.8.8", false},
		{"203.0.113.5", false},
		{"2001:4860:4860::8888", false},
		{"2001:db8::1", false},
		{"2002:0808:0808::1", false},                    // 6to4 embedding 8.8.8.8
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", false}, // Teredo embedding 192.0.2.45
	}
	for _, test := range tests {
		t.Run(test.ip, func(t *testing.T) {
			ip := net.ParseIP(test.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) failed", test.ip)
			}
			if got := IsPrivateOrLoopback(ip); got != test.unsafe {
				t.Errorf("IsPrivateOrLoopback(%s) = %v, want %v", test.ip, got, test.unsafe)
			}
			err := CheckPeerAddress(ip)
			if test.unsafe {
				if !errors.Is(err, ErrForbiddenPeer) {
					t.Errorf("CheckPeerAddress(%s) = %v, want ErrForbiddenPeer", test.ip, err)
				}
			} else if err != nil {
				t.Errorf("CheckPeerAddress(%s) = %v, want nil", test.ip, err)
			}
			if allowed := IsPeerAddressAllowed(ip); allowed == test.unsafe {
				t.Errorf("IsPeerAddressAllowed(%s) = %v", test.ip, allowed)
			}
		})
	}
}

func TestPeerPolicyFlags(t *testing.T) {
	loopback := net.ParseIP("127.0.0.1")
	private := net.ParseIP("10.1.2.3")
	public := net.ParseIP("8.8.8.8")

	if err := (PeerPolicy{AllowLoopback: true}).Check(loopback); err != nil {
		t.Errorf("AllowLoopback rejected loopback: %v", err)
	}
	if err := (PeerPolicy{AllowLoopback: true}).Check(private); !errors.Is(err, ErrForbiddenPeer) {
		t.Errorf("AllowLoopback accepted private: %v", err)
	}
	if err := (PeerPolicy{AllowPrivate: true}).Check(private); err != nil {
		t.Errorf("AllowPrivate rejected private: %v", err)
	}
	if err := (PeerPolicy{AllowPrivate: true}).Check(loopback); !errors.Is(err, ErrForbiddenPeer) {
		t.Errorf("AllowPrivate accepted loopback: %v", err)
	}
	if err := (PeerPolicy{AllowUnsafe: true}).Check(loopback); err != nil {
		t.Errorf("AllowUnsafe rejected loopback: %v", err)
	}
	if err := (PeerPolicy{}).Check(public); err != nil {
		t.Errorf("default policy rejected public address: %v", err)
	}
}

func TestPeerPolicyExplicitCIDR(t *testing.T) {
	_, network, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	policy := PeerPolicy{Allowed: []*net.IPNet{network}}
	if err := policy.Check(net.ParseIP("192.168.1.50")); err != nil {
		t.Errorf("explicit CIDR rejected: %v", err)
	}
	if err := policy.Check(net.ParseIP("192.168.2.50")); !errors.Is(err, ErrForbiddenPeer) {
		t.Errorf("address outside explicit CIDR accepted: %v", err)
	}
}

func TestPeerPolicyInvalidIP(t *testing.T) {
	for _, ip := range []net.IP{nil, {}, {1, 2, 3}} {
		if err := CheckPeerAddress(ip); !errors.Is(err, ErrForbiddenPeer) {
			t.Errorf("CheckPeerAddress(%v) = %v, want ErrForbiddenPeer", ip, err)
		}
	}
}
