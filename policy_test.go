package netsec

import (
	"crypto/x509"
	"testing"
)

func testPolicyOID(arc uint64) OID {
	return MustOIDFromInts([]uint64{1, 3, 6, 1, 4, 1, 99999, arc})
}

func TestExplicitPolicyRejectsEmptyTerminalPolicy(t *testing.T) {
	policy := testPolicyOID(1)

	root := &Certificate{
		RawSubject: []byte("root"),
		RawIssuer:  []byte("root"),
		Policies:   []OID{policy},
	}
	intermediate := &Certificate{
		RawSubject:                []byte("intermediate"),
		RawIssuer:                 []byte("root"),
		Policies:                  []OID{policy},
		RequireExplicitPolicyZero: true,
	}
	leaf := &Certificate{
		RawSubject: []byte("leaf"),
		RawIssuer:  []byte("intermediate"),
	}

	chain := []*Certificate{leaf, intermediate, root}
	if PoliciesValid(chain, VerifyOptions{RequireExplicitPolicy: true}) {
		t.Fatal("path with no policy at the end was accepted while explicit policy was required")
	}

	leaf.Policies = []OID{policy}
	if !PoliciesValid(chain, VerifyOptions{RequireExplicitPolicy: true}) {
		t.Fatal("path with an acceptable terminal policy was rejected")
	}
}

func TestExplicitPolicyRejectsUnconstrainedEmptyTerminalPolicy(t *testing.T) {
	root := &Certificate{
		RawSubject: []byte("root"),
		RawIssuer:  []byte("root"),
	}
	intermediate := &Certificate{
		RawSubject: []byte("intermediate"),
		RawIssuer:  []byte("root"),
		Policies:   []OID{testPolicyOID(1)},
	}
	leaf := &Certificate{
		RawSubject: []byte("leaf"),
		RawIssuer:  []byte("intermediate"),
	}

	if PoliciesValid([]*Certificate{leaf, intermediate, root}, true) {
		t.Fatal("explicit policy requirement did not reject an empty terminal policy")
	}
}

func TestWithoutExplicitPolicyEmptyTerminalPolicyIsAllowed(t *testing.T) {
	root := &Certificate{
		RawSubject: []byte("root"),
		RawIssuer:  []byte("root"),
	}
	intermediate := &Certificate{
		RawSubject: []byte("intermediate"),
		RawIssuer:  []byte("root"),
		Policies:   []OID{testPolicyOID(1)},
	}
	leaf := &Certificate{
		RawSubject: []byte("leaf"),
		RawIssuer:  []byte("intermediate"),
	}

	if !PoliciesValid([]*Certificate{leaf, intermediate, root}) {
		t.Fatal("path without an explicit policy requirement was rejected")
	}
}

func TestUserPolicyMustRemainAcceptable(t *testing.T) {
	accepted := testPolicyOID(1)
	unacceptable := testPolicyOID(2)

	root := &Certificate{
		RawSubject: []byte("root"),
		RawIssuer:  []byte("root"),
	}
	intermediate := &Certificate{
		RawSubject: []byte("intermediate"),
		RawIssuer:  []byte("root"),
		Policies:   []OID{accepted},
	}
	leaf := &Certificate{
		RawSubject: []byte("leaf"),
		RawIssuer:  []byte("intermediate"),
		Policies:   []OID{accepted},
	}
	chain := []*Certificate{leaf, intermediate, root}

	if !PoliciesValid(chain, []OID{accepted}, true) {
		t.Fatal("path with the requested policy was rejected")
	}
	if PoliciesValid(chain, []OID{unacceptable}, true) {
		t.Fatal("path without the requested policy was accepted")
	}
}

func TestTrustAnchorOnlyPathIsAccepted(t *testing.T) {
	root := &Certificate{
		RawSubject:                []byte("root"),
		RawIssuer:                 []byte("root"),
		RequireExplicitPolicyZero: true,
	}
	if !PoliciesValid([]*Certificate{root}, VerifyOptions{RequireExplicitPolicy: true}) {
		t.Fatal("trust-anchor-only path was rejected")
	}
}

func TestEmptyPathIsRejected(t *testing.T) {
	if PoliciesValid(nil, VerifyOptions{RequireExplicitPolicy: true}) {
		t.Fatal("empty path was accepted")
	}
}

func TestParseOIDCompatibility(t *testing.T) {
	oid, err := x509.OIDFromInts([]uint64{2, 5, 29, 32, 0})
	if err != nil {
		t.Fatal(err)
	}
	if !oid.Equal(AnyPolicyOID) {
		t.Fatal("AnyPolicyOID does not match crypto/x509 anyPolicy")
	}
}

func TestPolicyIntersection(t *testing.T) {
	policy1 := testPolicyOID(1)
	policy2 := testPolicyOID(2)
	policy3 := testPolicyOID(3)

	root := &Certificate{RawSubject: []byte("root"), RawIssuer: []byte("root")}
	intermediate := &Certificate{
		RawSubject: []byte("intermediate"),
		RawIssuer:  []byte("root"),
		Policies:   []OID{policy1, policy2},
	}
	leaf := &Certificate{
		RawSubject: []byte("leaf"),
		RawIssuer:  []byte("intermediate"),
		Policies:   []OID{policy1},
	}
	chain := []*Certificate{leaf, intermediate, root}

	if !PoliciesValid(chain, []OID{policy1}, true) {
		t.Fatal("path with policy1 should be valid")
	}
	if PoliciesValid(chain, []OID{policy3}, true) {
		t.Fatal("path with policy3 should be invalid")
	}

	leaf.Policies = []OID{AnyPolicyOID}
	if !PoliciesValid(chain, []OID{policy1}, true) {
		t.Fatal("anyPolicy should intersect with policy1")
	}
	if PoliciesValid(chain, []OID{policy3}, true) {
		t.Fatal("anyPolicy should not make policy3 acceptable")
	}
}

func TestPolicyMapping(t *testing.T) {
	policy1 := testPolicyOID(1)
	policy3 := testPolicyOID(3)

	root := &Certificate{RawSubject: []byte("root"), RawIssuer: []byte("root")}
	intermediate := &Certificate{
		RawSubject: []byte("intermediate"),
		RawIssuer:  []byte("root"),
		Policies:   []OID{policy3},
		PolicyMappings: []PolicyMapping{{
			IssuerDomainPolicy:  policy3,
			SubjectDomainPolicy: policy1,
		}},
	}
	leaf := &Certificate{
		RawSubject: []byte("leaf"),
		RawIssuer:  []byte("intermediate"),
		Policies:   []OID{policy1},
	}
	chain := []*Certificate{leaf, intermediate, root}

	if !PoliciesValid(chain, []OID{policy3}, true) {
		t.Fatal("mapped policy should be acceptable")
	}
	if PoliciesValid(chain, VerifyOptions{
		CertificatePolicies:   []OID{policy3},
		RequireExplicitPolicy: true,
		InhibitPolicyMapping:  true,
	}) {
		t.Fatal("inhibited policy mapping should not be acceptable")
	}
}
