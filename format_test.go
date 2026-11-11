package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Secrets are long, distinctive and unlikely to collide with legitimate output.
var (
	secretAPIKey = "AKIA-SECRET-7f3a9c21e8b4d6f0a5c8e2d1b9a7f4c3"
	secretToken  = "zk9Qm2Lp7Vr4Ts8Wn3Yh6Kd1Fg5Jc0BeAeUvX"
	secretPem    = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhki\nSECRET-LINE-2\n-----END PRIVATE KEY-----"
	allSecrets   = []string{secretAPIKey, secretToken, secretPem}
)

// assertNoSecretPrefix fails if any prefix of a secret of length >=
// minPrefixLen appears in out. The full secret is included in the check, so
// this catches both partial (unmatched-prefix) and full leaks.
func assertNoSecretPrefix(t *testing.T, out string, minPrefixLen int) {
	t.Helper()
	for _, secret := range allSecrets {
		for n := minPrefixLen; n <= len(secret); n++ {
			prefix := secret[:n]
			if strings.Contains(out, prefix) {
				t.Fatalf("secret prefix of length %d leaked into output %q (prefix %q)", n, out, prefix)
			}
		}
	}
}

func TestSensitiveValueFullyRedacted(t *testing.T) {
	out, err := FormatString("key=%s token=%v", []Arg{
		{Sensitive: true, Value: secretAPIKey},
		{Sensitive: true, Value: secretToken},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "key=[REDACTED] token=[REDACTED]" {
		t.Fatalf("unexpected output %q", out)
	}
	assertNoSecretPrefix(t, out, 3)
}

func TestSensitiveMixedWithNonSensitive(t *testing.T) {
	out, err := FormatString("user=%s key=%q n=%d", []Arg{
		{Value: "alice"},
		{Sensitive: true, Value: secretAPIKey},
		{Value: 42},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `user=alice key="[REDACTED]" n=42` {
		t.Fatalf("unexpected output %q", out)
	}
	assertNoSecretPrefix(t, out, 3)
	if !strings.Contains(out, "alice") {
		t.Fatalf("non-sensitive value missing from %q", out)
	}
}

func TestWidthAndPrecisionNeverLeak(t *testing.T) {
	formats := []string{
		"%s", "%10s", "%-12s", "%012s", "%.5s", "%8.3s",
		"%q", "%.4q", "%10.6s", "%v", "%x", "%X",
	}
	for _, f := range formats {
		out, err := FormatString(f, []Arg{{Sensitive: true, Value: secretToken}})
		if err != nil {
			t.Fatalf("format %q: unexpected error: %v", f, err)
		}
		assertNoSecretPrefix(t, out, 3)
		if strings.ContainsAny(out, "zk9") {
			t.Fatalf("format %q emitted raw secret characters: %q", f, out)
		}
	}

	// Precision truncates the token, never the secret.
	out, err := FormatString("%.4s", []Arg{{Sensitive: true, Value: secretToken}})
	if err != nil || out != "[RED" {
		t.Fatalf("got %q, %v", out, err)
	}

	// Width pads the token.
	out, err = FormatString("%15s", []Arg{{Sensitive: true, Value: secretToken}})
	if err != nil || out != "     [REDACTED]" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestRepeatedAndExplicitIndexParameters(t *testing.T) {
	// Same sensitive position used twice via explicit indexes.
	out, err := FormatString("a=%[1]s b=%[1]s c=%[2]d", []Arg{
		{Sensitive: true, Value: secretAPIKey},
		{Value: 7},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "a=[REDACTED] b=[REDACTED] c=7"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
	assertNoSecretPrefix(t, out, 3)

	// Repeated implicit position is not valid Go syntax; explicit [n] is the
	// supported way. An indexed star width must resolve a non-sensitive int.
	out, err = FormatString("w=%[1]*[2]s", []Arg{
		{Value: 13},
		{Sensitive: true, Value: secretToken},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "w=   [REDACTED]" {
		t.Fatalf("unexpected output %q", out)
	}
	assertNoSecretPrefix(t, out, 3)
}

func TestImplicitStarWidth(t *testing.T) {
	out, err := FormatString("pad=%*s", []Arg{
		{Value: 12},
		{Sensitive: true, Value: secretAPIKey},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "pad=  [REDACTED]" {
		t.Fatalf("unexpected output %q", out)
	}
	assertNoSecretPrefix(t, out, 3)

	if _, err := FormatString("%*s", []Arg{
		{Sensitive: true, Value: 5},
		{Sensitive: true, Value: secretAPIKey},
	}); err == nil || !errors.Is(err, ErrFormat) {
		t.Fatalf("expected ErrFormat for sensitive star, got %v", err)
	}
}

func TestBudgetTruncatesNonSensitiveByRune(t *testing.T) {
	text := "héllo世界" // 2+1+1+1+3+3 = 11 bytes, 7 runes
	out, err := FormatString("x=%s", []Arg{{Value: text}}, WithBudget(9))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "x=" (2) + "héllo世" (8 bytes) = 10 exceeds 9; marker "..." reserved.
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8: %q", out)
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("expected truncation marker, got %q", out)
	}
	if len(out) > 9 {
		t.Fatalf("output length %d exceeds budget 9: %q", len(out), out)
	}
}

func TestBudgetAppliedAfterRedaction(t *testing.T) {
	// A huge secret under a tiny budget still cannot surface a prefix.
	for _, budget := range []int{1, 2, 3, 5, 8, 20} {
		out, err := FormatString("k=%s", []Arg{{Sensitive: true, Value: secretPem}}, WithBudget(budget))
		if err != nil {
			t.Fatalf("budget %d: unexpected error: %v", budget, err)
		}
		if len(out) > budget {
			t.Fatalf("budget %d exceeded: %q (%d bytes)", budget, out, len(out))
		}
		assertNoSecretPrefix(t, out, 3)
	}

	// Budget larger than the redacted token simply prints the token fully.
	out, err := FormatString("%s", []Arg{{Sensitive: true, Value: secretPem}}, WithBudget(100))
	if err != nil || out != "[REDACTED]" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestTruncationMarkerBudget(t *testing.T) {
	// Zero budget writes nothing.
	out, err := FormatString("%s", []Arg{{Value: "abcdefghij"}}, WithBudget(0))
	if err != nil || out != "" {
		t.Fatalf("got %q, %v", out, err)
	}

	// Budget smaller than the marker: marker is shortened, content rune-safe.
	out, err = FormatString("%s", []Arg{{Value: "abcdefghij"}}, WithBudget(2))
	if err != nil || len(out) > 2 || !utf8.ValidString(out) {
		t.Fatalf("got %q, %v", out, err)
	}

	// Custom marker is itself bounded.
	out, err = FormatString("%s", []Arg{{Value: "abcdefghijklm"}},
		WithBudget(8), WithTruncationMarker("…"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(out, "…") || len(out) > 8 || !utf8.ValidString(out) {
		t.Fatalf("unexpected bounded marker output %q", out)
	}

	// Content that fits needs no marker.
	out, err = FormatString("%s", []Arg{{Value: "ab"}}, WithBudget(10))
	if err != nil || out != "ab" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestMalformedTemplatesNeverUseRawArgs(t *testing.T) {
	bad := []string{
		"trailing %",
		"verb %y",
		"star %*s",
		"bad %[s",
		"bad %[0]s",
		"dup %00s",
		"idx %[9]s",
	}
	args := []Arg{
		{Sensitive: true, Value: secretAPIKey},
		{Sensitive: true, Value: secretToken},
		{Value: 1},
	}
	for _, f := range bad {
		out, err := FormatString(f, args)
		if err == nil {
			t.Fatalf("template %q expected error", f)
		}
		var fe *FormatError
		if !errors.As(err, &fe) || !errors.Is(err, ErrFormat) {
			t.Fatalf("template %q: expected FormatError/ErrFormat, got %T %v", f, err, err)
		}
		assertNoSecretPrefix(t, out, 3)
		// The output must be a safe marker (possibly empty under no budget).
		if out != "" && !strings.Contains(out, "FORMAT_ERROR") {
			t.Fatalf("template %q emitted non-marker output %q", f, out)
		}
	}

	// Error marker is itself budget bounded and never leaks.
	out, err := FormatString("bad %y", args, WithBudget(5))
	if err == nil {
		t.Fatalf("expected error")
	}
	if len(out) > 5 || strings.Contains(out, secretAPIKey[:4]) {
		t.Fatalf("unexpected bounded error output %q", out)
	}
}

func TestNonSensitiveParityWithFmt(t *testing.T) {
	cases := []struct {
		format string
		args   []Arg
	}{
		{"%s-%d-%x", []Arg{{Value: "ab"}, {Value: 9}, {Value: 255}}},
		{"%8.3f", []Arg{{Value: 3.14159}}},
		{"%-7q", []Arg{{Value: "hi"}}},
		{"%[2]s %[1]d", []Arg{{Value: 1}, {Value: "two"}}},
		{"%t %v %o", []Arg{{Value: true}, {Value: []int{1, 2}}, {Value: 8}}},
		{"%.4s", []Arg{{Value: "héllo世界"}}},
		{"%8.4s", []Arg{{Value: "日本語テキスト"}}},
	}
	for _, tc := range cases {
		got, err := FormatString(tc.format, tc.args)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", tc.format, err)
		}
		raw := make([]any, len(tc.args))
		for i, a := range tc.args {
			raw[i] = a.Value
		}
		want := fmt.Sprintf(tc.format, raw...)
		if got != want {
			t.Fatalf("%q: got %q want %q", tc.format, got, want)
		}
	}
}

func TestStreamingWriterAndByteCount(t *testing.T) {
	var buf bytes.Buffer
	args := []Arg{
		{Value: "p-"},
		{Sensitive: true, Value: secretAPIKey},
	}
	n, err := Format(&buf, "%s%s", args, WithBudget(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != buf.Len() || n != len("p-[REDACTED]") {
		t.Fatalf("reported %d bytes, buffer has %q", n, buf.String())
	}
	assertNoSecretPrefix(t, buf.String(), 3)

	b, err := FormatBytes("%s", []Arg{{Sensitive: true, Value: secretToken}})
	if err != nil || string(b) != "[REDACTED]" {
		t.Fatalf("FormatBytes got %q, %v", b, err)
	}
}

func TestCustomRedactionToken(t *testing.T) {
	out, err := FormatString("%s", []Arg{{Sensitive: true, Value: secretAPIKey}},
		WithRedactionToken("<hidden>"))
	if err != nil || out != "<hidden>" {
		t.Fatalf("got %q, %v", out, err)
	}
	assertNoSecretPrefix(t, out, 3)
}

func TestEscapedPercent(t *testing.T) {
	out, err := FormatString("100%% done: %s", []Arg{{Sensitive: true, Value: secretToken}})
	if err != nil || out != "100% done: [REDACTED]" {
		t.Fatalf("got %q, %v", out, err)
	}
	assertNoSecretPrefix(t, out, 3)
}

func TestBudgetAcrossSegments(t *testing.T) {
	// The budget spans literals and several verbs; exhaustion must not split a
	// rune and must leave exactly one trailing marker.
	args := []Arg{
		{Value: "aaa"},
		{Value: "béb"},
		{Sensitive: true, Value: secretAPIKey},
	}
	out, err := FormatString("%s/%s/%s", args, WithBudget(10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > 10 || !utf8.ValidString(out) {
		t.Fatalf("budget/UTF-8 violation: %q (%d bytes)", out, len(out))
	}
	if strings.Count(out, "...") != 1 || !strings.HasSuffix(out, "...") {
		t.Fatalf("expected exactly one trailing marker, got %q", out)
	}
	assertNoSecretPrefix(t, out, 3)
}

func TestInvalidUTF8IsRuneSafe(t *testing.T) {
	// Range over a string maps invalid bytes to RuneError; truncation must
	// never emit a partial multi-byte sequence.
	bad := "abc\xff\xf8\x80def"
	out, err := FormatString("%s", []Arg{{Value: bad}}, WithBudget(6))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("output contains invalid UTF-8: %q", out)
	}
}

func TestUnlimitedBudgetAndUnusedArgs(t *testing.T) {
	// Without WithBudget output is unbounded; unused args are harmless.
	out, err := FormatString("only=%s", []Arg{
		{Value: "x"},
		{Sensitive: true, Value: secretAPIKey},
	})
	if err != nil || out != "only=x" {
		t.Fatalf("got %q, %v", out, err)
	}

	long := strings.Repeat("z", 4096)
	out, err = FormatString("%s", []Arg{{Value: long}})
	if err != nil || out != long {
		t.Fatalf("unbounded output mismatch: %v", err)
	}
}

func TestSensitiveNumericAndSliceTypes(t *testing.T) {
	// Numeric/collection verbs on a secret still render only the token.
	for _, f := range []string{"%d", "%f", "%t", "%v", "%#v", "%T"} {
		out, err := FormatString(f, []Arg{{Sensitive: true, Value: secretPem}})
		if err != nil {
			t.Fatalf("format %q: %v", f, err)
		}
		assertNoSecretPrefix(t, out, 3)
		if !strings.Contains(out, "REDACTED") {
			t.Fatalf("format %q did not redact: %q", f, out)
		}
	}
}

func TestEveryErrorPathIsPrefixFree(t *testing.T) {
	// Exercise every malformed shape with a secret present and assert the
	// strongest prefix bound (length 3) on the produced bytes.
	templates := []string{
		"%", "%[", "%[1", "%[1]", "%[0]s", "%[]s", "%[a]s",
		"%00s", "%y", "%.", "%*[", "%[1]*[", "%[1]*%[2]s",
		"%[1]*s", "%[1].*s",
	}
	for _, f := range templates {
		out, err := FormatString(f, []Arg{
			{Sensitive: true, Value: secretAPIKey},
			{Sensitive: true, Value: secretToken},
		}, WithBudget(64))
		if err == nil {
			t.Fatalf("template %q should fail, got %q", f, out)
		}
		assertNoSecretPrefix(t, out, 3)
	}
}

func TestNilAndEmptySensitive(t *testing.T) {
	out, err := FormatString("%s|%v", []Arg{
		{Sensitive: true, Value: nil},
		{Sensitive: true, Value: ""},
	})
	if err != nil || out != "[REDACTED]|[REDACTED]" {
		t.Fatalf("got %q, %v", out, err)
	}
}
