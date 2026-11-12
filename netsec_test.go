package netsec

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func testMultihash(t *testing.T, data []byte) Multihash {
	t.Helper()
	sum := sha256.Sum256(data)
	mh, err := NewMultihash(MultihashSHA256, sum[:])
	if err != nil {
		t.Fatalf("NewMultihash: %v", err)
	}
	return mh
}

func v1CID(codec uint64, mh Multihash) CID {
	return CID{version: cidVersion1, codec: codec, hash: mh}
}

func mustEncode(t *testing.T, cid CID, prefix byte) string {
	t.Helper()
	s, err := cid.Encode(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustReq(host, path string) *http.Request {
	u, err := url.Parse(path)
	if err != nil {
		panic(err)
	}
	return &http.Request{Host: host, URL: u, Header: http.Header{}}
}

func newTestAuthorizer(t *testing.T, resolver Resolver) (*Authorizer, *Blocklist, Multihash) {
	t.Helper()
	mh := testMultihash(t, []byte("banned-ipfs-block"))
	bl := NewBlocklist()
	if err := bl.Add(mh.String()); err != nil {
		t.Fatal(err)
	}
	auth := New(Config{
		Blocklist:     bl,
		Resolver:      resolver,
		AllowedHosts:  []string{"gw.example.net", "ipfs.example.net"},
		SubdomainHost: "ipfs.example.net",
	})
	return auth, bl, mh
}

func TestMultihashRoundTrip(t *testing.T) {
	mh := testMultihash(t, []byte("hello world"))

	// Known external vector: the sha256("") multihash in base58btc.
	empty := testMultihash(t, nil)
	wantEmpty := "QmdfTbBqBPQ7VNxZEYEj14VmRuZBkqFbiwReogJgS1zR1n"
	if got := base58Encode(empty.raw); got != wantEmpty {
		t.Fatalf("sha256(\"\") multihash vector mismatch:\n got=%s\nwant=%s", got, wantEmpty)
	}

	parsed, err := parseMultihash(mh.String())
	if err != nil {
		t.Fatalf("parseMultihash: %v", err)
	}
	if string(parsed.raw) != string(mh.raw) {
		t.Fatal("multihash round trip changed bytes")
	}

	// Bare base58 (no 'z' prefix) must parse identically.
	bare := strings.TrimPrefix(mh.String(), "z")
	if _, err := parseMultihash(bare); err != nil {
		t.Fatalf("bare base58 multihash rejected: %v", err)
	}

	// Truncated digest must be rejected rather than treated as valid.
	bad := append(appendUvarint(appendUvarint(nil, MultihashSHA256), 32), mh.raw[2:10]...)
	if _, err := MultihashFromBytes(bad); err == nil {
		t.Fatal("expected malformed multihash to be rejected")
	}
}

func TestMultibaseCaseVariantsDecodeIdentically(t *testing.T) {
	mh := testMultihash(t, []byte("attack-payload"))
	cid := v1CID(codecRaw, mh)

	lower := mustEncode(t, cid, prefixBase32)
	upper := mustEncode(t, cid, prefixBase32Upper)
	if lower == upper {
		t.Fatal("expected distinct base32 case spellings")
	}

	decodedLower, err := ParseCID(lower)
	if err != nil {
		t.Fatalf("ParseCID lower: %v", err)
	}
	decodedUpper, err := ParseCID(upper)
	if err != nil {
		t.Fatalf("ParseCID upper: %v", err)
	}
	if string(decodedLower.Multihash().raw) != string(mh.raw) {
		t.Fatal("lower-case base32 did not normalize to the same multihash")
	}
	if string(decodedUpper.Multihash().raw) != string(mh.raw) {
		t.Fatal("upper-case base32 did not normalize to the same multihash")
	}

	// An attacker-created mixed-case string must normalize too.
	mixed := strings.ToUpper(lower[:6]) + lower[6:]
	decodedMixed, err := ParseCID(mixed)
	if err != nil {
		t.Fatalf("ParseCID mixed case: %v", err)
	}
	if string(decodedMixed.Multihash().raw) != string(mh.raw) {
		t.Fatal("mixed-case base32 evaded normalization")
	}
}

func TestAllSupportedMultibaseSpellingsShareMultihash(t *testing.T) {
	mh := testMultihash(t, []byte("same-content-many-spellings"))
	cid := v1CID(codecDagPB, mh)

	prefixes := []byte{
		prefixBase16, prefixBase16Upper,
		prefixBase32, prefixBase32Upper,
		prefixBase36, prefixBase36Upper,
		prefixBase58BTC,
		prefixBase64, prefixBase64URL,
	}
	for _, p := range prefixes {
		encoded := mustEncode(t, cid, p)
		parsed, err := ParseCID(encoded)
		if err != nil {
			t.Fatalf("ParseCID prefix %q (%s): %v", p, encoded, err)
		}
		if string(parsed.Multihash().raw) != string(mh.raw) {
			t.Fatalf("prefix %q normalized to a different multihash", p)
		}
	}
}

func TestCIDv0ToV1KeepsMultihash(t *testing.T) {
	mh := testMultihash(t, []byte("legacy dag-pb block"))

	v0Text := base58Encode(mh.raw)
	v0, err := ParseCID(v0Text)
	if err != nil {
		t.Fatalf("ParseCID v0: %v", err)
	}
	if v0.Version() != 0 || v0.Codec() != codecDagPB {
		t.Fatalf("unexpected v0 fields: version=%d codec=%d", v0.Version(), v0.Codec())
	}

	v1 := v0.V1()
	if v1.Version() != cidVersion1 || v1.Codec() != codecDagPB {
		t.Fatal("v0->v1 conversion lost the dag-pb codec or version")
	}
	if string(v1.Multihash().raw) != string(mh.raw) {
		t.Fatal("v0->v1 conversion changed the multihash")
	}

	// The v1 base32 spelling must parse back to the same multihash, proving
	// one blocklist entry covers both representations.
	roundTrip, err := ParseCID(v1.String())
	if err != nil {
		t.Fatalf("ParseCID v1 spelling: %v", err)
	}
	if string(roundTrip.Multihash().raw) != string(mh.raw) {
		t.Fatal("v1 spelling does not share the v0 multihash")
	}

	label, err := v0.DNSLabel()
	if err != nil {
		t.Fatalf("DNSLabel: %v", err)
	}
	if label[0] != prefixBase32 {
		t.Fatalf("DNS label %q is not base32 multibase", label)
	}
}

func TestBlocklistBlocksEverySpelling(t *testing.T) {
	mh := testMultihash(t, []byte("banned-content"))
	bl := NewBlocklist()
	if err := bl.Add(mh.String()); err != nil {
		t.Fatalf("blocklist add: %v", err)
	}

	v1 := v1CID(codecRaw, mh)
	spellings := []string{
		base58Encode(mh.raw), // CIDv0
		mustEncode(t, v1, prefixBase32),
		mustEncode(t, v1, prefixBase32Upper),
		mustEncode(t, v1, prefixBase36),
		mustEncode(t, v1, prefixBase16),
		mustEncode(t, v1, prefixBase58BTC),
		mustEncode(t, v1, prefixBase64),
	}
	for _, text := range spellings {
		cid, err := ParseCID(text)
		if err != nil {
			t.Fatalf("ParseCID %q: %v", text, err)
		}
		if !bl.Contains(cid.Multihash()) {
			t.Fatalf("spelling %q evaded the multihash blocklist", text)
		}
	}

	other := testMultihash(t, []byte("different-content"))
	if bl.Contains(other) {
		t.Fatal("blocklist matched a different multihash")
	}
}

func TestBlocklistRemove(t *testing.T) {
	mh := testMultihash(t, []byte("temporary-block"))
	bl := NewBlocklist()
	if err := bl.AddBytes(mh.Bytes()); err != nil {
		t.Fatal(err)
	}
	if bl.Len() != 1 || !bl.Contains(mh) {
		t.Fatal("expected multihash to be blocked after add")
	}
	if err := bl.Remove(mh.String()); err != nil {
		t.Fatal(err)
	}
	if bl.Contains(mh) || bl.Len() != 0 {
		t.Fatal("expected multihash to be unblocked after remove")
	}
}

func TestAuthorizeIPFSBlocksNormalizedSpellings(t *testing.T) {
	auth, _, mh := newTestAuthorizer(t, nil)

	blockedPaths := []string{
		"/ipfs/" + base58Encode(mh.raw), // CIDv0
		"/ipfs/" + mustEncode(t, v1CID(codecRaw, mh), prefixBase32),
		"/ipfs/" + mustEncode(t, v1CID(codecDagPB, mh), prefixBase36Upper),
		"/ipfs/" + mustEncode(t, v1CID(codecRaw, mh), prefixBase16),
		"/ipfs/" + strings.ToUpper(mustEncode(t, v1CID(codecRaw, mh), prefixBase32)),
	}
	for _, path := range blockedPaths {
		decision := auth.Authorize(mustReq("gw.example.net", path))
		if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrContentBlocked) {
			t.Fatalf("path %s not blocked: %+v", path, decision)
		}
		if decision.Status != http.StatusGone {
			t.Fatalf("blocked path status = %d, want 410", decision.Status)
		}
	}

	allowedMH := testMultihash(t, []byte("fine-block"))
	ok := auth.Authorize(mustReq("gw.example.net", "/ipfs/"+mustEncode(t, v1CID(codecRaw, allowedMH), prefixBase32)))
	if ok.Action != ActionAllow {
		t.Fatalf("allowed content denied: %+v", ok)
	}
}

func TestAuthorizeRejectsForeignHost(t *testing.T) {
	auth, _, mh := newTestAuthorizer(t, nil)

	foreign := []string{
		"evil.example.com",
		"gw.example.net.evil.example.com",
		"notgw.example.net",
		"",
	}
	for _, host := range foreign {
		decision := auth.Authorize(mustReq(host, "/ipfs/"+base58Encode(mh.raw)))
		if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrHostNotAllowed) {
			t.Fatalf("foreign host %q not rejected: %+v", host, decision)
		}
		if decision.Status != http.StatusForbidden {
			t.Fatalf("foreign host status = %d, want 403", decision.Status)
		}
	}

	// Case-insensitive host match plus port stripping still reaches the
	// blocklist check for the legitimate (blocked) content.
	decision := auth.Authorize(mustReq("GW.EXAMPLE.NET:443", "/ipfs/"+base58Encode(mh.raw)))
	if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrContentBlocked) {
		t.Fatalf("host normalization failed, got %+v", decision)
	}
}

