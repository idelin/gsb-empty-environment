package netsec

import (
	"errors"
	"strings"
	"testing"
)

func mustNormalizer(t *testing.T, prefix string, cfg *Config) *Normalizer {
	t.Helper()
	n, err := NewNormalizer(prefix, cfg)
	if err != nil {
		t.Fatalf("NewNormalizer(%q): %v", prefix, err)
	}
	return n
}

func TestNormalizeStorageAndRetrieval(t *testing.T) {
	n := mustNormalizer(t, "tenant1:", nil)

	req, err := n.NormalizeText([]byte("set foo 0 0 3\r\nbar\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(req.Bytes), "set tenant1:foo 0 0 3\r\nbar\r\n"; got != want {
		t.Fatalf("normalized set = %q, want %q", got, want)
	}
	if string(req.Key()) != "foo" || req.ResponseKind != ResponseSingle {
		t.Fatalf("request metadata = key %q kind %v", req.Key(), req.ResponseKind)
	}

	req, err = n.NormalizeText([]byte("get  alpha   beta\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(req.Bytes), "get tenant1:alpha tenant1:beta\r\n"; got != want {
		t.Fatalf("normalized get = %q, want %q", got, want)
	}
	if len(req.Keys) != 2 || string(req.Keys[0]) != "alpha" || string(req.Keys[1]) != "beta" {
		t.Fatalf("get keys = %q", req.Keys)
	}

	req, err = n.NormalizeText([]byte("gets a b\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(req.Bytes), "gets tenant1:a tenant1:b\r\n"; got != want {
		t.Fatalf("normalized gets = %q, want %q", got, want)
	}
}

func TestKeyLengthBoundary(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil) // 3 + 247 = 250
	maxLeaf := strings.Repeat("k", MaxProtocolKeyLength-len("t1:"))
	if _, err := n.NormalizeText([]byte("get " + maxLeaf + "\r\n")); err != nil {
		t.Fatalf("250-byte prefixed key rejected: %v", err)
	}
	tooLong := maxLeaf + "x"
	_, err := n.NormalizeText([]byte("get " + tooLong + "\r\n"))
	if !errors.Is(err, ErrKeyTooLong) {
		t.Fatalf("overlong key error = %v, want ErrKeyTooLong", err)
	}
}

func TestKeyWhitespaceAndControls(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)

	// A space is a protocol separator. It cannot smuggle a raw key; each
	// resulting token is independently validated and prefixed.
	req, err := n.NormalizeText([]byte("get bad key\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(req.Bytes); got != "get t1:bad t1:key\r\n" {
		t.Fatalf("space-separated keys normalized as %q", got)
	}

	// In a single-key position extra tokens make the command malformed.
	if _, err := n.NormalizeText([]byte("set bad key 0 0 1\r\nx\r\n")); err == nil {
		t.Fatal("storage command with a space-split key was accepted")
	}

	for _, key := range []string{"bad\tkey", "bad\x01key", "bad\x7fkey"} {
		_, err := n.NormalizeText([]byte("get " + key + "\r\n"))
		if err == nil {
			t.Fatalf("key %q with a control byte was accepted", key)
		}
	}
}

func TestStorageValueExactlyFramed(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)

	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"exact", "set k 0 0 3\r\nabc\r\n", true},
		{"embedded crlf", "set k 0 0 5\r\nab\r\nc\r\n", true},
		{"empty", "set k 0 0 0\r\n\r\n", true},
		{"one byte extra", "set k 0 0 3\r\nabcX\r\n", false},
		{"one byte short", "set k 0 0 3\r\nab\r\n", false},
		{"noreply exact", "set k 0 0 3 noreply\r\nabc\r\n", true},
		{"noreply extra", "set k 0 0 3 noreply\r\nabcX\r\n", false},
		{"noreply short", "set k 0 0 3 noreply\r\nab\r\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := n.NormalizeText([]byte(tc.raw))
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if strings.Contains(tc.raw, "noreply") && req.ResponseKind != ResponseNone {
					t.Fatalf("noreply request response kind = %v", req.ResponseKind)
				}
				return
			}
			if err == nil {
				t.Fatal("malformed data block was accepted")
			}
			if !errors.Is(err, ErrMalformedBody) && !errors.Is(err, ErrTruncated) {
				t.Fatalf("error = %v, want framing error", err)
			}
		})
	}
}

