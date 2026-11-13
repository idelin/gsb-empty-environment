package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"testing"
)

func TestValidatePolicyMappingsConcreteOIDs(t *testing.T) {
	first := PolicyMapping{
		IssuerDomainPolicy:  OID{1, 2, 3, 4},
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}
	second := PolicyMapping{
		IssuerDomainPolicy:  OID{2, 16, 840, 1, 113730, 1, 1},
		SubjectDomainPolicy: OID{1, 3, 6, 1, 4, 1, 99},
	}

	got, err := NormalizePolicyMappings([]PolicyMapping{first, second})
	if err != nil {
		t.Fatalf("NormalizePolicyMappings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d mappings, want 2", len(got))
	}
	if !got[0].Equal(first) || !got[1].Equal(second) {
		t.Fatalf("concrete mappings were not preserved: got %#v", got)
	}
}

func TestValidatePolicyMappingsRejectsAnyPolicy(t *testing.T) {
	tests := []struct {
		name    string
		mapping PolicyMapping
		end     PolicyMappingEnd
	}{
		{
			name: "issuer anyPolicy",
			mapping: PolicyMapping{
				IssuerDomainPolicy:  AnyPolicyOID,
				SubjectDomainPolicy: OID{1, 2, 3, 4},
			},
			end: IssuerDomainPolicy,
		},
		{
			name: "subject anyPolicy",
			mapping: PolicyMapping{
				IssuerDomainPolicy:  OID{1, 2, 3, 4},
				SubjectDomainPolicy: AnyPolicyOID,
			},
			end: SubjectDomainPolicy,
		},
		{
			name: "both anyPolicy",
			mapping: PolicyMapping{
				IssuerDomainPolicy:  AnyPolicyOID,
				SubjectDomainPolicy: AnyPolicyOID,
			},
			end: BothDomainPolicies,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePolicyMappings([]PolicyMapping{tt.mapping})
			if !errors.Is(err, ErrAnyPolicyMapping) {
				t.Fatalf("error = %v, want ErrAnyPolicyMapping", err)
			}
			var mappingErr *AnyPolicyMappingError
			if !errors.As(err, &mappingErr) {
				t.Fatalf("error type = %T, want *AnyPolicyMappingError", err)
			}
			if mappingErr.End != tt.end {
				t.Fatalf("End = %q, want %q", mappingErr.End, tt.end)
			}
		})
	}
}

func TestValidatePolicyMappingsAcceptsX509Mappings(t *testing.T) {
	anyPolicy, err := x509.ParseOID(AnyPolicyOIDString)
	if err != nil {
		t.Fatal(err)
	}
	concrete, err := x509.ParseOID("1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidatePolicyMappings([]x509.PolicyMapping{{
		IssuerDomainPolicy:  concrete,
		SubjectDomainPolicy: concrete,
	}}); err != nil {
		t.Fatalf("concrete mapping rejected: %v", err)
	}
	if err := ValidatePolicyMappings([]x509.PolicyMapping{{
		IssuerDomainPolicy:  anyPolicy,
		SubjectDomainPolicy: concrete,
	}}); !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("anyPolicy mapping error = %v, want ErrAnyPolicyMapping", err)
	}
}

func TestPolicyMappingSetPreservesMappings(t *testing.T) {
	valid := PolicyMapping{
		IssuerDomainPolicy:  OID{1, 2, 3, 4},
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}
	invalid := PolicyMapping{
		IssuerDomainPolicy:  AnyPolicyOID,
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}

	set, err := NewPolicyMappingSet([]PolicyMapping{valid, valid})
	if err != nil {
		t.Fatalf("NewPolicyMappingSet: %v", err)
	}
	got := set.Mappings()
	if len(got) != 2 || !got[0].Equal(valid) || !got[1].Equal(valid) {
		t.Fatalf("mappings not preserved: %#v", got)
	}
	subjects, err := set.SubjectsFor(valid.IssuerDomainPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 2 || !subjects[0].Equal(valid.SubjectDomainPolicy) {
		t.Fatalf("SubjectsFor = %#v", subjects)
	}

	if _, err := NewPolicyMappingSet([]PolicyMapping{valid, invalid}); !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("set with illegal mapping error = %v", err)
	}
}

