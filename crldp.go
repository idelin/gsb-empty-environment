package netsec

import (
	"crypto/x509"
	"encoding/asn1"
)

// oidExtensionCRLDistributionPoints is id-ce-cRLDistributionPoints (2.5.29.31).
var oidExtensionCRLDistributionPoints = asn1.ObjectIdentifier{2, 5, 29, 31}

// dpEntry is the security-relevant view of one CRL distribution point.
type dpEntry struct {
	// uris holds the URI general names from distributionPoint/fullName.
	uris []string
	// reasons is set when the distributionPoint carries a reasonFlags field,
	// meaning the CRL only covers some revocation reasons (indirect CRL).
	reasons bool
	// crlIssuer is set when a cRLIssuer field names a different CRL issuer
	// (indirect CRL).
	crlIssuer bool
}

// asn1 structures follow RFC 5280 4.2.1.13:
//
//	CRLDistributionPoints ::= SEQUENCE SIZE (1..MAX) OF DistributionPoint
//	DistributionPoint ::= SEQUENCE {
//	  distributionPoint [0] DistributionPointName OPTIONAL,
//	  reasons           [1] ReasonFlags OPTIONAL,
//	  cRLIssuer         [2] GeneralNames OPTIONAL }
//	DistributionPointName ::= CHOICE {
//	  fullName                 [0] GeneralNames,
//	  nameRelativeToCRLIssuer  [1] RelativeDistinguishedName }
//	GeneralNames ::= SEQUENCE SIZE (1..MAX) OF GeneralName
//	GeneralName ::= CHOICE { ... uniformResourceIdentifier [6] IA5String, ... }

type asn1DistributionPoints struct {
	Points []asn1DistributionPoint `asn1:"sequence"`
}

type asn1DistributionPoint struct {
	DistributionPoint asn1.RawValue   `asn1:"tag:0,optional,explicit"`
	Reasons           asn1.BitString  `asn1:"tag:1,optional"`
	CRLIssuer         []asn1.RawValue `asn1:"tag:2,optional"`
}

// parseCRLDistributionPoints extracts the security-relevant fields from the
// id-ce-cRLDistributionPoints extension in a DER certificate. A certificate
// without the extension yields no entries and no error.
func parseCRLDistributionPoints(der []byte) ([]dpEntry, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	var ext []byte
	for _, e := range cert.Extensions {
		if e.Id.Equal(oidExtensionCRLDistributionPoints) {
			ext = e.Value
			break
		}
	}
	if ext == nil {
		return nil, nil
	}

	var dps asn1DistributionPoints
	if _, err := asn1.Unmarshal(ext, &dps); err != nil {
		return nil, err
	}

	entries := make([]dpEntry, 0, len(dps.Points))
	for _, dp := range dps.Points {
		entry := dpEntry{
			reasons:   dp.Reasons.BitLength > 0,
			crlIssuer: len(dp.CRLIssuer) > 0,
		}
		entry.uris = extractFullNameURIs(dp.DistributionPoint)
		entries = append(entries, entry)
	}
	return entries, nil
}

// extractFullNameURIs pulls uniformResourceIdentifier (context tag 6) values
// out of an explicit [0] fullName distribution point name. Other name forms
// and nameRelativeToCRLIssuer are ignored.
func extractFullNameURIs(distributionPoint asn1.RawValue) []string {
	if len(distributionPoint.Bytes) == 0 {
		return nil
	}

	var name asn1.RawValue
	if _, err := asn1.Unmarshal(distributionPoint.Bytes, &name); err != nil {
		return nil
	}
	// fullName is the [0] alternative of DistributionPointName.
	if name.Tag != 0 || name.Class != asn1.ClassContextSpecific {
		return nil
	}

	var generalNames []asn1.RawValue
	if _, err := asn1.Unmarshal(name.Bytes, &generalNames); err != nil {
		return nil
	}

	var uris []string
	for _, generalName := range generalNames {
		if generalName.Class == asn1.ClassContextSpecific && generalName.Tag == 6 {
			uris = append(uris, string(generalName.Bytes))
		}
	}
	return uris
}
