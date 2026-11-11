package resource

import (
	"net/url"
	"sort"
	"strings"
	"testing"
)

func TestParseNormalizesRepeatedSlashesAndEncoding(t *testing.T) {
	equivalent := []string{
		"/dl/report/final.csv",
		"/dl//report/final.csv",
		"/dl///report//final.csv",
		"/dl/report/%66inal.csv",
		"/dl/report/%66%69%6Eal.csv",
	}
	base := MustParse(equivalent[0])
	for _, raw := range equivalent[1:] {
		id, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", raw, err)
		}
		if !id.Equal(base) {
			t.Errorf("Parse(%q) = %q, want canonical %q", raw, id, base)
		}
		if id.CacheKey() != base.CacheKey() {
			t.Errorf("Parse(%q) cache key %q, want %q", raw, id.CacheKey(), base.CacheKey())
		}
	}
}

func TestParseDistinguishesLiteralPlusFromSpace(t *testing.T) {
	plus := MustParse("/dl?q=a+b")
	escapedPlus := MustParse("/dl?q=a%2Bb")
	space := MustParse("/dl?q=a%20b")

	if !plus.Equal(escapedPlus) {
		t.Errorf("raw '+' and %%2B must be the same literal plus: %q vs %q", plus, escapedPlus)
	}
	if plus.Equal(space) {
		t.Errorf("literal plus and space must differ, both canonicalize to %q", plus)
	}
	if got := plus.Params()[0].Value; got != "a+b" {
		t.Errorf("raw '+' decoded to %q, want literal %q", got, "a+b")
	}
	if got := space.Params()[0].Value; got != "a b" {
		t.Errorf("%%20 decoded to %q, want %q", got, "a b")
	}
}

func TestParsePreservesOrderedDuplicateParams(t *testing.T) {
	id := MustParse("/dl?b=1&a=2&a=3&b=4")
	want := []Param{
		{Key: "b", Value: "1"},
		{Key: "a", Value: "2"},
		{Key: "a", Value: "3"},
		{Key: "b", Value: "4"},
	}
	got := id.Params()
	if len(got) != len(want) {
		t.Fatalf("Params() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Params()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	forward := MustParse("/dl?a=1&a=2")
	reversed := MustParse("/dl?a=2&a=1")
	if forward.Equal(reversed) {
		t.Errorf("reordered duplicates must be distinct, both canonicalize to %q", forward)
	}
}

func TestCanonicalRoundTrip(t *testing.T) {
	raws := []string{
		"/dl//report/%7Efinal.csv?x=1&x=2&q=a+b",
		"/dl?q=a%20b&n=%2F%3F%26",
		"/dl/a%2Fb/c",
		"/dl",
	}
	for _, raw := range raws {
		id := MustParse(raw)
		reparsed, err := Parse(id.String())
		if err != nil {
			t.Fatalf("reparse of canonical %q failed: %v", id, err)
		}
		if !reparsed.Equal(id) {
			t.Errorf("round trip of %q yielded %q", id, reparsed)
		}
	}
}

func TestIDIsImmutable(t *testing.T) {
	id := MustParse("/dl?a=1&a=2")
	canonical := id.String()

	params := id.Params()
	params[0].Value = "forged"

	if id.String() != canonical {
		t.Errorf("mutating the returned params changed the ID: %q", id)
	}
}

func TestParseRejectsInvalidEncoding(t *testing.T) {
	invalid := []string{
		"",                       // empty
		"dl/report",              // not absolute
		"/dl/%zz",                // bad hex in path
		"/dl/%",                  // lone percent
		"/dl/%2",                 // truncated
		"/dl/report%",            // trailing percent
		"/dl?q=%GG",              // bad hex in query value
		"/dl?%4=key",             // truncated in query key
		"/dl?q=a%",               // trailing percent in query
	}
	for _, raw := range invalid {
		if id, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) = %q, want error", raw, id)
		}
	}
}

// naiveKey reproduces the legacy bug: form-decode the query ('+' becomes a
// space) and sort parameters, so distinct resources collapse onto one key.
func naiveKey(raw string) string {
	path, query, _ := strings.Cut(raw, "?")
	values, err := url.ParseQuery(query)
	if err != nil {
		return path
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		vals := append([]string(nil), values[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			pairs = append(pairs, k+"="+v)
		}
	}
	if len(pairs) == 0 {
		return path
	}
	return path + "?" + strings.Join(pairs, "&")
}

// TestDistinctResourcesShareNaiveKeyButNotID is the counterexample: pairs of
// genuinely different resources that the legacy keying scheme conflates.
func TestDistinctResourcesShareNaiveKeyButNotID(t *testing.T) {
	pairs := [][2]string{
		// Literal plus vs. space: form decoding maps both to "a b".
		{"/dl?q=a+b", "/dl?q=a%20b"},
		// Ordered duplicates: sorting maps both to "a=1&a=2".
		{"/dl?a=1&a=2", "/dl?a=2&a=1"},
		// Parameter order across keys: sorting maps both to "a=1&b=2".
		{"/dl?a=1&b=2", "/dl?b=2&a=1"},
	}
	for _, pair := range pairs {
		left, right := MustParse(pair[0]), MustParse(pair[1])

		if naiveKey(pair[0]) != naiveKey(pair[1]) {
			t.Fatalf("test bug: naive keys for %q and %q should collide", pair[0], pair[1])
		}
		if left.Equal(right) {
			t.Errorf("ID(%q) and ID(%q) must not share a key, both are %q", pair[0], pair[1], left)
		}
		if left.CacheKey() == right.CacheKey() {
			t.Errorf("cache keys collide for %q and %q", pair[0], pair[1])
		}
	}
}
