package netsec

import "testing"

func TestZWNJBetweenLatinLettersIsRejected(t *testing.T) {
	inputs := []string{
		"a\u200cb",
		"example\u200ccom",
		"a\u200cb.example",
	}
	for _, input := range inputs {
		if err := Validate(input); err == nil {
			t.Errorf("Validate(%q) succeeded, want ContextJ failure", input)
		}
		if _, err := ToASCII(input); err == nil {
			t.Errorf("ToASCII(%q) succeeded, want ContextJ failure", input)
		}
	}
}

func TestZWJBetweenLatinLettersIsRejected(t *testing.T) {
	if err := Validate("a\u200db"); err == nil {
		t.Fatal("Validate accepted ZWJ between Latin letters")
	}
}

func TestZWNJContexts(t *testing.T) {
	valid := []string{
		"\u0628\u200c\u0628",
		"\u0628\u064e\u200c\u0628",
		"\u0915\u094d\u200c\u0937",
	}
	invalid := []string{
		"\u0628\u200c",
		"\u200c\u0628",
		"\u0628\u200c" + "a",
		"a\u200c" + "\u0628",
		"\u0628\u200c\u200c\u0628",
	}
	for _, label := range valid {
		if err := CheckJoinerContext(label); err != nil {
			t.Errorf("CheckJoinerContext(%q) = %v, want nil", label, err)
		}
	}
	for _, label := range invalid {
		if err := CheckJoinerContext(label); err == nil {
			t.Errorf("CheckJoinerContext(%q) = nil, want ContextJ failure", label)
		}
	}
}

func TestZWJContext(t *testing.T) {
	if err := CheckJoinerContext("\u0915\u094d\u200d\u0937"); err != nil {
		t.Fatalf("ZWJ after virama rejected: %v", err)
	}
	for _, label := range []string{"\u0628\u200d\u0628", "\u0915\u200d\u0937", "a\u200db"} {
		if err := CheckJoinerContext(label); err == nil {
			t.Errorf("CheckJoinerContext(%q) accepted ZWJ outside virama context", label)
		}
	}
}

func TestValidJoinersArePreserved(t *testing.T) {
	labels := []string{
		"\u0628\u200c\u0628",
		"\u0915\u094d\u200d\u0937",
	}
	for _, label := range labels {
		ascii, err := ToASCII(label)
		if err != nil {
			t.Fatalf("ToASCII(%q): %v", label, err)
		}
		unicodeLabel, err := ToUnicode(ascii)
		if err != nil {
			t.Fatalf("ToUnicode(%q): %v", ascii, err)
		}
		if unicodeLabel != label {
			t.Errorf("round trip = %q, want %q", unicodeLabel, label)
		}
	}
}

func TestRegistrationRejectsEncodedInvalidJoiner(t *testing.T) {
	ascii, err := Punycode.ToASCII("a\u200cb")
	if err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	if err := Registration.Validate(ascii); err == nil {
		t.Fatalf("Registration accepted encoded label %q containing invalid ZWNJ", ascii)
	}
	if _, err := DecodeHostname(ascii); err == nil {
		t.Fatalf("DecodeHostname accepted encoded label %q containing invalid ZWNJ", ascii)
	}
}

func TestPunycodeRoundTrip(t *testing.T) {
	const label = "bücher"
	encoded, err := PunycodeEncode(label)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != "bcher-kva" {
		t.Fatalf("PunycodeEncode(%q) = %q, want bcher-kva", label, encoded)
	}
	decoded, err := PunycodeDecode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != label {
		t.Fatalf("PunycodeDecode(%q) = %q, want %q", encoded, decoded, label)
	}
}