func TestAuthorizeRejectsMalformedPath(t *testing.T) {
	auth, _, _ := newTestAuthorizer(t, nil)

	for _, path := range []string{"/", "/ipfs/", "/ipns/", "/other/thing", "/ipfs/!!!notacid"} {
		decision := auth.Authorize(mustReq("gw.example.net", path))
		if decision.Action != ActionDeny {
			t.Fatalf("path %q expected deny, got %+v", path, decision)
		}
	}
}

func testResolver(t *testing.T) Resolver {
	t.Helper()
	banned := testMultihash(t, []byte("banned-ipns-target"))
	allowedMH := testMultihash(t, []byte("allowed-ipns-target"))
	return ResolverFunc(func(name string) (string, error) {
		switch name {
		case "points-to-banned":
			return "/ipfs/" + mustEncode(t, v1CID(codecRaw, banned), prefixBase32), nil
		case "points-to-allowed":
			return "/ipfs/" + mustEncode(t, v1CID(codecRaw, allowedMH), prefixBase32), nil
		case "chain-a":
			return "/ipns/chain-b", nil
		case "chain-b":
			return "/ipfs/" + mustEncode(t, v1CID(codecDagPB, banned), prefixBase36), nil
		case "loop":
			return "/ipns/loop", nil
		case "broken":
			return "", errors.New("name system unavailable")
		case "garbage":
			return "not-a-gateway-path", nil
		default:
			return "", errors.New("unknown name")
		}
	})
}

