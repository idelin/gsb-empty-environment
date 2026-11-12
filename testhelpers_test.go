package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"time"
)

// uriGeneralName builds a uniformResourceIdentifier GeneralName, i.e. an
// IA5String wrapped in context-specific tag [6] (constructed, primitive).
func uriGeneralName(uri string) asn1.RawValue {
	return asn1.RawValue{
		Class: asn1.ClassContextSpecific,
		Tag:   6,
		Bytes: []byte(uri),
	}
}

// fullName wraps GeneralNames inside distributionPoint [0] EXPLICIT ->
// fullName [0] IMPLICIT GeneralNames.
func fullName(uris ...string) asn1.RawValue {
	names := make([]asn1.RawValue, 0, len(uris))
	for _, uri := range uris {
		names = append(names, uriGeneralName(uri))
	}
	generalNamesDER, err := asn1.Marshal(names)
	if err != nil {
		panic(err)
	}
	fullNameValue := asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      generalNamesDER,
	}
	fullNameDER, err := asn1.Marshal(fullNameValue)
	if err != nil {
		panic(err)
	}
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      fullNameDER,
	}
}

// reasonBitString wraps a ReasonFlags BIT STRING in the implicit [1] tag.
func reasonBitString() asn1.BitString {
	return asn1.BitString{Bytes: []byte{0x80}, BitLength: 1} // keyCompromise
}

// crlIssuer wraps one directoryName GeneralName ([4] explicit Name) in the
// cRLIssuer [2] IMPLICIT GeneralNames field.
func crlIssuer() []asn1.RawValue {
	name := pkix.Name{CommonName: "indirect-crl-issuer"}
	nameDER, err := asn1.Marshal(name.ToRDNSequence())
	if err != nil {
		panic(err)
	}
	directoryName := asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        4,
		IsCompound: true,
		Bytes:      nameDER,
	}
	return []asn1.RawValue{directoryName}
}

// buildCDPExtension DER-encodes an id-ce-cRLDistributionPoints extension from
// prebuilt DistributionPoint field values.
func buildCDPExtension(pts ...asn1DistributionPoint) pkix.Extension {
	dps := asn1DistributionPoints{Points: pts}
	value, err := asn1.Marshal(dps)
	if err != nil {
		panic(err)
	}
	return pkix.Extension{Id: oidExtensionCRLDistributionPoints, Value: value}
}

// certWithCDP creates a self-signed leaf certificate carrying the supplied
// extension. x509 ignores unknown CDP details when given as an ExtraExtension.
func certWithCDP(exts ...pkix.Extension) *x509.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "test-leaf"},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(time.Hour),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtraExtensions: exts,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}
