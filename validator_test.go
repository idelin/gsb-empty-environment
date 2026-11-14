package netsec

import (
	"errors"
	"testing"
)

func mustURI(t *testing.T, s string) URI {
	t.Helper()
	u, err := ParseURI(s)
	if err != nil {
		t.Fatalf("ParseURI(%q): %v", s, err)
	}
	return u
}

func mustVia(t *testing.T, s string) Via {
	t.Helper()
	v, err := ParseVia(s)
	if err != nil {
		t.Fatalf("ParseVia(%q): %v", s, err)
	}
	return v
}

func newValidator() *Validator {
	return NewValidator(Config{
		OwnDomains:   []string{"proxy.example.com"},
		TrustedPeers: []string{"peer.example.net"},
	})
}

// legitInvite is a well-formed initial INVITE from a real client.
func legitInvite(t *testing.T) *Message {
	t.Helper()
	return &Message{
		Method:     "INVITE",
		RequestURI: mustURI(t, "sip:bob@proxy.example.com"),
		Vias: []Via{
			mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK776asdhds;rport=5060"),
		},
		CallID:     "call-1@example.com",
		FromTag:    "from-tag-1",
		CSeqNum:    1,
		CSeqMethod: "INVITE",
		SrcAddr:    "10.0.0.1",
		SrcPort:    5060,
	}
}

func TestForgedRouteRejected(t *testing.T) {
	v := newValidator()

	// Route header injected by the client pointing at the attacker.
	m := legitInvite(t)
	m.Routes = []URI{mustURI(t, "sip:attacker.evil.com")}
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrForgedRoute) {
		t.Fatalf("forged top Route: got %v, want ErrForgedRoute", err)
	}

	// Route set that first hits this proxy, then bounces to the attacker.
	m = legitInvite(t)
	m.Vias[0].Branch = "z9hG4bK-other"
	m.Routes = []URI{
		mustURI(t, "sip:proxy.example.com;lr"),
		mustURI(t, "sip:attacker.evil.com;lr"),
	}
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrForgedRoute) {
		t.Fatalf("forged second Route: got %v, want ErrForgedRoute", err)
	}

	// Route chain staying inside the trust set is accepted and the next
	// hop is the first non-own entry.
	m = legitInvite(t)
	m.Routes = []URI{
		mustURI(t, "sip:proxy.example.com;lr"),
		mustURI(t, "sip:peer.example.net;lr"),
	}
	dec, err := v.ValidateRequest(m)
	if err != nil {
		t.Fatalf("trusted Route chain rejected: %v", err)
	}
	if dec.NextHop.Host != "peer.example.net" {
		t.Fatalf("next hop = %s, want peer.example.net", dec.NextHop.Host)
	}
}

func TestViaRewriteRejected(t *testing.T) {
	v := newValidator()

	// Attacker rewrites sent-by so responses would be misrouted to them.
	m := legitInvite(t)
	m.Vias[0] = mustVia(t, "SIP/2.0/UDP attacker.evil.com:5060;branch=z9hG4bK776asdhds")
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrViaMismatch) {
		t.Fatalf("rewritten sent-by: got %v, want ErrViaMismatch", err)
	}

	// Attacker pre-sets received to redirect the response.
	m = legitInvite(t)
	m.Vias[0] = mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK776asdhds;received=6.6.6.6")
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrForgedReceived) {
		t.Fatalf("forged received: got %v, want ErrForgedReceived", err)
	}

	// Even with a consistent Via, the response target is the observed
	// transport peer, never header data.
	m = legitInvite(t)
	dec, err := v.ValidateRequest(m)
	if err != nil {
		t.Fatalf("legit request rejected: %v", err)
	}
	if dec.RespHost != "10.0.0.1" || dec.RespPort != 5060 {
		t.Fatalf("response target = %s:%d, want 10.0.0.1:5060", dec.RespHost, dec.RespPort)
	}
}

func TestBranchReuseRejected(t *testing.T) {
	v := newValidator()

	// Branch without the RFC 3261 magic cookie.
	m := legitInvite(t)
	m.Vias[0] = mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=legacy-branch")
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrBadBranch) {
		t.Fatalf("missing magic cookie: got %v, want ErrBadBranch", err)
	}

	// First use of a branch succeeds.
	m = legitInvite(t)
	if _, err := v.ValidateRequest(m); err != nil {
		t.Fatalf("first branch use rejected: %v", err)
	}

	// Same branch, different Call-ID: replay/hijack attempt.
	m = legitInvite(t)
	m.CallID = "call-2@example.com"
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrBranchReuse) {
		t.Fatalf("branch replayed with new Call-ID: got %v, want ErrBranchReuse", err)
	}

	// Same branch, same dialog, bumped CSeq: reuse within sender.
	m = legitInvite(t)
	m.CSeqNum = 2
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrBranchReuse) {
		t.Fatalf("branch reused with new CSeq: got %v, want ErrBranchReuse", err)
	}

	// Exact retransmission is detected, not rejected.
	m = legitInvite(t)
	dec, err := v.ValidateRequest(m)
	if err != nil {
		t.Fatalf("retransmission rejected: %v", err)
	}
	if !dec.Retransmission {
		t.Fatal("retransmission not flagged")
	}
}

