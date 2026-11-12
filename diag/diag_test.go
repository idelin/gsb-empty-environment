package diag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func strip(rendered string) string {
	return strings.ReplaceAll(rendered, Marker, "")
}

func TestRenderRedactsSecretAndKeepsNeighbors(t *testing.T) {
	input := []byte("token=hunter2; user=ada")
	secrets := []Secret{{Interval: Interval{6, 13}, Label: "password"}}
	authorized := []Interval{{0, len(input)}}

	e := NewError("auth", "bad credential", Interval{6, 7}, secrets, authorized)
	got := e.Render(input)

	if strings.Contains(got, "hunter2") {
		t.Fatalf("secret bytes leaked: %q", got)
	}
	if strings.Contains(got, "password") {
		t.Fatalf("sensitive label leaked: %q", got)
	}
	if !strings.Contains(got, Marker) {
		t.Fatalf("marker missing: %q", got)
	}
	if !strings.Contains(got, "token=") || !strings.Contains(got, "; user=ada") {
		t.Fatalf("authorized context truncated: %q", got)
	}
	if strings.Count(got, Marker) != 1 {
		t.Fatalf("want exactly one merged marker, got %d in %q", strings.Count(got, Marker), got)
	}
}

func TestOverlappingSecretsProduceOneFixedMarker(t *testing.T) {
	input := []byte("prefixABCDEFGHsuffix")
	secrets := []Secret{
		{Interval: Interval{6, 10}, Label: "a"},
		{Interval: Interval{8, 14}, Label: "b"},
		{Interval: Interval{13, 14}, Label: "c"},
	}
	authorized := []Interval{{0, len(input)}}

	e := NewError("f", "m", Interval{6, 7}, secrets, authorized)
	got := e.Render(input)

	for _, secret := range []string{"ABC", "EFG", "DEFGH"} {
		if strings.Contains(got, secret) {
			t.Fatalf("overlapping secret leaked %q: %q", secret, got)
		}
	}
	if strings.Count(got, Marker) != 1 {
		t.Fatalf("overlapping secrets must collapse to one marker, got %q", got)
	}
	if strings.Contains(got, "|") {
		t.Fatalf("merged label metadata rendered: %q", got)
	}
}

func TestLengthIndistinguishableAcrossSecretSizes(t *testing.T) {
	build := func(secretSize int) (input []byte, failure Interval, secret Interval) {
		input = append(input, []byte("head-")...)
		start := len(input)
		input = append(input, bytesRepeat('S', secretSize)...)
		secret = Interval{start, len(input)}
		// Identical visible neighbors; only the secret span changes size.
		// Visible-window budgeting must make both renders indistinguishable.
		input = append(input, []byte("-tail")...)
		failure = Interval{start, start + 1}
		return input, failure, secret
	}

	longInput, longFailure, longSecret := build(22)
	shortInput, shortFailure, shortSecret := build(1)
	fullAuth := func(n int) []Interval { return []Interval{{0, n}} }

	long := NewError("f", "m", longFailure,
		[]Secret{{Interval: longSecret, Label: "long"}}, fullAuth(len(longInput))).Render(longInput)
	short := NewError("f", "m", shortFailure,
		[]Secret{{Interval: shortSecret, Label: "short"}}, fullAuth(len(shortInput))).Render(shortInput)

	if strings.Count(long, Marker) != strings.Count(short, Marker) {
		t.Fatalf("marker count depends on secret length:\n%s\n%s", long, short)
	}
	if strip(long) != strip(short) {
		t.Fatalf("visible context reveals secret length:\n%q\n%q", strip(long), strip(short))
	}
}

func TestRuneIsNeverSplitAtWindowEdge(t *testing.T) {
	// The 2-byte rune "é" sits exactly where a raw-byte context window
	// would have clipped it (16 raw bytes in). Expansion walks whole
	// atomic units, so the rune can never appear half-emitted.
	input := append(bytesRepeat('a', 16), 0xc3, 0xa9)
	input = append(input, bytesRepeat('b', 20)...)
	if input[16] != 0xc3 || input[17] != 0xa9 {
		t.Fatalf("fixture broken: % x", input)
	}
	failure := Interval{Start: 18, End: 19}
	e := NewError("f", "m", failure, nil, []Interval{{0, len(input)}})

	got := strip(e.Render(input))

	if !utf8.ValidString(got) {
		t.Fatalf("emitted context is not valid UTF-8: %q (% x)", got, got)
	}
	if strings.ContainsRune(got, 0xc3) || strings.ContainsRune(got, 0xa9) {
		t.Fatalf("half rune emitted: % x", got)
	}
}