func TestAuthorizeIPNSResolution(t *testing.T) {
	resolver := testResolver(t)
	banned := testMultihash(t, []byte("banned-ipns-target"))
	allowedMH := testMultihash(t, []byte("allowed-ipns-target"))

	bl := NewBlocklist()
	if err := bl.Add(banned.String()); err != nil {
		t.Fatal(err)
	}
	auth := New(Config{
		Blocklist:     bl,
		Resolver:      resolver,
		AllowedHosts:  []string{"gw.example.net", "ipfs.example.net"},
		SubdomainHost: "ipfs.example.net",
	})

	// Direct resolution to blocked content: the resolved CID is checked.
	decision := auth.Authorize(mustReq("gw.example.net", "/ipns/points-to-banned/file.html"))
	if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrContentBlocked) {
		t.Fatalf("resolved banned target not blocked: %+v", decision)
	}

	// A chain of /ipns/ names must still terminate on the banned multihash.
	decision = auth.Authorize(mustReq("gw.example.net", "/ipns/chain-a"))
	if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrContentBlocked) {
		t.Fatalf("chained banned target not blocked: %+v", decision)
	}

	// Resolution reaching allowed content passes.
	decision = auth.Authorize(mustReq("gw.example.net", "/ipns/points-to-allowed"))
	if decision.Action != ActionAllow {
		t.Fatalf("allowed IPNS target denied: %+v", decision)
	}
	if string(decision.CID.Multihash().raw) != string(allowedMH.raw) {
		t.Fatal("allowed decision did not carry the resolved CID")
	}

	// Fail closed: resolver error, cyclic chain and malformed target all deny.
	for _, name := range []string{"broken", "loop", "garbage", "unknown"} {
		decision = auth.Authorize(mustReq("gw.example.net", "/ipns/"+name))
		if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrResolveFailed) {
			t.Fatalf("name %q should fail closed, got %+v", name, decision)
		}
	}
}

