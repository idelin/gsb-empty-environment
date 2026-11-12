package dmarc

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

type fakeResolver struct {
	txt   map[string][]string
	err   map[string]error
	calls []string
}

func (f *fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.calls = append(f.calls, name)
	if err, ok := f.err[name]; ok {
		return nil, err
	}
	return f.txt[name], nil
}

func notFoundErr(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// testOrgDomain is a stand-in for a public-suffix based function: the last
// two labels form the organizational domain.
func testOrgDomain(domain string) string {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(domain), "."), ".")
	if len(labels) <= 2 {
		return domain
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

func newResolver() *fakeResolver {
	return &fakeResolver{
		txt: map[string][]string{},
		err: map[string]error{},
	}
}

func TestSubdomainInheritsSPReject(t *testing.T) {
	res := newResolver()
	res.err["_dmarc.mail.example.com"] = notFoundErr("_dmarc.mail.example.com")
	res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none; sp=reject"}

	result, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.RecordPresent || !result.FromOrgDomain {
		t.Fatalf("expected organizational-domain fallback, got %+v", result)
	}
	if result.Domain != "example.com" {
		t.Fatalf("expected domain example.com, got %q", result.Domain)
	}
	if result.RecordPolicy != PolicyReject || result.Policy != PolicyReject {
		t.Fatalf("expected sp=reject to be applied, got record=%v policy=%v",
			result.RecordPolicy, result.Policy)
	}
	if !result.SampledIn || result.Pct != 100 {
		t.Fatalf("expected default pct=100 and sampled in, got pct=%d sampled=%v",
			result.Pct, result.SampledIn)
	}
	if len(res.calls) != 2 || res.calls[0] != "_dmarc.mail.example.com" ||
		res.calls[1] != "_dmarc.example.com" {
		t.Fatalf("unexpected query order: %v", res.calls)
	}

	// r=99 is still inside pct=100.
	result, _ = Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 99)
	if result.Policy != PolicyReject || !result.SampledIn {
		t.Fatalf("r=99 with pct=100 must stay reject, got %+v", result)
	}
}

func TestSPDefaultsToP(t *testing.T) {
	res := newResolver()
	res.err["_dmarc.mail.example.com"] = notFoundErr("_dmarc.mail.example.com")
	res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}

	result, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecordPolicy != PolicyQuarantine || result.Policy != PolicyQuarantine {
		t.Fatalf("expected sp to default to p=quarantine, got %+v", result)
	}
}

func TestSenderRecordOverridesOrgDomain(t *testing.T) {
	res := newResolver()
	res.txt["_dmarc.mail.example.com"] = []string{"v=DMARC1; p=none"}
	res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none; sp=reject"}

	result, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromOrgDomain || result.Domain != "mail.example.com" {
		t.Fatalf("sender record must take precedence, got %+v", result)
	}
	if result.RecordPolicy != PolicyNone || result.Policy != PolicyNone {
		t.Fatalf("expected sender p=none, got %+v", result)
	}
	if len(res.calls) != 1 || res.calls[0] != "_dmarc.mail.example.com" {
		t.Fatalf("organizational domain must not be queried, got %v", res.calls)
	}
}

func TestMultipleValidRecordsTreatedAsNone(t *testing.T) {
	res := newResolver()
	res.txt["_dmarc.mail.example.com"] = []string{
		"v=DMARC1; p=none",
		"v=DMARC1; p=reject",
	}
	res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none; sp=reject"}

	result, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FromOrgDomain || result.Policy != PolicyReject {
		t.Fatalf("multiple sender records must trigger org fallback, got %+v", result)
	}

	// Multiple valid records everywhere means no policy at all.
	res.txt["_dmarc.example.com"] = []string{
		"v=DMARC1; p=none",
		"v=DMARC1; p=reject",
	}
	result, err = Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecordPresent || result.Policy != PolicyNone {
		t.Fatalf("expected no record with multi-record org domain, got %+v", result)
	}
}

func TestPctBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		record     string
		r          int
		wantPolicy Policy
		wantIn     bool
	}{
		{"pct=0 never samples reject", "v=DMARC1; p=reject; pct=0", 0, PolicyQuarantine, false},
		{"pct=0 never samples reject high r", "v=DMARC1; p=reject; pct=0", 99, PolicyQuarantine, false},
		{"pct=100 r=99 samples", "v=DMARC1; p=reject; pct=100", 99, PolicyReject, true},
		{"pct=100 r=0 samples", "v=DMARC1; p=reject; pct=100", 0, PolicyReject, true},
		{"pct=50 r=49 inside", "v=DMARC1; p=reject; pct=50", 49, PolicyReject, true},
		{"pct=50 r=50 outside downgrades", "v=DMARC1; p=reject; pct=50", 50, PolicyQuarantine, false},
		{"quarantine downgrades to none", "v=DMARC1; p=quarantine; pct=0", 0, PolicyNone, false},
		{"none stays none", "v=DMARC1; p=none; pct=0", 0, PolicyNone, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := newResolver()
			res.txt["_dmarc.example.com"] = []string{tc.record}
			result, err := Lookup(context.Background(), res, testOrgDomain, "example.com", tc.r)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Policy != tc.wantPolicy || result.SampledIn != tc.wantIn {
				t.Fatalf("got policy=%v sampled=%v, want policy=%v sampled=%v",
					result.Policy, result.SampledIn, tc.wantPolicy, tc.wantIn)
			}
		})
	}
}