func TestParsePolicyMappingsExtension(t *testing.T) {
	valid := []PolicyMapping{{
		IssuerDomainPolicy:  OID{1, 2, 3, 4},
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}}
	der, err := MarshalPolicyMappingsExtension(valid)
	if err != nil {
		t.Fatalf("MarshalPolicyMappingsExtension: %v", err)
	}
	got, err := ParsePolicyMappingsExtension(der)
	if err != nil {
		t.Fatalf("ParsePolicyMappingsExtension: %v", err)
	}
	if len(got) != 1 || !got[0].Equal(valid[0]) {
		t.Fatalf("got %#v, want %#v", got, valid)
	}

	invalid := []asn1PolicyMapping{{
		IssuerDomainPolicy:  AnyPolicyOID,
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}}
	invalidDER, err := asn1.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePolicyMappingsExtension(invalidDER); !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("parse illegal mapping error = %v, want ErrAnyPolicyMapping", err)
	}
	decoded, err := DecodePolicyMappingsExtension(invalidDER)
	if err != nil {
		t.Fatalf("decode illegal mapping should not validate: %v", err)
	}
	if len(decoded) != 1 || !decoded[0].IssuerDomainPolicy.Equal(AnyPolicyOID) {
		t.Fatalf("decoded mappings = %#v", decoded)
	}
}

func TestValidateCertificatePolicyMappings(t *testing.T) {
	valid := PolicyMapping{
		IssuerDomainPolicy:  OID{1, 2, 3, 4},
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}
	validDER, err := MarshalPolicyMappingsExtension([]PolicyMapping{valid})
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		Extensions: []pkix.Extension{{
			Id:       PolicyMappingsOID,
			Critical: true,
			Value:    validDER,
		}},
	}
	if err := ValidateCertificatePolicyMappings(cert); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}

	invalidDER, err := asn1.Marshal([]asn1PolicyMapping{{
		IssuerDomainPolicy:  AnyPolicyOID,
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cert.Extensions[0].Value = invalidDER
	if err := ValidateCertificatePolicyMappings(cert); !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("certificate error = %v, want ErrAnyPolicyMapping", err)
	}
}

func TestValidatePathPolicyMappings(t *testing.T) {
	validDER, err := MarshalPolicyMappingsExtension([]PolicyMapping{{
		IssuerDomainPolicy:  OID{1, 2, 3, 4},
		SubjectDomainPolicy: OID{1, 2, 3, 5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := x509.ParseOID(AnyPolicyOIDString)
	if err != nil {
		t.Fatal(err)
	}
	concrete, err := x509.ParseOID("1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}

	chain := []*x509.Certificate{
		{},
		{Extensions: []pkix.Extension{{Id: PolicyMappingsOID, Value: validDER}}},
		{PolicyMappings: []x509.PolicyMapping{{
			IssuerDomainPolicy:  invalid,
			SubjectDomainPolicy: concrete,
		}}},
	}
	err = ValidatePathPolicyMappings(chain)
	if !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("path error = %v, want ErrAnyPolicyMapping", err)
	}
	var pathErr *PathPolicyMappingError
	if !errors.As(err, &pathErr) || pathErr.Index != 2 {
		t.Fatalf("path error = %#v, want index 2", err)
	}
}

func TestOIDInputForms(t *testing.T) {
	if !IsAnyPolicy("2.5.29.32.0") || !IsAnyPolicy(AnyPolicy) || !IsAnyPolicy([]int{2, 5, 29, 32, 0}) {
		t.Fatal("IsAnyPolicy did not recognize all supported forms")
	}
	if err := ValidatePolicyMapping("1.2.3.4", "1.2.3.5"); err != nil {
		t.Fatalf("string OIDs rejected: %v", err)
	}
	if err := ValidatePolicyMapping("2.5.29.32.0", "1.2.3.4"); !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("string anyPolicy error = %v", err)
	}

	found, err := ContainsAnyPolicy(PolicyMapping{
		IssuerDomainPolicy:  AnyPolicyOID,
		SubjectDomainPolicy: OID{1, 2, 3, 4},
	})
	if err != nil || !found {
		t.Fatalf("ContainsAnyPolicy = %v, %v; want true, nil", found, err)
	}

	mapping, err := NewPolicyMapping("1.2.3.4", "1.2.3.5")
	if err != nil || !mapping.Equal(PolicyMapping{OID{1, 2, 3, 4}, OID{1, 2, 3, 5}}) {
		t.Fatalf("NewPolicyMapping = %#v, %v", mapping, err)
	}
	if err := ValidatePolicyMappings("2.5.29.32.0", "1.2.3.4"); !errors.Is(err, ErrAnyPolicyMapping) {
		t.Fatalf("two-argument ValidatePolicyMappings error = %v", err)
	}
}