func TestAuthorizeIPNSWithoutResolverFailsClosed(t *testing.T) {
	auth, _, _ := newTestAuthorizer(t, nil)
	decision := auth.Authorize(mustReq("gw.example.net", "/ipns/k51q.../x"))
	if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrResolveFailed) {
		t.Fatalf("IPNS without resolver should deny, got %+v", decision)
	}
}

func TestContentPolicyRedirectsExecutableToSubdomain(t *testing.T) {
	auth, _, _ := newTestAuthorizer(t, nil)
	mh := testMultihash(t, []byte("active-but-allowed-block"))
	cid := v1CID(codecRaw, mh)
	path := "/ipfs/" + mustEncode(t, cid, prefixBase32) + "/index.html"
	r := mustReq("gw.example.net", path+"?a=1")

	allowed := auth.Authorize(r)
	if allowed.Action != ActionAllow {
		t.Fatalf("setup: expected allow, got %+v", allowed)
	}

	decision := auth.ContentPolicy(r, "text/html; charset=utf-8", allowed)
	if decision.Action != ActionRedirect || decision.Status != http.StatusFound {
		t.Fatalf("executable type not redirected: %+v", decision)
	}

	label, err := cid.DNSLabel()
	if err != nil {
		t.Fatal(err)
	}
	wantHost := label + ".ipfs.example.net"
	if !strings.Contains(decision.Location, "//"+wantHost) {
		t.Fatalf("redirect location %q does not target subdomain %q", decision.Location, wantHost)
	}
	if !strings.HasSuffix(decision.Location, path+"?a=1") {
		t.Fatalf("redirect %q lost path/query", decision.Location)
	}

	// SVG with parameters and other executable types are isolated too.
	for _, ct := range []string{"image/svg+xml", "application/pdf", "application/xhtml+xml"} {
		if d := auth.ContentPolicy(r, ct, allowed); d.Action != ActionRedirect {
			t.Fatalf("content type %q not redirected: %+v", ct, d)
		}
	}

	// Non-executable content is served in place on the path gateway.
	if d := auth.ContentPolicy(r, "image/png", allowed); d.Action != ActionAllow {
		t.Fatalf("image/png should be allowed in place: %+v", d)
	}
}

