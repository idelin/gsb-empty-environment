package rpki

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestParseTALTextAndValidate(t *testing.T) {
	h := newTestHarness(t)
	ee := h.createEE("mft-ee", h.root, inheritClaims())
	crlName := h.writeCRL(h.root)
	h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName})

	parsed, err := ParseTAL("text-tal", h.tal.WriteText())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.URIs) != 1 || parsed.URIs[0] != h.tal.URIs[0] {
		t.Fatalf("bad TAL URIs: %#v", parsed.URIs)
	}
	if !bytesEqual(parsed.SubjectPublicKeyInfo, h.tal.SubjectPublicKeyInfo) {
		t.Fatal("TAL SPKI mismatch")
	}
	report, err := NewValidator(h.repository(), Options{Now: fixedNow(h.now)}).ValidateTAL(context.Background(), parsed)
	if err != nil {
		t.Fatalf("ValidateTAL with parsed text TAL: %v", err)
	}
	if !report.Valid {
		t.Fatal(report.Err)
	}
}

func TestParseTALJSON(t *testing.T) {
	h := newTestHarness(t)
	encoded := base64.StdEncoding.EncodeToString(h.tal.SubjectPublicKeyInfo)
	raw, err := json.Marshal(jsonTAL{
		Name:                 "json-tal",
		URIs:                 h.tal.URIs,
		SubjectPublicKeyInfo: encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseTAL("fallback", raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "json-tal" {
		t.Fatalf("name = %q", parsed.Name)
	}
	if _, err := parsed.ECDSAP256PublicKey(); err != nil {
		t.Fatal(err)
	}
}
