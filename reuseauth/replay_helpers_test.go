package reuseauth

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

type fixture struct {
	t       *testing.T
	trust   *TrustStore
	auth    *Authority
	origins map[string]Origin
	signers map[string]HopSigner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	trust := NewTrustStore()
	names := []string{"search", "link", "cdn"}
	origins := make(map[string]Origin, len(names)+3)
	signers := make(map[string]HopSigner, len(names))
	for i, name := range names {
		origin := mustParseOrigin(t, "https://"+name+".example")
		seed := make([]byte, ed25519.SeedSize)
		for j := range seed {
			seed[j] = byte(i*31 + j + 1)
		}
		privateKey := ed25519.NewKeyFromSeed(seed)
		signerID := SignerID(name)
		if err := trust.Add(signerID, privateKey.Public().(ed25519.PublicKey), origin); err != nil {
			t.Fatal(err)
		}
		origins[name] = origin
		signers[name] = HopSigner{ID: signerID, Key: privateKey}
	}
	for _, name := range []string{"app", "partner", "evil"} {
		origins[name] = mustParseOrigin(t, "https://"+name+".example")
	}
	auth, err := NewAuthorityFromSeed("authority", trust, bytes.Repeat([]byte{0x5a}, ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, trust: trust, auth: auth, origins: origins, signers: signers}
}

func mustParseOrigin(t *testing.T, raw string) Origin {
	t.Helper()
	origin, err := ParseOrigin(raw)
	if err != nil {
		t.Fatal(err)
	}
	return origin
}

func manifestForData(data []byte, chunk int) Manifest {
	if chunk <= 0 {
		chunk = 2
	}
	segments := make([]Segment, 0, (len(data)+chunk-1)/chunk)
	for start, index := 0, 0; start < len(data); start, index = start+chunk, index+1 {
		end := start + chunk
		if end > len(data) {
			end = len(data)
		}
		r := MustRange(int64(start), int64(end))
		rd, err := MakeRangeDigest(data, r)
		if err != nil {
			panic(err)
		}
		segments = append(segments, Segment{Index: index, Range: r, Digest: rd.Digest})
	}
	return Manifest{ObjectLength: int64(len(data)), Segments: segments}
}

type evidenceConfig struct {
	data        []byte
	chunk       int
	public      bool
	audience    Origin
	knownLength bool
}

type journey struct {
	config   evidenceConfig
	Chain    SignedRedirectChain
	Response SignedResponse
	Manifest Manifest
}

func (f *fixture) evidence(cfg evidenceConfig) journey {
	f.t.Helper()
	manifest := manifestForData(cfg.data, cfg.chunk)
	length := int64(len(cfg.data))
	if !cfg.knownLength {
		length = -1
		manifest.ObjectLength = -1
	}
	if err := manifest.VerifyContent(cfg.data); err != nil {
		f.t.Fatalf("test fixture content does not match manifest: %v", err)
	}

	search := f.origins["search"]
	link := f.origins["link"]
	cdn := f.origins["cdn"]
	hops := []Hop{
		{
			RequestURL: "https://search.example/start",
			Status:     302,
			Location:   "https://link.example/next",
			Responder:  search,
			NotBefore:  1,
			ExpiresAt:  10000,
		},
		{
			RequestURL: "https://link.example/next",
			Status:     302,
			Location:   "https://cdn.example/object",
			Responder:  link,
			NotBefore:  1,
			ExpiresAt:  10000,
		},
		{
			RequestURL: "https://cdn.example/object",
			Status:     206,
			Responder:  cdn,
			NotBefore:  1,
			ExpiresAt:  10000,
		},
	}
	chain, err := SignRedirectChain(hops, []HopSigner{f.signers["search"], f.signers["link"], f.signers["cdn"]})
	if err != nil {
		f.t.Fatal(err)
	}
	response := FinalResponse{
		FinalURL:       "https://cdn.example/object",
		Status:         206,
		Responder:      cdn,
		Recipient:      cfg.audience,
		HeaderDigest:   hashString("test-header-digest", "vary-origin"),
		ManifestDigest: manifest.Digest(),
		Length:         length,
		Public:         cfg.public,
		NotBefore:      1,
		ExpiresAt:      10000,
	}
	signedResponse, err := SignResponse(f.signers["cdn"], response)
	if err != nil {
		f.t.Fatal(err)
	}
	return journey{config: cfg, Chain: chain, Response: signedResponse, Manifest: manifest}
}

func (j journey) request(requestID RequestID, source Origin, offered []RangeDigest) ReuseRequest {
	if offered == nil {
		offered = j.Manifest.RangeDigests()
	}
	return ReuseRequest{
		RequestID: requestID,
		Now:       10,
		Context: RequestContext{
			URL:    j.Chain.Hops[0].RequestURL,
			Source: source,
		},
		Offered:  offered,
		Chain:    j.Chain,
		Response: j.Response,
		Manifest: j.Manifest,
	}
}

func newTestNav(t *testing.T, id NavID, origin Origin, mode CredentialMode) *Navigation {
	t.Helper()
	site := "https://" + origin.Host
	nav, err := NewNavigation(id, origin, site, mode)
	if err != nil {
		t.Fatal(err)
	}
	return nav
}

func allowCDNAnything(t *testing.T, nav *Navigation, cdn Origin, visibility Visibility) {
	t.Helper()
	if err := nav.Allow(PolicyDecision{
		At:         2,
		Source:     nav.Recipient,
		Target:     cdn,
		Action:     ActionAny,
		Visibility: visibility,
	}); err != nil {
		t.Fatal(err)
	}
}

func changeRangeStrings(cs ChangeSet) []string {
	out := make([]string, 0, len(cs.Added))
	for _, rd := range cs.Added {
		out = append(out, rd.Range.String())
	}
	return out
}
