package netsec

import (
	"net/mail"
	"strings"
	"testing"
)

const (
	hdrMX1    = `mx1.example.org; spf=pass smtp.mailfrom=example.net; dkim=pass header.i=@example.net`
	hdrMX2    = `mx2.example.org; dkim=pass header.i=@example.net; spf=pass smtp.mailfrom=example.net`
	hdrForged = `evil.example.com; spf=pass smtp.mailfrom=example.net; dkim=pass header.i=@example.net; dmarc=pass header.from=example.net`
)

func buildRawMessage(arHeaders ...string) string {
	var b strings.Builder
	for _, h := range arHeaders {
		b.WriteString("Authentication-Results: " + h + "\r\n")
	}
	b.WriteString("From: alice@example.net\r\nTo: bob@example.org\r\nSubject: hi\r\n\r\nbody\r\n")
	return b.String()
}

func parseMessage(t *testing.T, arHeaders ...string) mail.Header {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(buildRawMessage(arHeaders...)))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	return msg.Header
}

func reasons(ev []Evidence) []string {
	out := make([]string, len(ev))
	for i, e := range ev {
		out[i] = e.Reason
	}
	return out
}

func TestForgedHeaderPreservedAsUntrustedEvidence(t *testing.T) {
	chain := NewChain("conn-1")
	chain.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	chain.Attest("mx2.example.org", hdrMX2, "dkim", "spf")

	header := parseMessage(t, hdrMX2, hdrMX1, hdrForged)
	res := Sanitize(header, chain, "conn-1")

	if len(res.Trusted) != 2 {
		t.Fatalf("trusted = %d, want 2", len(res.Trusted))
	}
	if res.Trusted[0].NodeID != "mx2.example.org" || res.Trusted[1].NodeID != "mx1.example.org" {
		t.Fatalf("trusted order = %q, %q; want innermost-first mx2, mx1",
			res.Trusted[0].NodeID, res.Trusted[1].NodeID)
	}
	for _, tr := range res.Trusted {
		if tr.Level != LevelVerified {
			t.Fatalf("trusted level = %v, want LevelVerified", tr.Level)
		}
	}
	if len(res.Untrusted) != 1 {
		t.Fatalf("untrusted = %d, want 1", len(res.Untrusted))
	}
	if res.Untrusted[0].Raw != hdrForged {
		t.Fatalf("forged header not preserved verbatim:\ngot  %q\nwant %q", res.Untrusted[0].Raw, hdrForged)
	}
	if res.Untrusted[0].Reason != ReasonNoProof {
		t.Fatalf("reason = %q, want %q", res.Untrusted[0].Reason, ReasonNoProof)
	}
	if got, ok := res.MethodResult("spf"); !ok || got != "pass" {
		t.Fatalf("spf = %q, %v; want pass, true", got, ok)
	}
	if _, ok := res.MethodResult("dmarc"); ok {
		t.Fatal("dmarc from forged header must not be trusted")
	}
}

func TestForwardingNodeRenamedBreaksChain(t *testing.T) {
	const hdrFwdRenamed = `fwd-alias.example.org; spf=pass smtp.mailfrom=example.net`
	chain := NewChain("conn-2")
	chain.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	chain.Attest("fwd.example.org", hdrFwdRenamed, "spf") // node renamed itself in the header
	chain.Attest("mx2.example.org", hdrMX2, "dkim", "spf")

	header := parseMessage(t, hdrMX2, hdrFwdRenamed, hdrMX1)
	res := Sanitize(header, chain, "conn-2")

	if len(res.Trusted) != 1 || res.Trusted[0].NodeID != "mx2.example.org" {
		t.Fatalf("trusted = %+v, want only mx2", res.Trusted)
	}
	want := []string{ReasonNodeMismatch, ReasonBrokenChain}
	if got := reasons(res.Untrusted); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("untrusted reasons = %v, want %v", got, want)
	}
	if res.Untrusted[0].Raw != hdrFwdRenamed || res.Untrusted[1].Raw != hdrMX1 {
		t.Fatal("renamed node and everything below it must be kept as evidence")
	}
}

func TestProofNotReusableAcrossConnections(t *testing.T) {
	chainA := NewChain("conn-A")
	chainA.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	header := parseMessage(t, hdrMX1)

	resA := Sanitize(header, chainA, "conn-A")
	if len(resA.Trusted) != 1 {
		t.Fatalf("same connection: trusted = %d, want 1", len(resA.Trusted))
	}

	// Same message, different receiving connection: replayed proofs must fail.
	resB := Sanitize(header, chainA, "conn-B")
	if len(resB.Trusted) != 0 {
		t.Fatalf("replayed proof elevated trust on another connection: %+v", resB.Trusted)
	}
	if len(resB.Untrusted) != 1 || resB.Untrusted[0].Reason != ReasonProofConnection {
		t.Fatalf("untrusted = %+v, want one %q", resB.Untrusted, ReasonProofConnection)
	}

	// Re-attesting on the new connection works.
	chainB := NewChain("conn-B")
	chainB.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	if got := Sanitize(header, chainB, "conn-B"); len(got.Trusted) != 1 {
		t.Fatalf("fresh per-connection proof: trusted = %d, want 1", len(got.Trusted))
	}
}

