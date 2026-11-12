package netsec

import (
	"encoding/asn1"
	"testing"
)

func mustCert(t *testing.T, kind keyKind) *testCert {
	t.Helper()
	tc, err := generateTestCert(kind)
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	return tc
}

func mustParse(t *testing.T, der []byte) (ContentInfo, SignedData) {
	t.Helper()
	var ci ContentInfo
	if rest, err := asn1.Unmarshal(der, &ci); err != nil {
		t.Fatalf("unmarshal ContentInfo: %v", err)
	} else if len(rest) > 0 {
		t.Fatal("trailing data after ContentInfo")
	}
	var sd SignedData
	if rest, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		t.Fatalf("unmarshal SignedData: %v", err)
	} else if len(rest) > 0 {
		t.Fatal("trailing data after SignedData")
	}
	return ci, sd
}

func removeAttr(attrs []Attribute, oid asn1.ObjectIdentifier) []Attribute {
	out := make([]Attribute, 0, len(attrs))
	for _, attr := range attrs {
		if !attr.Type.Equal(oid) {
			out = append(out, attr)
		}
	}
	return out
}

func duplicateAttr(attrs []Attribute, oid asn1.ObjectIdentifier) []Attribute {
	out := make([]Attribute, 0, len(attrs)+1)
	for _, attr := range attrs {
		out = append(out, attr)
		if attr.Type.Equal(oid) {
			out = append(out, attr)
		}
	}
	return out
}

// flipSignatureByte re-encodes a SignedData with one byte of the first
// SignerInfo signature inverted, returning a new DER buffer.
func flipSignatureByte(t *testing.T, der []byte) []byte {
	t.Helper()
	ci, sd := mustParse(t, der)
	sig := sd.SignerInfos[0].Signature
	if len(sig) == 0 {
		t.Fatal("empty signature")
	}
	sig[0] ^= 0xFF
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		t.Fatalf("marshal SignedData: %v", err)
	}
	ci.Content = explicitContext0(sdDER)
	out, err := asn1.Marshal(ci)
	if err != nil {
		t.Fatalf("marshal ContentInfo: %v", err)
	}
	return out
}

// stripPSSParams re-encodes the SignedData after removing the
// RSASSA-PSS AlgorithmIdentifier parameters.
func stripPSSParams(t *testing.T, der []byte) ([]byte, error) {
	t.Helper()
	ci, sd := mustParse(t, der)
	sd.SignerInfos[0].SignatureAlgorithm.Parameters = asn1.RawValue{}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	ci.Content = explicitContext0(sdDER)
	return asn1.Marshal(ci)
}

// omitPSSSaltLength re-encodes the SignedData after removing the [2]
// saltLength field from the RSASSA-PSS parameters.
func omitPSSSaltLength(t *testing.T, der []byte) ([]byte, error) {
	t.Helper()
	ci, sd := mustParse(t, der)
	paramsDER := sd.SignerInfos[0].SignatureAlgorithm.Parameters.FullBytes
	var outer asn1.RawValue
	if _, err := asn1.Unmarshal(paramsDER, &outer); err != nil {
		return nil, err
	}
	var kept []byte
	remain := outer.Bytes
	for len(remain) > 0 {
		var field asn1.RawValue
		var err error
		remain, err = asn1.Unmarshal(remain, &field)
		if err != nil {
			return nil, err
		}
		if !(field.Class == asn1.ClassContextSpecific && field.Tag == 2) {
			kept = append(kept, field.FullBytes...)
		}
	}
	newParams, err := asn1.Marshal(asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSequence,
		IsCompound: true,
		Bytes:      kept,
	})
	if err != nil {
		return nil, err
	}
	var newRaw asn1.RawValue
	if _, err := asn1.Unmarshal(newParams, &newRaw); err != nil {
		return nil, err
	}
	sd.SignerInfos[0].SignatureAlgorithm.Parameters = newRaw
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	ci.Content = explicitContext0(sdDER)
	return asn1.Marshal(ci)
}
