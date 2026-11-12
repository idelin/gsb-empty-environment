package netsec

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestAuthorize_UnconstrainedCertificate(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)

	spec := certSpec{
		certType:   CertTypeUser,
		principals: []string{"alice"},
		// Zero time bounds mean no lower/upper restriction.
	}
	raw := ca.mint(spec)
	req := Request{
		RawCertificate: raw,
		Login:          "alice",
		RemoteAddr:     peerIP,
	}
	decision, err := authz.Authorize(req)
	if err != nil {
		t.Fatalf("unconstrained cert denied: %v", err)
	}
	if decision.ForceCommand || decision.Command != "" {
		t.Fatalf("unexpected force command: %+v", decision)
	}
}

func TestAuthorize_MalformedSourceAddress(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)

	for _, spec := range []map[string]string{
		{OptionSourceAddress: "not-a-cidr"},
		{OptionSourceAddress: "10.0.0.0/8,"},
	} {
		c := validSpec()
		c.criticalOptions = spec
		raw := ca.mint(c)
		_, err := authz.Authorize(validRequest(raw))
		if !errors.Is(err, ErrMalformedSourceAddress) {
			t.Fatalf("want ErrMalformedSourceAddress for %v, got %v", spec, err)
		}
	}
}

func TestAuthorize_MissingPeerAndPrincipal(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)

	// source-address present but peer unknown: deny, never fail-open.
	raw := ca.mint(validSpec())
	req := validRequest(raw)
	req.RemoteAddr = netip.Addr{}
	if _, err := authz.Authorize(req); !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("missing peer want deny, got %v", err)
	}

	// Certificate with no principals cannot authorize anyone.
	empty := validSpec()
	empty.principals = nil
	raw = ca.mint(empty)
	if _, err := authz.Authorize(validRequest(raw)); !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("principal-less cert want mismatch, got %v", err)
	}

	// Empty login is rejected before mapper invocation.
	req = validRequest(ca.mint(validSpec()))
	req.Login = "   "
	if _, err := authz.Authorize(req); !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("blank login want mismatch, got %v", err)
	}
}

func TestAuthorize_MapperErrorDenies(t *testing.T) {
	ca := newEd25519CA()
	authz, err := NewAuthorizer(ca.pub,
		WithClock(func() time.Time { return baseTime }),
		WithLoginMapper(func(string) (string, error) {
			return "", errors.New("directory unavailable")
		}),
	)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	_, err = authz.Authorize(validRequest(ca.mint(validSpec())))
	if !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("mapper failure must deny, got %v", err)
	}
}

func TestCheckSourceAddress_V6Peer(t *testing.T) {
	spec := "2001:db8:1::/48"
	v6 := netip.MustParseAddr("2001:db8:1::dead:beef")
	if err := checkSourceAddress(spec, v6); err != nil {
		t.Fatalf("v6 in-prefix denied: %v", err)
	}
	if err := checkSourceAddress(spec, netip.MustParseAddr("2001:db9::1")); !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("v6 out-of-prefix want deny, got %v", err)
	}
	// v4-mapped v6 representation of an allowed v4 peer still matches.
	if err := checkSourceAddress("10.20.30.0/24", netip.MustParseAddr("::ffff:10.20.30.40")); err != nil {
		t.Fatalf("mapped v4 peer should match v4 prefix: %v", err)
	}
}
