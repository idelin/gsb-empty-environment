package netsec

import (
	"errors"
	"net"
	"testing"
)

func testIE(id byte, body []byte) []byte {
	return append([]byte{id, byte(len(body))}, body...)
}

func rsnIEWithAKM(akm byte) []byte {
	body := []byte{
		1, 0, // RSN version 1
		0x00, 0x0f, 0xac, 0x04, // group cipher: CCMP
		1, 0, // one pairwise suite
		0x00, 0x0f, 0xac, 0x04, // pairwise: CCMP
		1, 0, // one AKM suite
		0x00, 0x0f, 0xac, akm, // AKM suite
	}
	return testIE(rsnElementID, body)
}

func TestTransitionElementRoundTrip(t *testing.T) {
	want := TransitionMode{BSSID: oweBSSID, SSID: "hidden-owe"}
	element, err := BuildTransitionElement(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseTransitionElement(element)
	if err != nil {
		t.Fatal(err)
	}
	if !sameBSSID(got.BSSID, want.BSSID) || got.SSID != want.SSID {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
}

func TestParseTransitionIEsAmongOtherElements(t *testing.T) {
	transition, err := BuildTransitionElement(TransitionMode{BSSID: oweBSSID, SSID: "hidden-owe"})
	if err != nil {
		t.Fatal(err)
	}
	ies := append(testIE(0, []byte("example-ssid")), transition...)
	ies = append(ies, testIE(rsnElementID, []byte{1, 0})...)

	got, err := ParseTransitionIEs(ies)
	if err != nil {
		t.Fatal(err)
	}
	if !sameBSSID(got.BSSID, oweBSSID) || got.SSID != "hidden-owe" {
		t.Fatalf("parsed %+v", got)
	}
}

func TestParseTransitionIgnoresUnrelatedVendorElement(t *testing.T) {
	vendor := testIE(vendorSpecificElementID, []byte{0x00, 0x11, 0x22, 0x01, 0xaa})
	_, err := ParseTransitionIEs(vendor)
	if !errors.Is(err, ErrNoTransitionElement) {
		t.Fatalf("error = %v, want ErrNoTransitionElement", err)
	}
}

func TestParseMalformedTransitionPayload(t *testing.T) {
	element := []byte{221, 8, 0x50, 0x6f, 0x9a, 0x1c, 0x02, 0x00, 0x00, 0x00}
	_, err := ParseTransitionElement(element)
	if !errors.Is(err, ErrMalformedTransitionElement) {
		t.Fatalf("error = %v, want ErrMalformedTransitionElement", err)
	}
}

func TestParseAmbiguousTransitionElements(t *testing.T) {
	first, err := BuildTransitionElement(TransitionMode{BSSID: oweBSSID, SSID: "owe-one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildTransitionElement(TransitionMode{BSSID: otherBSSID, SSID: "owe-two"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseTransitionIEs(append(first, second...))
	if !errors.Is(err, ErrAmbiguousTransitionElement) {
		t.Fatalf("error = %v, want ErrAmbiguousTransitionElement", err)
	}
}

func TestParseMalformedIEStream(t *testing.T) {
	_, err := ParseTransitionIEs([]byte{0, 5, 1, 2})
	if !errors.Is(err, ErrMalformedIEs) {
		t.Fatalf("error = %v, want ErrMalformedIEs", err)
	}
}

func TestInferSecurity(t *testing.T) {
	if got := InferSecurity(testIE(0, []byte("ssid"))); got != SecurityOpen {
		t.Fatalf("no RSN element inferred as %s, want open", got)
	}
	if got := InferSecurity(rsnIEWithAKM(oweAKMSuiteType)); got != SecurityOWE {
		t.Fatalf("OWE RSN inferred as %s, want owe", got)
	}
	if got := InferSecurity(rsnIEWithAKM(8)); got != SecurityUnknown {
		t.Fatalf("non-OWE RSN inferred as %s, want unknown", got)
	}
}

func TestBuildTransitionElementRejectsInvalidTarget(t *testing.T) {
	if _, err := BuildTransitionElement(TransitionMode{BSSID: testMAC("00:00:00:00:00:00"), SSID: "x"}); err == nil {
		t.Fatal("all-zero BSSID accepted")
	}
	if _, err := BuildTransitionElement(TransitionMode{BSSID: oweBSSID, SSID: ""}); err == nil {
		t.Fatal("empty target SSID accepted")
	}
}

func FuzzParseTransitionIEs(f *testing.F) {
	valid, err := BuildTransitionElement(TransitionMode{BSSID: oweBSSID, SSID: "owe-net"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte{221, 8, 0x50, 0x6f, 0x9a, 0x1c, 0x02, 0x00, 0x00, 0x00})
	f.Add([]byte{0, 5, 1, 2})

	f.Fuzz(func(t *testing.T, ies []byte) {
		_, _ = ParseTransitionIEs(ies)
		_ = InferSecurity(ies)
	})
}

func FuzzSelect(f *testing.F) {
	f.Add([]byte(openBSSID), []byte(oweBSSID), uint8(SecurityOpen), uint8(SecurityOWE))
	f.Fuzz(func(t *testing.T, openRaw, oweRaw []byte, openSecurity, oweSecurity uint8) {
		scan := []BSS{
			{
				BSSID:    net.HardwareAddr(openRaw),
				SSID:     "open-net",
				Security: Security(openSecurity % 3),
			},
			{
				BSSID:    net.HardwareAddr(oweRaw),
				SSID:     "owe-net",
				Security: Security(oweSecurity % 3),
			},
		}
		_ = Select(scan, Policy{})
		_ = Select(scan, Policy{AllowOpenFallback: true})
	})
}
