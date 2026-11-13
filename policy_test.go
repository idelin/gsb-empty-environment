package netsec

import (
	"strings"
	"testing"
)

const testNamespace = "tenant-a"

func testOptions() Options {
	return Options{
		AllowedCIDRs: []string{
			"203.0.113.0/24",
			"2001:db8:1::/48",
		},
		Reserved: ReservedSet{
			CIDRs: []string{
				"10.96.0.0/12",
				"10.244.0.0/16",
				"fd00:1::/112",
			},
			IPs: []string{
				"10.0.0.5",
				"10.0.0.6",
				"10.96.0.10",
				"fd00:abcd::53",
			},
		},
	}
}

func mustPolicy(t *testing.T, opts Options) *Policy {
	t.Helper()
	policy, err := NewPolicy(opts)
	if err != nil {
		t.Fatalf("NewPolicy returned error: %v", err)
	}
	return policy
}

func TestAcceptsAllowedExternalIPs(t *testing.T) {
	policy := mustPolicy(t, testOptions())
	svc := &Service{
		Namespace: testNamespace,
		Spec: ServiceSpec{
			Type:        ServiceTypeLoadBalancer,
			ExternalIPs: []string{"203.0.113.7", "2001:db8:1::abcd"},
		},
		Status: ServiceStatus{
			LoadBalancer: LoadBalancerStatus{
				Ingress: []LoadBalancerIngress{{IP: "203.0.113.8"}},
			},
		},
	}
	if err := policy.Validate(svc, ""); err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
}

func TestRejectsNodeAddress(t *testing.T) {
	policy := mustPolicy(t, testOptions())
	err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"10.0.0.5"}},
	}, "")
	if err == nil {
		t.Fatal("expected node IP 10.0.0.5 to be rejected")
	}
	if !strings.Contains(err.Error(), "reserved cluster address") {
		t.Fatalf("expected reserved-address reason, got %v", err)
	}
}

func TestRejectsDNSAddress(t *testing.T) {
	policy := mustPolicy(t, testOptions())

	err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"10.96.0.10"}},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "reserved cluster address") {
		t.Fatalf("expected IPv4 DNS IP rejection, got %v", err)
	}

	err = policy.Validate(&Service{
		Namespace: testNamespace,
		Status: ServiceStatus{LoadBalancer: LoadBalancerStatus{
			Ingress: []LoadBalancerIngress{{IP: "fd00:abcd::53"}},
		}},
	}, SubresourceStatus)
	if err == nil || !strings.Contains(err.Error(), "reserved cluster address") {
		t.Fatalf("expected IPv6 DNS IP rejection on status, got %v", err)
	}
}

func TestRejectsServiceAndPodCIDRs(t *testing.T) {
	policy := mustPolicy(t, testOptions())
	for _, ip := range []string{"10.96.0.1", "10.244.5.9"} {
		err := policy.Validate(&Service{
			Namespace: testNamespace,
			Spec:      ServiceSpec{ExternalIPs: []string{ip}},
		}, "")
		if err == nil || !strings.Contains(err.Error(), "reserved range") {
			t.Fatalf("expected %s to be rejected as reserved range, got %v", ip, err)
		}
	}
}

func TestMappedIPv6IsReducedAndBlocked(t *testing.T) {
	policy := mustPolicy(t, testOptions())

	// Mapped form of node address 10.0.0.5 is reduced to IPv4 and hits the
	// reserved individual-address rule.
	err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"::ffff:10.0.0.5"}},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "reserved cluster address") {
		t.Fatalf("expected mapped node IP to be blocked, got %v", err)
	}

	// Mapped forms of an allowed address are accepted as an IPv4 address.
	err = policy.Validate(&Service{
		Namespace: testNamespace,
		Spec: ServiceSpec{ExternalIPs: []string{
			"::ffff:203.0.113.20",
			"0:0:0:0:0:ffff:cb00:7114",
		}},
	}, "")
	if err != nil {
		t.Fatalf("expected mapped allowed IPv4 to pass, got %v", err)
	}
}

