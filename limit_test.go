package netsec

import (
	"errors"
	"strings"
	"testing"
)

// buildWrapResponse builds a plugin wrap-file-key response whose wire length
// is exactly wantBytes. It uses a valid recipient-stanza header whose first
// argument matches the session recipient and fills the body with base64
// padding-free data ('A' encodes to three zero bytes per four chars).
func buildWrapResponse(t *testing.T, wantBytes int) string {
	t.Helper()
	header := "recipient-stanza " + testRecipientType + "\n"
	terminator := mustWrite(&Stanza{Type: "done"})
	base := len(header) + len(terminator)
	if wantBytes < base {
		t.Fatalf("cannot build %d-byte response, overhead alone is %d", wantBytes, base)
	}

	// Body lines cost len(line)+1 bytes each and the blank stanza separator
	// adds one more byte. Search for a partition whose total is exact.
	for columns := 0; columns <= wantBytes/65+1; columns++ {
		remainder := wantBytes - base - 65*columns - 1
		if remainder < 0 || remainder == 1 || (remainder > 1 && (remainder-1)%4 != 0) {
			continue
		}
		var builder strings.Builder
		builder.WriteString(header)
		for i := 0; i < columns; i++ {
			builder.WriteString(strings.Repeat("A", 64))
			builder.WriteByte('\n')
		}
		if remainder > 0 {
			lineLen := remainder - 1
			builder.WriteString(strings.Repeat("A", lineLen))
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
		builder.WriteString(mustWrite(&Stanza{Type: "done"}))
		response := builder.String()
		if len(response) != wantBytes {
			t.Fatalf("internal: built response of %d bytes, want %d", len(response), wantBytes)
		}
		return response
	}
	t.Fatalf("internal: could not partition %d bytes into base64 body lines", wantBytes)
	return ""
}

func TestWrapFileKeyAcceptsExactly64KiB(t *testing.T) {
	response := buildWrapResponse(t, MaxPluginResponse)
	session, _ := fakeHost(t, greetStanza()+response)
	results, err := session.WrapFileKey(testFileKey)
	if err != nil {
		t.Fatalf("WrapFileKey rejected a %d-byte response: %v", MaxPluginResponse, err)
	}
	if len(results) != 1 || results[0].Args[0] != testRecipientType {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestWrapFileKeyRejectsOver64KiB(t *testing.T) {
	for _, overflow := range []int{1, 64, 4096} {
		response := buildWrapResponse(t, MaxPluginResponse+overflow)
		session, _ := fakeHost(t, greetStanza()+response)
		_, err := session.WrapFileKey(testFileKey)
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("overflow=%d: error = %v, want ErrResponseTooLarge", overflow, err)
		}
	}
}

func TestLimitAppliesPerResponse(t *testing.T) {
	// Two responses that each fit, but whose concatenation exceeds the budget,
	// must both succeed because the budget resets per command response.
	first := buildWrapResponse(t, MaxPluginResponse)
	second := buildWrapResponse(t, MaxPluginResponse)

	transcript := greetStanza() + first + second
	session, _ := fakeHost(t, transcript)

	if _, err := session.WrapFileKey(testFileKey); err != nil {
		t.Fatalf("first response: %v", err)
	}
	if _, err := session.WrapFileKey(testFileKey); err != nil {
		t.Fatalf("second response: %v", err)
	}
}

func TestHandshakeRejectsBadVersion(t *testing.T) {
	bad := mustWrite(&Stanza{Type: "greet", Args: []string{"v2"}})
	in := strings.NewReader(bad)
	if _, err := NewSession(in, &strings.Builder{}, testRecipientType); !errors.Is(err, ErrProtocol) {
		t.Fatalf("error = %v, want ErrProtocol", err)
	}
}

func TestHandshakeIgnoresUnknownLeadingStanzas(t *testing.T) {
	transcript := mustWrite(&Stanza{Type: "future-greet-extension", Args: []string{"x"}}) +
		greetStanza()
	session, _ := fakeHost(t, transcript)
	if session.BinaryName() != "age-plugin-test" {
		t.Fatalf("BinaryName = %q", session.BinaryName())
	}
}

func TestHandshakeRejectsPluginError(t *testing.T) {
	transcript := mustWrite(&Stanza{Type: "error", Args: []string{"refusing"}})
	in := strings.NewReader(transcript)
	_, err := NewSession(in, &strings.Builder{}, testRecipientType)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("error = %v, want ErrProtocol", err)
	}
}
