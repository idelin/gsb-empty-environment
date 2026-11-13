package netsec

import (
	"encoding/asn1"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type testCapability struct {
	ID asn1.ObjectIdentifier
}

func mustCapabilities(t *testing.T, algorithms ...Algorithm) []byte {
	t.Helper()
	capabilities := make([]testCapability, 0, len(algorithms))
	for _, algorithm := range algorithms {
		capabilities = append(capabilities, testCapability{ID: mustASN1OID(t, algorithm.OID())})
	}
	der, err := asn1.Marshal(capabilities)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	return der
}

func mustASN1OID(t *testing.T, dotted string) asn1.ObjectIdentifier {
	t.Helper()
	parts := strings.Split(dotted, ".")
	if len(parts) < 2 {
		t.Fatalf("invalid OID %q", dotted)
	}
	oid := make(asn1.ObjectIdentifier, len(parts))
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			t.Fatalf("invalid OID %q: %v", dotted, err)
		}
		oid[i] = value
	}
	return oid
}

func derWrap(tag byte, content []byte) []byte {
	out := []byte{tag}
	length := len(content)
	if length < 0x80 {
		out = append(out, byte(length))
	} else {
		var lengthBytes []byte
		for n := length; n > 0; n >>= 8 {
			lengthBytes = append([]byte{byte(n)}, lengthBytes...)
		}
		out = append(out, 0x80|byte(len(lengthBytes)))
		out = append(out, lengthBytes...)
	}
	return append(out, content...)
}

func mustAttribute(t *testing.T, oid asn1.ObjectIdentifier, values ...[]byte) []byte {
	t.Helper()
	oidDER, err := asn1.Marshal(oid)
	if err != nil {
		t.Fatalf("marshal attribute OID: %v", err)
	}
	var setContent []byte
	for _, value := range values {
		setContent = append(setContent, value...)
	}
	content := append(oidDER, derWrap(derTagSet, setContent)...)
	return derWrap(derTagSequence, content)
}

func mustSignedAttributes(t *testing.T, attributes ...[]byte) []byte {
	t.Helper()
	var content []byte
	for _, attribute := range attributes {
		content = append(content, attribute...)
	}
	return derWrap(derTagContextZeroSet, content)
}

func TestParseSMIMECapabilities(t *testing.T) {
	unknown := Algorithm("1.2.3.4.5")
	der := mustCapabilities(t, AES256GCM, TripleDESCBC, unknown, AES256GCM)
	got, err := ParseSMIMECapabilities(der)
	if err != nil {
		t.Fatalf("ParseSMIMECapabilities: %v", err)
	}
	want := []Algorithm{AES256GCM, TripleDESCBC, unknown}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseSMIMECapabilitiesWithParameters(t *testing.T) {
	oidDER, err := asn1.Marshal(mustASN1OID(t, AES256GCM.OID()))
	if err != nil {
		t.Fatalf("marshal OID: %v", err)
	}
	nullDER, err := asn1.Marshal(asn1.NullRawValue)
	if err != nil {
		t.Fatalf("marshal NULL: %v", err)
	}
	capability := derWrap(derTagSequence, append(oidDER, nullDER...))
	der := derWrap(derTagSequence, capability)
	got, err := ParseSMIMECapabilities(der)
	if err != nil {
		t.Fatalf("ParseSMIMECapabilities: %v", err)
	}
	if len(got) != 1 || got[0] != AES256GCM {
		t.Fatalf("got %v, want [%s]", got, AES256GCM)
	}
}

func TestParseSMIMECapabilitiesRejectsMalformedDER(t *testing.T) {
	cases := map[string][]byte{
		"empty":                  nil,
		"wrong outer tag":        {derTagSet, 0x00},
		"truncated":              {derTagSequence, 0x01},
		"trailing data":          append(mustCapabilities(t, AES256GCM), 0x00),
		"capability not seq":     {derTagSequence, 0x03, derTagOID, 0x01, 0x2a},
		"empty capability OID":   {derTagSequence, 0x04, derTagSequence, 0x02, derTagOID, 0x00},
		"trailing in capability": {derTagSequence, 0x08, derTagSequence, 0x06, derTagOID, 0x03, 0x2a, 0x86, 0x48, 0x00},
	}
	for name, der := range cases {
		if _, err := ParseSMIMECapabilities(der); !errors.Is(err, ErrInvalidCapabilities) {
			t.Errorf("%s: error = %v, want ErrInvalidCapabilities", name, err)
		}
	}
}

func TestExtractSMIMECapabilitiesFromSignedAttributes(t *testing.T) {
	capabilities := mustCapabilities(t, AES128GCM, RC2CBC)
	attribute := mustAttribute(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 10}, capabilities)
	signedAttributes := mustSignedAttributes(t, attribute)

	got, found, err := ExtractSMIMECapabilitiesFromSignedAttributes(signedAttributes)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !found {
		t.Fatal("capabilities attribute not found")
	}
	if len(got) != 2 || got[0] != AES128GCM || got[1] != RC2CBC {
		t.Fatalf("got %v", got)
	}
}

func TestExtractSMIMECapabilitiesAcceptsBareSet(t *testing.T) {
	capabilities := mustCapabilities(t, AES256GCM)
	attribute := mustAttribute(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 10}, capabilities)
	bareSet := derWrap(derTagSet, attribute)
	got, found, err := ExtractSMIMECapabilitiesFromSignedAttributes(bareSet)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !found || len(got) != 1 || got[0] != AES256GCM {
		t.Fatalf("got %v, found=%v", got, found)
	}
}

func TestExtractSMIMECapabilitiesAbsentAttribute(t *testing.T) {
	value, err := asn1.Marshal("text/plain")
	if err != nil {
		t.Fatalf("marshal content type: %v", err)
	}
	contentType := mustAttribute(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 2}, value)
	signedAttributes := mustSignedAttributes(t, contentType)

	got, found, err := ExtractSMIMECapabilitiesFromSignedAttributes(signedAttributes)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if found || got != nil {
		t.Fatalf("got %v, found=%v; want absent", got, found)
	}
}

func TestExtractSMIMECapabilitiesRejectsDuplicateAttribute(t *testing.T) {
	capabilities := mustCapabilities(t, AES256GCM)
	attribute := mustAttribute(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 10}, capabilities)
	signedAttributes := mustSignedAttributes(t, attribute, attribute)
	if _, _, err := ExtractSMIMECapabilitiesFromSignedAttributes(signedAttributes); !errors.Is(err, ErrInvalidCapabilities) {
		t.Fatalf("error = %v, want ErrInvalidCapabilities", err)
	}
}

func TestExtractSMIMECapabilitiesRejectsAmbiguousValues(t *testing.T) {
	capabilities := mustCapabilities(t, AES256GCM)
	attribute := mustAttribute(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 10}, capabilities, capabilities)
	signedAttributes := mustSignedAttributes(t, attribute)
	if _, _, err := ExtractSMIMECapabilitiesFromSignedAttributes(signedAttributes); !errors.Is(err, ErrInvalidCapabilities) {
		t.Fatalf("error = %v, want ErrInvalidCapabilities", err)
	}
}