func TestRejectsNonCanonicalNotation(t *testing.T) {
	policy := mustPolicy(t, testOptions())
	for _, ip := range []string{
		"010.0.0.1",    // octal / leading zero
		"0177.0.0.1",   // octal loopback
		"0x7f.0.0.1",   // hexadecimal
		"203.0113.0.1", // leading zero inside an otherwise allowed address
		"256.0.0.1",    // octet overflow
		"203.0.113",    // truncated dotted form
		"not-an-ip",
	} {
		err := policy.Validate(&Service{
			Namespace: testNamespace,
			Spec:      ServiceSpec{ExternalIPs: []string{ip}},
		}, "")
		if err == nil {
			t.Errorf("expected %q to be rejected", ip)
			continue
		}
		if !strings.Contains(err.Error(), "not a valid IPv4 or IPv6 address") {
			t.Errorf("expected %q to fail strict parsing, got %v", ip, err)
		}
	}
}

func TestRejectsLoopbackLinkLocalAndMulticast(t *testing.T) {
	// Allow every range so the special-purpose address checks are isolated.
	policy := mustPolicy(t, Options{
		AllowedCIDRs: []string{"0.0.0.0/0", "::/0"},
	})
	cases := map[string]string{
		"ipv4 loopback":        "127.0.0.1",
		"ipv6 loopback":        "::1",
		"ipv4 link local":      "169.254.1.1",
		"ipv6 link local":      "fe80::1",
		"ipv4 multicast":       "224.0.0.1",
		"ipv6 multicast":       "ff02::1",
		"ipv4 unspecified":     "0.0.0.0",
		"mapped ipv4 loopback": "::ffff:127.0.0.1",
	}
	for name, ip := range cases {
		err := policy.Validate(&Service{
			Namespace: testNamespace,
			Spec:      ServiceSpec{ExternalIPs: []string{ip}},
		}, "")
		if err == nil {
			t.Errorf("%s (%s): expected rejection", name, ip)
		}
	}
}

func TestRejectsAddressOutsideAllowedRanges(t *testing.T) {
	policy := mustPolicy(t, testOptions())
	err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"198.51.100.9"}},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "not within any address range allowed") {
		t.Fatalf("expected out-of-range rejection, got %v", err)
	}

	// An IPv4 address cannot be covered by an IPv6 range and vice versa.
	err = policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"2001:db8:2::1"}},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "not within any address range allowed") {
		t.Fatalf("expected wrong-family rejection, got %v", err)
	}
}

func TestEmptyAllowedListFailsClosed(t *testing.T) {
	policy := mustPolicy(t, Options{})
	err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"203.0.113.7"}},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "not within any address range allowed") {
		t.Fatalf("expected fail-closed rejection, got %v", err)
	}
}

func TestStatusSubresourceCannotBypass(t *testing.T) {
	policy := mustPolicy(t, testOptions())

	statusSvc := &Service{
		Namespace: testNamespace,
		Status: ServiceStatus{LoadBalancer: LoadBalancerStatus{
			Ingress: []LoadBalancerIngress{{IP: "10.0.0.6"}},
		}},
	}
	err := policy.Validate(statusSvc, SubresourceStatus)
	if err == nil || !strings.Contains(err.Error(), "reserved cluster address") {
		t.Fatalf("expected status update with node IP to be rejected, got %v", err)
	}

	statusSvc.Status.LoadBalancer.Ingress[0].IP = "010.0.0.1"
	err = policy.Validate(statusSvc, SubresourceStatus)
	if err == nil || !strings.Contains(err.Error(), "not a valid IPv4 or IPv6 address") {
		t.Fatalf("expected strict-parse rejection on status, got %v", err)
	}

	// A hostname-only ingress entry remains valid.
	statusSvc.Status.LoadBalancer.Ingress[0] = LoadBalancerIngress{Hostname: "lb.example.com"}
	if err := policy.Validate(statusSvc, SubresourceStatus); err != nil {
		t.Fatalf("expected hostname ingress to pass, got %v", err)
	}

	// The status path ignores spec fields entirely.
	statusSvc.Spec = ServiceSpec{
		Type:         ServiceTypeExternalName,
		ExternalName: "",
		ExternalIPs:  []string{"10.0.0.5"},
	}
	if err := policy.Validate(statusSvc, SubresourceStatus); err != nil {
		t.Fatalf("status path must not evaluate spec, got %v", err)
	}
}

