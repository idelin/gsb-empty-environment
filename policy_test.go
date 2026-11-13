package netsec

import (
	"crypto/x509/pkix"
	"testing"
)

func testPolicyOID(n uint64) OID {
	return mustOIDFromInts([]uint64{1, 2, 3, 4, n})
}

func testCert(subject, issuer string, policies []OID) *Certificate {
	return &Certificate{
		RawSubject: []byte(subject),
		RawIssuer:  []byte(issuer),
		Subject:    pkix.Name{CommonName: subject},
		Issuer:     pkix.Name{CommonName: issuer},
		Policies:   policies,
	}
}

func TestAnyPolicyAtInhibitionDepth(t *testing.T) {
	oid1 := testPolicyOID(1)
	root := testCert("root", "root", []OID{AnyPolicyOID})
	leaf := testCert("leaf", "ca", []OID{AnyPolicyOID})

	tests := []struct {
		name   string
		pinned bool
		valid  bool
	}{
		{"inhibited", true, false},
		{"one_more_certificate", false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ca := testCert("ca", "root", []OID{AnyPolicyOID})
			if tc.pinned {
				ca.InhibitAnyPolicyZero = true
				ca.InhibitAnyPolicy = 0
			} else {
				ca.InhibitAnyPolicy = 1
			}

			leafFirst := []*Certificate{leaf, ca, root}
			got := PoliciesValid(leafFirst, VerifyOptions{
				CertificatePolicies:   []OID{oid1},
				RequireExplicitPolicy: true,
			})
			if got != tc.valid {
				t.Fatalf("PoliciesValid(leaf-first) = %v, want %v", got, tc.valid)
			}

			rootFirst := []*Certificate{root, ca, leaf}
			if got := PoliciesValid(rootFirst, VerifyOptions{
				CertificatePolicies:   []OID{oid1},
				RequireExplicitPolicy: true,
			}); got != tc.valid {
				t.Fatalf("PoliciesValid(root-first) = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestAnyPolicyDoesNotCreateConcretePoliciesAfterInhibit(t *testing.T) {
	oid1 := testPolicyOID(1)

	root := testCert("root", "root", []OID{AnyPolicyOID})
	ca1 := testCert("ca1", "root", []OID{AnyPolicyOID})
	ca2 := testCert("ca2", "ca1", []OID{AnyPolicyOID})
	ca2.InhibitAnyPolicyZero = true
	ca2.InhibitAnyPolicy = 0
	leaf := testCert("leaf", "ca2", []OID{AnyPolicyOID})

	evaluation := EvaluatePolicies([]*Certificate{leaf, ca2, ca1, root}, VerifyOptions{
		CertificatePolicies:   []OID{oid1},
		RequireExplicitPolicy: true,
	})
	if evaluation.Valid {
		t.Fatal("path validated even though the post-inhibition leaf only asserts anyPolicy")
	}
	if len(evaluation.Policies) != 0 {
		t.Fatalf("post-inhibition anyPolicy produced policies: %v", evaluation.Policies)
	}

	// A concrete policy asserted by the post-inhibition leaf is still valid:
	// inhibitAnyPolicy prevents anyPolicy from standing in for other policies,
	// it does not forbid explicit policy assertions.
	leaf.Policies = []OID{oid1}
	evaluation = EvaluatePolicies([]*Certificate{leaf, ca2, ca1, root}, VerifyOptions{
		CertificatePolicies:   []OID{oid1},
		RequireExplicitPolicy: true,
	})
	if !evaluation.Valid {
		t.Fatal("explicit policy at the post-inhibition leaf was rejected")
	}
}

func TestInitialInhibitAnyPolicyOption(t *testing.T) {
	oid1 := testPolicyOID(1)

	root := testCert("root", "root", []OID{AnyPolicyOID})
	ca := testCert("ca", "root", []OID{AnyPolicyOID})
	leaf := testCert("leaf", "ca", []OID{AnyPolicyOID})

	if PoliciesValid([]*Certificate{leaf, ca, root}, VerifyOptions{
		CertificatePolicies:   []OID{oid1},
		RequireExplicitPolicy: true,
		InhibitAnyPolicy:      true,
	}) {
		t.Fatal("initial inhibitAnyPolicy did not prevent anyPolicy from creating policies")
	}
}

func TestPreInhibitionAnyPolicyMappingIsRetained(t *testing.T) {
	oid1 := testPolicyOID(1)

	root := testCert("root", "root", []OID{AnyPolicyOID})
	ca1 := testCert("ca1", "root", []OID{AnyPolicyOID})
	ca2 := testCert("ca2", "ca1", []OID{oid1})
	ca2.InhibitAnyPolicyZero = true
	ca2.InhibitAnyPolicy = 0
	leaf := testCert("leaf", "ca2", []OID{oid1})

	evaluation := EvaluatePolicies([]*Certificate{leaf, ca2, ca1, root}, VerifyOptions{
		CertificatePolicies:   []OID{oid1},
		RequireExplicitPolicy: true,
	})
	if !evaluation.Valid {
		t.Fatal("valid mapping created before the inhibition depth was lost")
	}
	if len(evaluation.Policies) != 1 || !evaluation.Policies[0].Equal(oid1) {
		t.Fatalf("valid policy set = %v, want [%s]", evaluation.Policies, oid1)
	}
}

func TestInvalidAnyPolicyMapping(t *testing.T) {
	oid1 := testPolicyOID(1)

	root := testCert("root", "root", []OID{AnyPolicyOID})
	ca := testCert("ca", "root", []OID{oid1})
	ca.PolicyMappings = []PolicyMapping{{
		IssuerDomainPolicy:  AnyPolicyOID,
		SubjectDomainPolicy: oid1,
	}}
	leaf := testCert("leaf", "ca", []OID{oid1})

	if PoliciesValid([]*Certificate{leaf, ca, root}, VerifyOptions{
		CertificatePolicies:   []OID{oid1},
		RequireExplicitPolicy: true,
	}) {
		t.Fatal("mapping from anyPolicy to a concrete policy was accepted")
	}
}
