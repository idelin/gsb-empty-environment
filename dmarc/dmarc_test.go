package dmarc

import (
	"context"
	"errors"
	"net"
	"testing"
)

type fakeDNS struct {
	txt  map[string][]string
	errs map[string]error
}

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	return f.txt[name], nil
}

func newDNS() *fakeDNS {
	return &fakeDNS{txt: make(map[string][]string), errs: make(map[string]error)}
}

func evaluate(t *testing.T, dns *fakeDNS, from string, spfStatus, spfDomain string, dkim ...DKIMResult) Result {
	t.Helper()
	result, err := Evaluate(context.Background(), Options{
		FromDomain: from,
		SPF:        SPFResult{Status: spfStatus, Domain: spfDomain},
		DKIM:       dkim,
		Resolver:   dns,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return result
}

func TestParseRecord(t *testing.T) {
	record, err := ParseRecord("v=DMARC1; p=quarantine; sp=reject; adkim=s; aspf=r; pct=25; rua=mailto:a@example.com, mailto:b@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if record.Policy != PolicyQuarantine || record.SubdomainPolicy != PolicyReject {
		t.Fatalf("policies = %q/%q", record.Policy, record.SubdomainPolicy)
	}
	if record.ADKIM != AlignmentStrict || record.ASPF != AlignmentRelaxed {
		t.Fatalf("alignment = %q/%q", record.ADKIM, record.ASPF)
	}
	if record.Pct != 25 || len(record.RUA) != 2 {
		t.Fatalf("pct/rua = %d/%v", record.Pct, record.RUA)
	}
}

func TestAlignmentStrictAndRelaxed(t *testing.T) {
	if !IsAligned("mail.example.com", "example.com", AlignmentRelaxed) {
		t.Fatal("relaxed alignment should accept sibling subdomains")
	}
	if IsAligned("mail.example.com", "example.com", AlignmentStrict) {
		t.Fatal("strict alignment must be exact")
	}
	if !IsAligned("example.com", "example.com", AlignmentStrict) {
		t.Fatal("strict alignment should accept exact domains")
	}
	if IsAligned("mail.example.com", "example.net", AlignmentRelaxed) {
		t.Fatal("different organizational domains must not align")
	}
}

func TestSPFAndDKIMAlignmentCombination(t *testing.T) {
	dns := newDNS()
	dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; adkim=r; aspf=r"}

	result := evaluate(t, dns, "example.com", "pass", "mail.example.com")
	if result.Status != Pass || result.Disposition != DispositionNone || !result.SPFAligned {
		t.Fatalf("result = %+v, want aligned SPF pass", result)
	}

	result = evaluate(t, dns, "example.com", "fail", "other.example", DKIMResult{Status: "pass", Domain: "mail.example.com"})
	if result.Status != Pass || !result.DKIMAligned {
		t.Fatalf("result = %+v, want aligned DKIM pass", result)
	}

	result = evaluate(t, dns, "example.com", "pass", "mail.example.com", DKIMResult{Status: "pass", Domain: "evil.example"})
	if result.Status != Pass || !result.SPFAligned {
		t.Fatalf("result = %+v, want SPF alone to pass", result)
	}
}

func TestStrictAlignmentFailures(t *testing.T) {
	dns := newDNS()
	dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; adkim=s; aspf=s"}

	result := evaluate(t, dns, "example.com", "pass", "mail.example.com", DKIMResult{Status: "pass", Domain: "mail.example.com"})
	if result.Status != Fail || result.Disposition != DispositionReject {
		t.Fatalf("result = %+v, want strict-alignment reject", result)
	}
	if result.SPFAligned || result.DKIMAligned {
		t.Fatalf("result = %+v, alignment should be false", result)
	}
}

func TestSubdomainPolicyOverride(t *testing.T) {
	dns := newDNS()
	dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none; sp=reject"}

	result := evaluate(t, dns, "sub.example.com", "fail", "other.example")
	if result.Status != Fail || result.AppliedPolicy != PolicyReject || result.Disposition != DispositionReject {
		t.Fatalf("result = %+v, want sp=reject", result)
	}

	result = evaluate(t, dns, "example.com", "fail", "other.example")
	if result.Status != Fail || result.AppliedPolicy != PolicyNone || result.Disposition != DispositionNone {
		t.Fatalf("result = %+v, want p=none at organizational domain", result)
	}
}

func TestExactSubdomainRecordUsesPNotSp(t *testing.T) {
	dns := newDNS()
	dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none; sp=reject"}
	dns.txt["_dmarc.sub.example.com"] = []string{"v=DMARC1; p=quarantine"}
	result := evaluate(t, dns, "sub.example.com", "fail", "other.example")
	if result.RecordDomain != "sub.example.com" {
		t.Fatalf("record domain = %q", result.RecordDomain)
	}
	if result.AppliedPolicy != PolicyQuarantine || result.Disposition != DispositionQuarantine {
		t.Fatalf("result = %+v, want exact p=quarantine", result)
	}
}

func TestPctZeroSuppressesDisposition(t *testing.T) {
	dns := newDNS()
	dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; pct=0"}
	result := evaluate(t, dns, "example.com", "fail", "other.example")
	if result.Status != Fail {
		t.Fatalf("status = %s, want fail", result.Status)
	}
	if result.Disposition != DispositionNone || result.Sampled {
		t.Fatalf("result = %+v, want suppressed by pct=0", result)
	}
}

func TestMissingRecordIsNone(t *testing.T) {
	result := evaluate(t, newDNS(), "example.com", "fail", "other.example")
	if result.Status != None || result.Disposition != DispositionNone || result.Record != nil {
		t.Fatalf("result = %+v, want none", result)
	}
}

func TestTemporaryDNSError(t *testing.T) {
	dns := newDNS()
	dns.errs["_dmarc.example.com"] = &net.DNSError{Err: "timeout", IsTimeout: true}
	result := evaluate(t, dns, "example.com", "pass", "example.com")
	if result.Status != TempError {
		t.Fatalf("result = %+v, want temperror", result)
	}
}

func TestFromDomain(t *testing.T) {
	domain, err := FromDomain([]byte("From: Alice <alice@Sub.Example.COM>\r\nTo: b@example.net\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if domain != "sub.example.com" {
		t.Fatalf("domain = %q", domain)
	}
	if _, err := FromDomain([]byte("From: a@example.com\r\nFrom: b@example.com\r\n\r\n")); err == nil {
		t.Fatal("multiple From headers should fail")
	}
	if _, err := FromDomain([]byte("From: a@example.com, b@example.com\r\n\r\n")); err == nil {
		t.Fatal("multiple From mailboxes should fail")
	}
}

func TestUnrelatedErrorPropagatesAsPermError(t *testing.T) {
	dns := newDNS()
	dns.errs["_dmarc.example.com"] = errors.New("malformed DNS response")
	result := evaluate(t, dns, "example.com", "pass", "example.com")
	if result.Status != PermError {
		t.Fatalf("result = %+v, want permerror", result)
	}
}