func TestExternalNameValidation(t *testing.T) {
	policy := mustPolicy(t, testOptions())

	allowed := []string{
		"external.example.com",
		"api.tenant-a.svc.cluster.local", // same namespace
		"api.tenant-a.svc",
		"api.tenant-a.svc.", // trailing FQDN dot
	}
	for _, name := range allowed {
		err := policy.Validate(&Service{
			Namespace: testNamespace,
			Spec:      ServiceSpec{Type: ServiceTypeExternalName, ExternalName: name},
		}, "")
		if err != nil {
			t.Errorf("expected ExternalName %q to pass, got %v", name, err)
		}
	}

	rejected := []string{
		"10.0.0.1",                      // plain IP literal
		"2001:db8::1",                   // IPv6 literal
		"::ffff:10.0.0.1",               // mapped IP literal
		"0177.0.0.1",                    // loose octal IP literal
		"0x7f.0.0.1",                    // loose hex IP literal
		"db.tenant-b.svc.cluster.local", // other namespace
		"db.tenant-b.svc",               // other namespace, short form
		"db.tenant-b.svc.cluster.local.",
		"kubernetes.default.svc.cluster.local",
		"malformed.svc", // too few labels around "svc"
	}
	for _, name := range rejected {
		err := policy.Validate(&Service{
			Namespace: testNamespace,
			Spec:      ServiceSpec{Type: ServiceTypeExternalName, ExternalName: name},
		}, "")
		if err == nil {
			t.Errorf("expected ExternalName %q to be rejected", name)
		}
	}

	if err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{Type: ServiceTypeExternalName},
	}, ""); err == nil {
		t.Error("expected empty externalName to be rejected")
	}
}

func TestTopLevelValidateAndBuildErrors(t *testing.T) {
	err := Validate(&Service{
		Namespace: testNamespace,
		Spec:      ServiceSpec{ExternalIPs: []string{"10.0.0.5"}},
	}, testOptions())
	if err == nil {
		t.Fatal("expected package-level Validate to reject node IP")
	}

	badTopLevel := testOptions()
	badTopLevel.Reserved.CIDRs = []string{"not-a-cidr"}
	if err := Validate(&Service{Namespace: testNamespace}, badTopLevel); err == nil {
		t.Fatal("expected package-level Validate to surface build errors")
	}

	bad := testOptions()
	bad.AllowedCIDRs = []string{"not-a-cidr"}
	if _, err := NewPolicy(bad); err == nil {
		t.Fatal("expected NewPolicy to reject a malformed CIDR")
	}

	bad = testOptions()
	bad.Reserved.IPs = []string{"010.0.0.1"}
	if _, err := NewPolicy(bad); err == nil {
		t.Fatal("expected NewPolicy to reject a non-canonical reserved IP")
	}

	// Whitespace around CIDRs is trimmed and accepted.
	trimmed := testOptions()
	trimmed.AllowedCIDRs = []string{"  203.0.113.0/24  "}
	if _, err := NewPolicy(trimmed); err != nil {
		t.Fatalf("expected trimmed CIDR to be accepted, got %v", err)
	}

	trimmed = testOptions()
	trimmed.Reserved.CIDRs = []string{" 10.96.0.0/12 "}
	trimmed.Reserved.IPs = []string{" 10.0.0.5 "}
	if _, err := NewPolicy(trimmed); err != nil {
		t.Fatalf("expected trimmed reserved entries to be accepted, got %v", err)
	}

	if err := mustPolicy(t, testOptions()).Validate(nil, ""); err == nil {
		t.Fatal("expected nil service to be rejected")
	}
}

func TestMultipleViolationsAreAggregated(t *testing.T) {
	policy := mustPolicy(t, testOptions())
	err := policy.Validate(&Service{
		Namespace: testNamespace,
		Spec: ServiceSpec{ExternalIPs: []string{
			"010.0.0.1",   // invalid notation
			"10.0.0.5",    // reserved node IP
			"127.0.0.1",   // loopback and out of range
			"203.0.113.9", // valid
		}},
	}, "")
	if err == nil {
		t.Fatal("expected aggregated violations")
	}
	joined := err.Error()
	for _, want := range []string{
		"spec.externalIPs[0]",
		"spec.externalIPs[1]",
		"spec.externalIPs[2]",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected aggregated error to mention %s; got: %v", want, err)
		}
	}
	if strings.Contains(joined, "spec.externalIPs[3]") {
		t.Errorf("valid entry must not appear in errors: %v", err)
	}
}