func TestInvalidRecords(t *testing.T) {
	invalid := []string{
		"v=DMARC1; p=reject; p=none",         // duplicate p tag
		"v=DMARC1; p=reject; pct=101",        // pct above 100
		"v=DMARC1; p=reject; pct=-1",         // negative pct
		"v=DMARC1; p=reject; pct=abc",        // non-integer pct
		"v=DMARC1; p=reject; pct=1.5",        // float pct
		"foo=bar; v=DMARC1; p=reject",        // version not first
		"v=DMARC1",                           // missing p
		"v=DMARC1; p=reject; sp=bogus",       // bad sp
		"v=DMARC1; p=reject; pct=",           // empty pct
		"v=DMARC2; p=reject",                 // wrong version
		"v=DMARC1; p=reject; pct=50; pct=60", // duplicate pct
	}
	for _, rec := range invalid {
		res := newResolver()
		res.txt["_dmarc.mail.example.com"] = []string{rec}
		res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none; sp=reject"}
		result, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
		if err != nil {
			t.Fatalf("record %q: unexpected error: %v", rec, err)
		}
		if !result.FromOrgDomain || result.Policy != PolicyReject {
			t.Fatalf("record %q should be invalid and fall back to org domain, got %+v", rec, result)
		}
	}

	// Non-DMARC TXT records are ignored; a single valid record still wins.
	res := newResolver()
	res.txt["_dmarc.example.com"] = []string{
		"google-site-verification=abc",
		"v=DMARC1; p=reject",
	}
	result, err := Lookup(context.Background(), res, testOrgDomain, "example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Policy != PolicyReject {
		t.Fatalf("non-DMARC TXT should be ignored, got %+v", result)
	}

	// Invalid records at both levels means no record.
	res = newResolver()
	res.txt["_dmarc.mail.example.com"] = []string{"v=DMARC1; pct=200"}
	res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; p=reject"}
	result, _ = Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0)
	if result.RecordPresent || result.Policy != PolicyNone {
		t.Fatalf("expected no usable record, got %+v", result)
	}
}

func TestNoRecordAnywhere(t *testing.T) {
	res := newResolver()
	res.err["_dmarc.mail.example.com"] = notFoundErr("_dmarc.mail.example.com")
	res.err["_dmarc.example.com"] = notFoundErr("_dmarc.example.com")

	result, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 5)
	if err != nil {
		t.Fatalf("missing records must not be an error, got %v", err)
	}
	if result.RecordPresent || result.Policy != PolicyNone || result.SampledIn {
		t.Fatalf("expected empty none result, got %+v", result)
	}
}

func TestSenderIsOrgDomainSkipsFallback(t *testing.T) {
	res := newResolver()
	res.err["_dmarc.example.com"] = notFoundErr("_dmarc.example.com")

	result, err := Lookup(context.Background(), res, testOrgDomain, "example.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecordPresent {
		t.Fatalf("expected no record, got %+v", result)
	}
	if len(res.calls) != 1 || res.calls[0] != "_dmarc.example.com" {
		t.Fatalf("organizational domain must not be re-queried, got %v", res.calls)
	}
}

func TestHardDNSErrorPropagates(t *testing.T) {
	res := newResolver()
	res.err["_dmarc.mail.example.com"] = errors.New("server misbehaving")

	if _, err := Lookup(context.Background(), res, testOrgDomain, "mail.example.com", 0); err == nil {
		t.Fatal("expected hard DNS error to be returned")
	}
}

func TestROutOfRange(t *testing.T) {
	res := newResolver()
	res.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	for _, r := range []int{-1, 100, 1000} {
		if _, err := Lookup(context.Background(), res, testOrgDomain, "example.com", r); err == nil {
			t.Fatalf("r=%d must be rejected", r)
		}
	}
}

func TestPolicyString(t *testing.T) {
	cases := map[Policy]string{
		PolicyNone:       "none",
		PolicyQuarantine: "quarantine",
		PolicyReject:     "reject",
	}
	for policy, want := range cases {
		if got := policy.String(); got != want {
			t.Fatalf("policy %d: got %q want %q", policy, got, want)
		}
	}
}

func TestOrgDomainMustBeParent(t *testing.T) {
	res := newResolver()
	res.err["_dmarc.mail.example.com"] = notFoundErr("_dmarc.mail.example.com")
	badOrg := func(string) string { return "evil.org" }
	_, err := Lookup(context.Background(), res, badOrg, "mail.example.com", 0)
	if err == nil {
		t.Fatal("expected error when organizational domain is not a parent")
	}
}