func TestInvalidUTF8ByteIsAtomic(t *testing.T) {
	input := []byte{'a', 'b', 0xff, 'c', 'd'}
	authorized := []Interval{{0, len(input)}}

	e := NewError("f", "m", Interval{2, 3}, nil, authorized)
	got := strip(e.Render(input))

	// 0xff must be passed through as a raw byte, never replaced by U+FFFD.
	if strings.Contains(got, string(utf8.RuneError)) {
		t.Fatalf("invalid byte was replaced by U+FFFD: %q", got)
	}
	if strings.Count(got, "\xff") != 1 {
		t.Fatalf("invalid byte altered or duplicated: %q", got)
	}

	secretErr := NewError("f", "m", Interval{2, 3},
		[]Secret{{Interval: Interval{2, 3}, Label: "badbyte"}}, authorized)
	hidden := secretErr.Render(input)
	if strings.Contains(hidden, "\xff") {
		t.Fatalf("sensitive invalid byte leaked: %q", hidden)
	}
	if strings.Count(hidden, Marker) != 1 {
		t.Fatalf("invalid secret must yield one marker: %q", hidden)
	}
}

func TestAuthorizationClippingOmitsUnauthorizedBytes(t *testing.T) {
	input := []byte("AAA-public-BBB")
	// Only "public" (4..10) is authorized; flanking regions vanish without
	// even a marker, so their size cannot be measured.
	authorized := []Interval{{4, 10}}

	e := NewError("f", "m", Interval{7, 8}, nil, authorized)
	got := e.Render(input)

	if strings.Contains(got, "AAA") || strings.Contains(got, "BBB") {
		t.Fatalf("unauthorized bytes leaked: %q", got)
	}
	if !strings.Contains(got, "public") {
		t.Fatalf("authorized span missing: %q", got)
	}
	if strings.Contains(got, Marker) {
		t.Fatalf("omission must not emit a marker: %q", got)
	}
}