func TestCommandLineRequiresCRLF(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	if _, err := n.NormalizeText([]byte("version\n")); !errors.Is(err, ErrMalformedRequest) {
		t.Fatalf("bare LF error = %v", err)
	}
	if _, err := n.NormalizeText([]byte("version\rX")); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated line error = %v", err)
	}
}

func TestPrefixValidation(t *testing.T) {
	for _, prefix := range []string{"", "bad prefix", "bad\ttab", "bad\x00nul"} {
		if _, err := NewNormalizer(prefix, nil); err == nil {
			t.Fatalf("prefix %q was accepted", prefix)
		}
	}
}

func TestStatelessHelpersRejectTrailingPipelining(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	if _, err := n.NormalizeText([]byte("version\r\nversion\r\n")); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("trailing request error = %v", err)
	}
	req, err := n.NormalizeText([]byte("version\r\n"))
	if err != nil || string(req.Bytes) != "version\r\n" {
		t.Fatalf("version normalization = %q, %v", req.Bytes, err)
	}
}

func TestValueLimitRejectedBeforeReading(t *testing.T) {
	cfg := Config{MaxValueLength: 4}
	n := mustNormalizer(t, "t1:", &cfg)
	if _, err := n.NormalizeText([]byte("set k 0 0 5\r\nabcde\r\n")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized value error = %v", err)
	}
}

func TestPrefixKeyAndStripKey(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	key, err := n.PrefixKey([]byte("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != "t1:alpha" {
		t.Fatalf("prefix key = %q", key)
	}
	got, ok := n.StripKey(key)
	if !ok || string(got) != "alpha" {
		t.Fatalf("strip tenant key = %q, %v", got, ok)
	}
	for _, foreign := range [][]byte{[]byte("t2:alpha"), []byte("t1:"), []byte("t1:bad key")} {
		if _, ok := n.StripKey(foreign); ok {
			t.Fatalf("foreign key %q was accepted", foreign)
		}
	}
}

func TestSupportedCommandShapes(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	tests := []struct {
		raw  string
		want string
	}{
		{"cas key 0 0 1 9\r\nx\r\n", "cas t1:key 0 0 1 9\r\nx\r\n"},
		{"append key 0 0 1\r\nx\r\n", "append t1:key 0 0 1\r\nx\r\n"},
		{"delete key\r\n", "delete t1:key\r\n"},
		{"delete key noreply\r\n", "delete t1:key noreply\r\n"},
		{"incr key 5\r\n", "incr t1:key 5\r\n"},
		{"decr key 7 noreply\r\n", "decr t1:key 7 noreply\r\n"},
		{"touch key 10\r\n", "touch t1:key 10\r\n"},
		{"gat 10 a b\r\n", "gat 10 t1:a t1:b\r\n"},
		{"stats\r\n", "stats\r\n"},
		{"flush_all 5\r\n", "flush_all 5\r\n"},
		{"verbosity 2\r\n", "verbosity 2\r\n"},
		{"quit\r\n", "quit\r\n"},
	}
	for _, tc := range tests {
		req, err := n.NormalizeText([]byte(tc.raw))
		if err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if got := string(req.Bytes); got != tc.want {
			t.Fatalf("%q normalized as %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestRejectedCommandShapes(t *testing.T) {
	cfg := Config{MaxKeysPerRequest: 2}
	n := mustNormalizer(t, "t1:", &cfg)
	for _, raw := range []string{
		"shutdown\r\n",
		"set key 0 nope 1\r\nx\r\n",
		"set key 0 0 1 bogus\r\nx\r\n",
		"delete key 10\r\n",
		"get a b c\r\n",
		"version extra\r\n",
	} {
		if _, err := n.NormalizeText([]byte(raw)); err == nil {
			t.Fatalf("malformed command %q was accepted", raw)
		}
	}
	if _, err := n.NormalizeText([]byte{0x80}); !errors.Is(err, ErrProtocolSwitch) {
		t.Fatalf("text helper accepted binary magic: %v", err)
	}
}