func TestCrossDomainPolicy(t *testing.T) {
	v := newValidator()

	// Request-URI in a foreign domain with no trust policy.
	m := legitInvite(t)
	m.RequestURI = mustURI(t, "sip:bob@outside.example.org")
	if _, err := v.ValidateRequest(m); !errors.Is(err, ErrUntrustedDomain) {
		t.Fatalf("untrusted cross-domain: got %v, want ErrUntrustedDomain", err)
	}

	// Same target once an explicit trust policy exists.
	v2 := NewValidator(Config{
		OwnDomains:   []string{"proxy.example.com"},
		TrustedPeers: []string{"peer.example.net", "outside.example.org"},
	})
	m = legitInvite(t)
	m.RequestURI = mustURI(t, "sip:bob@outside.example.org")
	dec, err := v2.ValidateRequest(m)
	if err != nil {
		t.Fatalf("trusted cross-domain rejected: %v", err)
	}
	if dec.NextHop.Host != "outside.example.org" {
		t.Fatalf("next hop = %s, want outside.example.org", dec.NextHop.Host)
	}
}

func TestLegitimateDialog(t *testing.T) {
	v := newValidator()

	// Initial INVITE from the real client.
	invite := legitInvite(t)
	dec, err := v.ValidateRequest(invite)
	if err != nil {
		t.Fatalf("initial INVITE rejected: %v", err)
	}
	if dec.NextHop.Host != "proxy.example.com" {
		t.Fatalf("next hop = %s, want proxy.example.com", dec.NextHop.Host)
	}

	// The proxy records its own route entry for the dialog.
	v.RecordOwnRoute("call-1@example.com", mustURI(t, "sip:proxy.example.com;lr"))

	// 200 OK comes back matching the transaction; it must be forwarded to
	// the real client address observed at validation time.
	resp := &Message{
		IsResponse: true,
		StatusCode: 200,
		Vias:       invite.Vias,
		CallID:     invite.CallID,
		FromTag:    invite.FromTag,
		ToTag:      "to-tag-1",
		CSeqNum:    1,
		CSeqMethod: "INVITE",
	}
	host, port, err := v.ValidateResponse(resp)
	if err != nil {
		t.Fatalf("response rejected: %v", err)
	}
	if host != "10.0.0.1" || port != 5060 {
		t.Fatalf("response routed to %s:%d, want 10.0.0.1:5060", host, port)
	}

	// A response carrying a Via branch nobody opened is dropped.
	forged := *resp
	forged.Vias = []Via{mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-never-seen")}
	if _, _, err := v.ValidateResponse(&forged); !errors.Is(err, ErrUnknownTransaction) {
		t.Fatalf("response with unknown branch: got %v, want ErrUnknownTransaction", err)
	}

	// In-dialog request with correct tags and a higher CSeq. The client
	// injects a Record-Route pointing at the attacker; the next hop must
	// still come from the proxy's own recorded route set.
	bye := &Message{
		Method:     "BYE",
		RequestURI: mustURI(t, "sip:alice@proxy.example.com"),
		Vias: []Via{
			mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-bye1"),
		},
		RecordRoutes: []URI{mustURI(t, "sip:attacker.evil.com;lr")},
		CallID:       "call-1@example.com",
		FromTag:      "from-tag-1",
		ToTag:        "to-tag-1",
		CSeqNum:      2,
		CSeqMethod:   "BYE",
		SrcAddr:      "10.0.0.1",
		SrcPort:      5060,
	}
	dec, err = v.ValidateRequest(bye)
	if err != nil {
		t.Fatalf("in-dialog BYE rejected: %v", err)
	}
	if dec.NextHop.Host != "proxy.example.com" {
		t.Fatalf("client Record-Route changed next hop to %s", dec.NextHop.Host)
	}
	if dec.RespHost != "10.0.0.1" {
		t.Fatalf("response target = %s, want 10.0.0.1", dec.RespHost)
	}

	// Replayed request with a lower CSeq is rejected.
	replay := *bye
	replay.Vias = []Via{mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-bye2")}
	replay.CSeqNum = 1
	if _, err := v.ValidateRequest(&replay); !errors.Is(err, ErrCSeqReplay) {
		t.Fatalf("lower CSeq replay: got %v, want ErrCSeqReplay", err)
	}

	// Wrong To tag inside the dialog is rejected.
	impostor := *bye
	impostor.Vias = []Via{mustVia(t, "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-bye3")}
	impostor.ToTag = "wrong-tag"
	if _, err := v.ValidateRequest(&impostor); !errors.Is(err, ErrDialogMismatch) {
		t.Fatalf("wrong To tag: got %v, want ErrDialogMismatch", err)
	}
}