func TestRuneStraddlingAuthorizationGapIsHiddenWhole(t *testing.T) {
	// 2-byte rune "é" occupies [2,4); authorization covers [0,3) and [4,6),
	// so neither interval fully contains the rune.
	input := []byte{'a', 'b', 0xc3, 0xa9, 'c', 'd'}
	authorized := []Interval{{0, 3}, {4, 6}}

	e := NewError("f", "m", Interval{4, 5}, nil, authorized)
	got := strip(e.Render(input))

	if strings.Contains(got, "é") {
		t.Fatalf("rune crossing authorization gap leaked whole: %q", got)
	}
	if strings.Contains(got, "\xc3") || strings.Contains(got, "\xa9") {
		t.Fatalf("half of a split rune leaked: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("emitted context invalid UTF-8: %q", got)
	}
}

func TestRenderAllSharesSecretsAcrossErrors(t *testing.T) {
	input := []byte("key=ssss;ok=1;x=2")
	fullAuth := []Interval{{0, len(input)}}

	// First error declares the secret; the second does not but its context
	// window reaches into the same region.
	first := NewError("k", "bad key", Interval{4, 5},
		[]Secret{{Interval: Interval{4, 8}, Label: "key-material"}}, fullAuth)
	second := NewError("x", "bad x", Interval{9, 10}, nil, fullAuth)

	combined := RenderAll(input, []*Error{first, second})

	if strings.Contains(combined, "ssss") {
		t.Fatalf("secret leaked across merged diagnostics: %q", combined)
	}
	if strings.Contains(combined, "key-material") {
		t.Fatalf("label leaked across merged diagnostics: %q", combined)
	}

	// Both windows cover the span, so both blocks must redact via the union
	// of secrets, even though second never declared the secret itself.
	blocks := strings.Split(combined, "\n")
	markerSeen := 0
	for _, block := range blocks {
		if strings.Contains(block, "key=") {
			if !strings.Contains(block, Marker) {
				t.Fatalf("block touching secret lacks marker: %q", block)
			}
			markerSeen++
		}
	}
	if markerSeen != 2 {
		t.Fatalf("both blocks touching the secret need a marker, got %d: %q", markerSeen, combined)
	}

	// Rendering second alone has no knowledge of the secret and shows the
	// raw bytes; RenderAll must be strictly safer.
	alone := second.Render(input)
	if !strings.Contains(alone, "ssss") {
		t.Fatalf("standalone render fixture changed: %q", alone)
	}
}

func TestMarkerIsFixedConstantAndLengthSafe(t *testing.T) {
	if Marker == "" {
		t.Fatal("marker must be non-empty")
	}
	if utf8.RuneCountInString(Marker) != len(Marker) {
		t.Fatal("marker should be ASCII for a stable byte budget")
	}
	if len(Marker) != 10 {
		t.Fatalf("marker length drifted to %d", len(Marker))
	}

	base := []byte("0123456789abcdef")
	auth := []Interval{{0, len(base)}}
	one := NewError("f", "m", Interval{5, 6},
		[]Secret{{Interval: Interval{5, 6}, Label: "x"}}, auth).Render(base)
	two := NewError("f", "m", Interval{5, 6},
		[]Secret{{Interval: Interval{5, 6}, Label: "different-label"}}, auth).Render(base)
	if len(one) != len(two) {
		t.Fatalf("label metadata changed rendered length: %d vs %d", len(one), len(two))
	}

	// A one-byte secret and a five-byte secret contribute the same marker
	// count and marker byte budget: no length channel for hidden size.
	small := NewError("f", "m", Interval{5, 6},
		[]Secret{{Interval: Interval{5, 6}, Label: "x"}}, auth).Render(base)
	large := NewError("f", "m", Interval{5, 6},
		[]Secret{{Interval: Interval{5, 10}, Label: "y"}}, auth).Render(base)
	if strings.Count(small, Marker) != strings.Count(large, Marker) {
		t.Fatalf("marker count varies with secret size")
	}
	if strings.Count(small, Marker)*len(Marker) != strings.Count(large, Marker)*len(Marker) {
		t.Fatal("redacted byte budget varies with secret size")
	}
}

func TestLabelsStayOnStructuredAPI(t *testing.T) {
	input := []byte("public-secret-public")
	secrets := []Secret{{Interval: Interval{7, 13}, Label: "api-key"}}
	e := NewError("field", "msg", Interval{7, 8}, secrets, []Interval{{0, len(input)}})

	if got := e.Error(); got != "field: msg" {
		t.Fatalf("Error() must stay context-free, got %q", got)
	}
	if len(e.Secrets) != 1 || e.Secrets[0].Label != "api-key" {
		t.Fatalf("structured label not retained for host: %+v", e.Secrets)
	}
	rendered := e.Render(input)
	if strings.Contains(rendered, "api-key") {
		t.Fatalf("label must never render: %q", rendered)
	}

	secrets[0].Label = "mutated"
	if e.Secrets[0].Label != "api-key" {
		t.Fatal("diagnostic aliases caller secret slice")
	}
}

func TestMultiErrorIsContextFree(t *testing.T) {
	input := []byte("secret-bytes-here")
	auth := []Interval{{0, len(input)}}
	merr := MultiError{
		NewError("a", "first", Interval{0, 1},
			[]Secret{{Interval: Interval{0, 6}, Label: "s"}}, auth),
		NewError("b", "second", Interval{8, 9}, nil, auth),
	}

	var asError error = merr
	text := asError.Error()
	if strings.Contains(text, "secret") || strings.Contains(text, string(input)) {
		t.Fatalf("MultiError leaked input: %q", text)
	}
	if strings.Count(text, "\n") != 1 {
		t.Fatalf("want two lines, got %q", text)
	}
}

func TestInvalidIntervalsAreDropped(t *testing.T) {
	input := []byte("hello world")
	e := NewError("f", "m",
		Interval{9, 3},
		[]Secret{{Interval: Interval{-1, 2}, Label: "bad"}},
		[]Interval{{5, 2}, {0, len(input)}})

	got := e.Render(input)
	if !strings.Contains(got, "hello world") {
		t.Fatalf("valid context missing after bad intervals: %q", got)
	}
	if len(e.Secrets) != 0 {
		t.Fatalf("invalid secret retained: %+v", e.Secrets)
	}
}

func TestEmptyFieldAndOutOfRangeFailure(t *testing.T) {
	if got := NewError("", "boom", Interval{0, 1}, nil, nil).Error(); got != "boom" {
		t.Fatalf("empty field must not add a prefix: %q", got)
	}

	// Failure beyond the input is clamped; rendering must not panic.
	input := []byte("abc")
	e := NewError("f", "m", Interval{99, 120}, nil, []Interval{{0, 3}})
	got := e.Render(input)
	if !strings.Contains(got, "abc") {
		t.Fatalf("clamped failure lost context: %q", got)
	}

	// Empty input renders just the message line.
	empty := NewError("f", "m", Interval{0, 0}, nil, nil).Render(nil)
	if empty != "f: m\n" {
		t.Fatalf("empty input render mismatch: %q", empty)
	}
}

func TestRenderNeverEmitsSecretBytesAcrossWindows(t *testing.T) {
	// Property sweep over failure positions and moving secret spans:
	// marker text aside, every emitted byte must come from a non-secret
	// position, and the visible output must stay valid UTF-8.
	input := bytesRepeat('z', 64)
	auth := []Interval{{0, len(input)}}

	secretByte := func(iv Interval, pos int) bool {
		return pos >= iv.Start && pos < iv.End
	}

	for secretStart := 0; secretStart < len(input); secretStart += 7 {
		for secretLen := 1; secretStart+secretLen <= len(input); secretLen += 5 {
			secret := Interval{secretStart, secretStart + secretLen}
			err := NewError("f", "m", Interval{secretStart, secretStart + 1},
				[]Secret{{Interval: secret, Label: "sweep"}}, auth)

			for failure := 0; failure < len(input); failure++ {
				err.Failure = Interval{failure, failure + 1}
				body := strings.TrimPrefix(err.Render(input), "f: m\n")
				visible := strings.ReplaceAll(body, Marker, "")

				if !utf8.ValidString(visible) {
					t.Fatalf("invalid UTF-8: secret=%+v failure=%d", secret, failure)
				}

				cursor := 0
				for i := 0; i < len(visible); i++ {
					for cursor < len(input) && secretByte(secret, cursor) {
						cursor++
					}
					if cursor >= len(input) || visible[i] != input[cursor] {
						t.Fatalf("secret or mismatched byte emitted: secret=%+v failure=%d",
							secret, failure)
					}
					cursor++
				}
			}
		}
	}
}