func TestContentPolicyRejectsForeignHost(t *testing.T) {
	auth, _, mh := newTestAuthorizer(t, nil)
	cid := v1CID(codecRaw, mh)
	path := "/ipfs/" + mustEncode(t, cid, prefixBase32) + "/x.html"
	r := mustReq("evil.example.com", path)

	allowed := Decision{Action: ActionAllow, Path: path, CID: cid}
	decision := auth.ContentPolicy(r, "text/html", allowed)
	if decision.Action != ActionDeny || !errors.Is(decision.Reason, ErrHostNotAllowed) {
		t.Fatalf("foreign host not rejected by content policy: %+v", decision)
	}
}

func TestContentPolicyWithoutSubdomainHostDeniesExecutable(t *testing.T) {
	mh := testMultihash(t, []byte("active-block"))
	auth := New(Config{
		Blocklist:    NewBlocklist(),
		AllowedHosts: []string{"gw.example.net"},
	})
	cid := v1CID(codecRaw, mh)
	path := "/ipfs/" + mustEncode(t, cid, prefixBase32) + "/x.html"
	r := mustReq("gw.example.net", path)

	allowed := Decision{Action: ActionAllow, Path: path, CID: cid}
	decision := auth.ContentPolicy(r, "text/html", allowed)
	if decision.Action != ActionDeny {
		t.Fatalf("expected deny without an isolation origin, got %+v", decision)
	}
}

func TestIPNSExecutableRedirectUsesResolvedCID(t *testing.T) {
	resolver := testResolver(t)
	allowedMH := testMultihash(t, []byte("allowed-ipns-target"))
	bl := NewBlocklist()
	auth := New(Config{
		Blocklist:     bl,
		Resolver:      resolver,
		AllowedHosts:  []string{"gw.example.net", "ipfs.example.net"},
		SubdomainHost: "ipfs.example.net",
	})

	r := mustReq("gw.example.net", "/ipns/points-to-allowed/page.html")
	allowed := auth.Authorize(r)
	if allowed.Action != ActionAllow {
		t.Fatalf("expected allow, got %+v", allowed)
	}

	decision := auth.ContentPolicy(r, "text/html", allowed)
	if decision.Action != ActionRedirect {
		t.Fatalf("expected redirect, got %+v", decision)
	}
	label, err := v1CID(codecRaw, allowedMH).DNSLabel()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decision.Location, "//"+label+".ipfs.example.net") {
		t.Fatalf("redirect %q not keyed to resolved CID subdomain", decision.Location)
	}
}

func TestAuthorizePathSkipsHostCheck(t *testing.T) {
	auth, _, mh := newTestAuthorizer(t, nil)
	// No request/host available; the blocklist is still enforced.
	d := auth.AuthorizePath("/ipfs/" + base58Encode(mh.raw))
	if d.Action != ActionDeny || !errors.Is(d.Reason, ErrContentBlocked) {
		t.Fatalf("AuthorizePath did not enforce blocklist: %+v", d)
	}
	allowedMH := testMultihash(t, []byte("ok-block"))
	d = auth.AuthorizePath("/ipfs/" + mustEncode(t, v1CID(codecRaw, allowedMH), prefixBase32))
	if d.Action != ActionAllow {
		t.Fatalf("AuthorizePath denied allowed content: %+v", d)
	}
}

func TestParseCIDRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "!!!", "b@@@@", "z", "fzz"} {
		if _, err := ParseCID(s); err == nil {
			t.Fatalf("ParseCID(%q) expected error", s)
		}
	}

	// A multibase payload that is not a version-1 CID must be rejected.
	mh := testMultihash(t, []byte("x"))
	notV1, err := encodeMultibase(prefixBase32, mh.raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCID(notV1); err == nil {
		t.Fatal("expected non-CIDv1 multibase payload to be rejected")
	}

	// An unsupported prefix and an invalid base58 payload are errors rather
	// than a silent allow.
	if _, err := decodeMultibase("Xabc"); err == nil {
		t.Fatal("expected unsupported multibase error")
	}
	if _, err := decodeMultibase("z0O!"); err == nil {
		t.Fatal("expected invalid multibase payload error")
	}
}

func TestMultihashHexAndCode(t *testing.T) {
	mh := testMultihash(t, []byte("hex-case"))
	hex := mh.Hex()
	if hex[0] != prefixBase16 {
		t.Fatalf("hex rendering %q missing base16 prefix", hex)
	}
	parsed, err := decodeMultibase(hex)
	if err != nil {
		t.Fatalf("hex multibase decode: %v", err)
	}
	if string(parsed) != string(mh.raw) {
		t.Fatal("hex rendering did not round-trip")
	}
	code, err := mh.Code()
	if err != nil || code != MultihashSHA256 {
		t.Fatalf("multihash code = %d, err = %v", code, err)
	}
}

func TestBlocklistRejectsInvalidEntry(t *testing.T) {
	bl := NewBlocklist()
	if err := bl.Add("!!!not-base58!!!"); err == nil {
		t.Fatal("expected invalid blocklist entry to error")
	}
	if err := bl.AddBytes([]byte{0x12}); err == nil {
		t.Fatal("expected truncated multihash to error")
	}
}

func TestSubdomainRedirectScheme(t *testing.T) {
	auth, _, _ := newTestAuthorizer(t, nil)
	mh := testMultihash(t, []byte("scheme-block"))
	cid := v1CID(codecRaw, mh)
	path := "/ipfs/" + mustEncode(t, cid, prefixBase32) + "/a.html"

	// Plain HTTP request (no TLS, no forwarding header) yields an http target.
	r := mustReq("gw.example.net", path)
	allowed := Decision{Action: ActionAllow, Path: path, CID: cid}
	d := auth.ContentPolicy(r, "text/html", allowed)
	if !strings.HasPrefix(d.Location, "http://") {
		t.Fatalf("expected http scheme, got %q", d.Location)
	}

	// Terminating TLS behind a proxy is signaled via X-Forwarded-Proto.
	r2 := mustReq("gw.example.net", path)
	r2.Header.Set("X-Forwarded-Proto", "https")
	d = auth.ContentPolicy(r2, "text/html", allowed)
	if !strings.HasPrefix(d.Location, "https://") {
		t.Fatalf("expected https scheme behind proxy, got %q", d.Location)
	}
}

func TestHostStripsPortAndTrailingDot(t *testing.T) {
	auth, _, _ := newTestAuthorizer(t, nil)
	mh := testMultihash(t, []byte("host-normalization"))
	path := "/ipfs/" + mustEncode(t, v1CID(codecRaw, mh), prefixBase32)

	for _, host := range []string{"gw.example.net:8080", "gw.example.net."} {
		d := auth.Authorize(mustReq(host, path))
		if d.Action != ActionAllow {
			t.Fatalf("host %q should be allowed, got %+v", host, d)
		}
	}
}
