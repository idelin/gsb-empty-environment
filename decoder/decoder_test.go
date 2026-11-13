package decoder_test

import (
	"testing"

	"example.org/netsec/q0324/decoder"
)

func TestDecodeHostnameAppliesJoinerContext(t *testing.T) {
	if _, err := decoder.DecodeHostname("a\u200cb"); err == nil {
		t.Fatal("DecodeHostname accepted ZWNJ between Latin letters")
	}
	if _, err := decoder.DecodeHostname("a\u200db"); err == nil {
		t.Fatal("DecodeHostname accepted ZWJ between Latin letters")
	}

	const host = "\u0628\u200c\u0628"
	if _, err := decoder.DecodeHostname(host); err != nil {
		t.Fatalf("DecodeHostname rejected valid ZWNJ context: %v", err)
	}
}

func TestDecodeLabel(t *testing.T) {
	const label = "\u0915\u094d\u200d\u0937"
	got, err := decoder.DecodeLabel(label)
	if err != nil {
		t.Fatalf("DecodeLabel rejected valid ZWJ context: %v", err)
	}
	if got != label {
		t.Fatalf("DecodeLabel = %q, want %q", got, label)
	}
}