func TestDuplicateNodeRefusesElevation(t *testing.T) {
	const hdrLoop = `mx1.example.org; spf=fail smtp.mailfrom=evil.example.com`
	chain := NewChain("conn-4")
	chain.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	chain.Attest("mx1.example.org", hdrLoop, "spf") // routing loop: same node twice

	header := parseMessage(t, hdrLoop, hdrMX1)
	res := Sanitize(header, chain, "conn-4")

	if len(res.Trusted) != 1 {
		t.Fatalf("trusted = %d, want 1 (duplicate must not elevate)", len(res.Trusted))
	}
	if len(res.Untrusted) != 1 || res.Untrusted[0].Reason != ReasonDuplicateNode {
		t.Fatalf("untrusted = %+v, want one %q", res.Untrusted, ReasonDuplicateNode)
	}
}

func TestMethodBinding(t *testing.T) {
	chain := NewChain("conn-5")
	chain.Attest("mx1.example.org", hdrMX1, "spf") // header also claims dkim

	header := parseMessage(t, hdrMX1)
	res := Sanitize(header, chain, "conn-5")

	if len(res.Trusted) != 0 {
		t.Fatalf("unattested method elevated trust: %+v", res.Trusted)
	}
	if len(res.Untrusted) != 1 || res.Untrusted[0].Reason != ReasonMethodMismatch {
		t.Fatalf("untrusted = %+v, want one %q", res.Untrusted, ReasonMethodMismatch)
	}
}

func TestStrippedHeaderBreaksChain(t *testing.T) {
	chain := NewChain("conn-6")
	chain.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	chain.Attest("mx2.example.org", hdrMX2, "dkim", "spf")

	header := parseMessage(t, hdrMX1) // inner hop's header was stripped
	res := Sanitize(header, chain, "conn-6")

	if len(res.Trusted) != 0 {
		t.Fatalf("broken chain elevated trust: %+v", res.Trusted)
	}
	if len(res.Untrusted) != 1 || res.Untrusted[0].Reason != ReasonHeaderMismatch {
		t.Fatalf("untrusted = %+v, want one %q", res.Untrusted, ReasonHeaderMismatch)
	}
}

func TestTamperedProofDetected(t *testing.T) {
	chain := NewChain("conn-7")
	chain.Attest("mx1.example.org", hdrMX1, "spf", "dkim")
	chain.Attest("mx2.example.org", hdrMX2, "dkim", "spf")
	if err := chain.Verify(); err != nil {
		t.Fatalf("fresh chain must verify: %v", err)
	}

	chain.hops[0].Methods = append(chain.hops[0].Methods, "dmarc")
	if err := chain.Verify(); err == nil {
		t.Fatal("tampered proof chain must not verify")
	}

	header := parseMessage(t, hdrMX2, hdrMX1)
	res := Sanitize(header, chain, "conn-7")
	if len(res.Trusted) != 0 {
		t.Fatalf("tampered chain elevated trust: %+v", res.Trusted)
	}
	if res.Untrusted[0].Reason != ReasonProofTampered {
		t.Fatalf("reason = %q, want %q", res.Untrusted[0].Reason, ReasonProofTampered)
	}
}

func TestParseAuthResultsRealWorld(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		authservID string
		methods    []string
		none       bool
	}{
		{
			name:       "gmail style",
			raw:        `mx.google.com; dkim=pass header.i=@example.com header.s=selector header.b=abc123; spf=pass (google.com: domain of x@example.com designates 1.2.3.4 as permitted sender) smtp.mailfrom=example.com; dmarc=pass (p=NONE sp=QUARANTINE dis=NONE) header.from=example.com`,
			authservID: "mx.google.com",
			methods:    []string{"dkim", "spf", "dmarc"},
		},
		{
			name:       "with reason and version",
			raw:        `mx1.example.org 1; spf=pass reason="client ip 203.0.113.7" smtp.mailfrom=example.net`,
			authservID: "mx1.example.org",
			methods:    []string{"spf"},
		},
		{
			name:       "none",
			raw:        `mx1.example.org; none`,
			authservID: "mx1.example.org",
			none:       true,
		},
		{
			name:       "multiple dkim signatures",
			raw:        `mx2.example.org; dkim=pass header.i=@example.net header.s=s1; dkim=fail header.i=@evil.example header.s=s2`,
			authservID: "mx2.example.org",
			methods:    []string{"dkim", "dkim"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ar, err := ParseAuthResults(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ar.AuthservID != tc.authservID {
				t.Fatalf("authserv-id = %q, want %q", ar.AuthservID, tc.authservID)
			}
			if ar.None != tc.none {
				t.Fatalf("none = %v, want %v", ar.None, tc.none)
			}
			if len(ar.Methods) != len(tc.methods) {
				t.Fatalf("methods = %v, want %v", ar.Methods, tc.methods)
			}
			for i, m := range tc.methods {
				if ar.Methods[i].Method != m {
					t.Fatalf("method[%d] = %q, want %q", i, ar.Methods[i].Method, m)
				}
			}
			if ar.Raw != tc.raw {
				t.Fatal("raw header not preserved")
			}
		})
	}
}

func TestParseAuthResultsReason(t *testing.T) {
	ar, err := ParseAuthResults(`mx1.example.org; spf=softfail reason="mail from 198.51.100.9 not designated" smtp.mailfrom=example.net`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ar.Methods[0].Result != "softfail" {
		t.Fatalf("result = %q, want softfail", ar.Methods[0].Result)
	}
	if ar.Methods[0].Reason != "mail from 198.51.100.9 not designated" {
		t.Fatalf("reason = %q", ar.Methods[0].Reason)
	}
	if ar.Methods[0].Props["smtp.mailfrom"] != "example.net" {
		t.Fatalf("props = %v", ar.Methods[0].Props)
	}
}
