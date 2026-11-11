package nameconstraint

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"net"
)

// parseCertificateLenient parses a DER certificate without running the full
// crypto/x509 extension validation. In particular it accepts NameConstraints
// extensions with explicitly empty permitted subtrees, which crypto/x509
// rejects as invalid. Only the fields needed for chain verification are
// populated; signature verification remains the responsibility of the TLS
// stack (or the caller).
func parseCertificateLenient(der []byte) (*x509.Certificate, error) {
	var raw struct {
		Raw                asn1.RawContent
		TBSCertificate     asn1.RawValue
		SignatureAlgorithm asn1.RawValue
		SignatureValue     asn1.BitString
	}
	if _, err := asn1.Unmarshal(der, &raw); err != nil {
		return nil, err
	}

	var tbs struct {
		Raw        asn1.RawContent
		Version    int `asn1:"optional,default:0,explicit,tag:0"`
		Serial     asn1.RawValue
		SigAlg     asn1.RawValue
		Issuer     asn1.RawValue
		Validity   asn1.RawValue
		Subject    asn1.RawValue
		PublicKey  asn1.RawValue
		IssuerID   asn1.BitString   `asn1:"optional,tag:1,implicit"`
		SubjectID  asn1.BitString   `asn1:"optional,tag:2,implicit"`
		Extensions []pkix.Extension `asn1:"optional,explicit,tag:3"`
	}
	if _, err := asn1.Unmarshal(raw.TBSCertificate.FullBytes, &tbs); err != nil {
		return nil, err
	}

	cert := &x509.Certificate{
		Raw:               der,
		RawTBSCertificate: tbs.Raw,
		RawSubject:        tbs.Subject.FullBytes,
		RawIssuer:         tbs.Issuer.FullBytes,
		Extensions:        tbs.Extensions,
	}

	for _, ext := range tbs.Extensions {
		if !ext.Id.Equal(oidExtensionSubjectAltName) {
			continue
		}
		if err := parseSANExtension(ext.Value, cert); err != nil {
			return nil, err
		}
	}
	return cert, nil
}

// parseSANExtension decodes the GeneralNames of a SAN extension, populating
// only DNS, IP and email name forms.
func parseSANExtension(data []byte, cert *x509.Certificate) error {
	var seq asn1.RawValue
	if rest, err := asn1.Unmarshal(data, &seq); err != nil {
		return err
	} else if len(rest) != 0 {
		return asn1.SyntaxError{Msg: "trailing data after SAN"}
	}

	inner := seq.Bytes
	for len(inner) > 0 {
		var name asn1.RawValue
		var err error
		inner, err = asn1.Unmarshal(inner, &name)
		if err != nil {
			return err
		}
		switch name.Tag {
		case tagDNSName:
			cert.DNSNames = append(cert.DNSNames, string(name.Bytes))
		case tagRFC822Name:
			cert.EmailAddresses = append(cert.EmailAddresses, string(name.Bytes))
		case tagIPAddress:
			if l := len(name.Bytes); l == net.IPv4len || l == net.IPv6len {
				ip := make(net.IP, l)
				copy(ip, name.Bytes)
				cert.IPAddresses = append(cert.IPAddresses, ip)
			}
		default:
			// Other GeneralName forms are out of scope.
		}
	}
	return nil
}

// parseCertificateAny first tries the strict stdlib parser and falls back to
// the lenient parser for certificates carrying extensions stdlib rejects
// (notably empty permitted subtrees).
func parseCertificateAny(der []byte) (*x509.Certificate, error) {
	if cert, err := x509.ParseCertificate(der); err == nil {
		return cert, nil
	}
	return parseCertificateLenient(der)
}
