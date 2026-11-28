package pathverify

import (
	"crypto/x509"
	"net"
	"testing"
	"time"
)

func ncLeaf(parent *keyPair, dns []string, ips []net.IP, emails, uris []string) *x509.Certificate {
	spec := defaultLeafSpec()
	if len(dns) > 0 {
		spec.cn = dns[0]
	}
	spec.dnsSAN = dns
	spec.ipSAN = ips
	spec.emailSAN = emails
	spec.uriSAN = uris
	return issueLeaf(spec, parent)
}

// TestNameConstraints 验收点 3：多层 permitted 交集 / excluded 并集。
func TestNameConstraints(t *testing.T) {
	root := createCA("NC Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 2, true)

	upper := issueCA("Upper NC CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true, false,
		ncSpec{permittedDNS: []string{".example.com"}, excludedDNS: []string{".corp.example.com"}})
	lower := issueCA("Lower NC CA", mustGenerateKey(), upper,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false,
		ncSpec{permittedDNS: []string{".api.example.com"}})

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(upper.cert)
	inters.AddCert(lower.cert)

	okLeaf := ncLeaf(lower, []string{"svc.api.example.com"}, nil, nil, nil)
	if _, err := Verify(noHostname(okLeaf, roots, inters)); err != nil {
		t.Fatalf("leaf within both permitted subtrees should pass, got %v", err)
	}

	// 满足上层但被下层收窄拒绝（逐层交集）。
	badLeaf := ncLeaf(lower, []string{"svc.www.example.com"}, nil, nil, nil)
	if err := expectReasonOpts(noHostname(badLeaf, roots, inters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}

	// 命中上层 excluded 子树（逐层并集，下层未再排除也拒绝）。
	excludedLeaf := ncLeaf(lower, []string{"host.corp.example.com"}, nil, nil, nil)
	if err := expectReasonOpts(noHostname(excludedLeaf, roots, inters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}

	// RFC 5280 边界：".example.com" 不匹配 example.com 自身。
	apexLeaf := ncLeaf(lower, []string{"example.com"}, nil, nil, nil)
	if err := expectReasonOpts(noHostname(apexLeaf, roots, inters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}
}

// TestNameConstraintsIPAndEmail IP CIDR 与 Email 约束。
func TestNameConstraintsIPAndEmail(t *testing.T) {
	root := createCA("NC Root 2", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)

	_, ten, _ := net.ParseCIDR("10.0.0.0/8")
	_, tenOne, _ := net.ParseCIDR("10.1.0.0/16")
	ipCA := issueCA("IP NC CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false,
		ncSpec{permittedIP: []*net.IPNet{ten}, excludedIP: []*net.IPNet{tenOne}})

	roots := NewCertPool()
	roots.AddCert(root.cert)
	ipInters := NewCertPool()
	ipInters.AddCert(ipCA.cert)

	ipOK := ncLeaf(ipCA, nil, []net.IP{net.ParseIP("10.2.0.9")}, nil, nil)
	if _, err := Verify(noHostname(ipOK, roots, ipInters)); err != nil {
		t.Fatalf("IP within permitted CIDR should pass, got %v", err)
	}
	ipRejected := ncLeaf(ipCA, nil, []net.IP{net.ParseIP("192.168.1.1")}, nil, nil)
	if err := expectReasonOpts(noHostname(ipRejected, roots, ipInters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}
	ipExcluded := ncLeaf(ipCA, nil, []net.IP{net.ParseIP("10.1.5.5")}, nil, nil)
	if err := expectReasonOpts(noHostname(ipExcluded, roots, ipInters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}

	mailCA := issueCA("Mail NC CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false,
		ncSpec{permittedEmail: []string{"example.com"}, excludedEmail: []string{".denied.example.com"}})
	mailInters := NewCertPool()
	mailInters.AddCert(mailCA.cert)

	mailOK := ncLeaf(mailCA, nil, nil, []string{"user@example.com"}, nil)
	if _, err := Verify(noHostname(mailOK, roots, mailInters)); err != nil {
		t.Fatalf("permitted email should pass, got %v", err)
	}
	mailSub := ncLeaf(mailCA, nil, nil, []string{"user@sub.example.com"}, nil)
	if _, err := Verify(noHostname(mailSub, roots, mailInters)); err != nil {
		t.Fatalf("email on permitted subdomain should pass, got %v", err)
	}
	mailDenied := ncLeaf(mailCA, nil, nil, []string{"x@host.denied.example.com"}, nil)
	if err := expectReasonOpts(noHostname(mailDenied, roots, mailInters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}
	mailOutside := ncLeaf(mailCA, nil, nil, []string{"user@other.org"}, nil)
	if err := expectReasonOpts(noHostname(mailOutside, roots, mailInters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}
}
