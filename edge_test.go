package netsec

import (
	"encoding/asn1"
	"errors"
	"testing"
)

func TestVerifySignedData_UnsignedAttrsDoNotAffectVerification(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	withUnsigned, err := appendUnsignedAttrs(t, der)
	if err != nil {
		t.Fatalf("append unsigned attrs: %v", err)
	}
	certs, err := VerifySignedData(withUnsigned, nil)
	if err != nil {
		t.Fatalf("verify with unsigned attrs: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("got %d certificates, want 1", len(certs))
	}
}

func TestVerifySignedData_MissingSignerCertificate(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stripped, err := emptyCertificateSet(t, der)
	if err != nil {
		t.Fatalf("strip certificates: %v", err)
	}
	if _, err := VerifySignedData(stripped, nil); !errors.Is(err, ErrCertificateNotFound) {
		t.Fatalf("err = %v, want ErrCertificateNotFound", err)
	}
}

func appendUnsignedAttrs(t *testing.T, der []byte) ([]byte, error) {
	t.Helper()
	ci, sd := mustParse(t, der)
	countersig := Attribute{
		Type:   asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 6},
		Values: []asn1.RawValue{marshalOctetStringValue([]byte("not signed"))},
	}
	setDER, err := asn1.Marshal([]Attribute{countersig})
	if err != nil {
		return nil, err
	}
	var setRaw asn1.RawValue
	if _, err := asn1.Unmarshal(setDER, &setRaw); err != nil {
		return nil, err
	}
	implicit := append([]byte(nil), setRaw.FullBytes...)
	implicit[0] = 0xA1
	sd.SignerInfos[0].UnsignedAttrs = asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        1,
		IsCompound: true,
		Bytes:      setRaw.Bytes,
		FullBytes:  implicit,
	}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	ci.Content = explicitContext0(sdDER)
	return asn1.Marshal(ci)
}

func emptyCertificateSet(t *testing.T, der []byte) ([]byte, error) {
	t.Helper()
	ci, sd := mustParse(t, der)
	sd.Certificates = asn1.RawValue{}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	ci.Content = explicitContext0(sdDER)
	return asn1.Marshal(ci)
}
