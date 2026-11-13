package prioritystate

import (
	"errors"
	"testing"
)

func TestParsePriorityFieldExamples(t *testing.T) {
	cases := []struct {
		field string
		want  Entry
	}{
		// Examples taken from RFC 9218, section 6.1.
		{"u=7", Entry{Urgency: 7}},
		{"i", Entry{Urgency: 3, Incremental: true}},
		{"u=2, i", Entry{Urgency: 2, Incremental: true}},
		{"u=0; foo=bar", Entry{Urgency: 0}},
		{" i=?1 ", Entry{Urgency: 3, Incremental: true}},
	}
	for _, tc := range cases {
		got, ok := parsePriorityFieldValue([]byte(tc.field))
		if !ok {
			t.Fatalf("field %q: rejected", tc.field)
		}
		if got != tc.want {
			t.Fatalf("field %q: got %+v, want %+v", tc.field, got, tc.want)
		}
	}
}

func TestParserEdgeCases(t *testing.T) {
	valid := []string{
		"u=-0",              // negative zero integer
		"u=7, u=1.5",        // decimal overrides with wrong type -> reject below
		`x="a\"b", u=1`,     // escaped quote in string
		`x="a\\b", u=1`,     // escaped backslash in string
		"x=:+/=:, u=1",      // full base64 alphabet in byte sequence
		"x=(1 2), u=1",      // inner list under unknown key
		"x=(), u=1",         // empty inner list
		"u=1;x=?0;y=\"z\"",  // varied parameter types
		"t=ok, u=2",         // token value under unknown key
		"u=000000000000000", // 15 digits, still within the integer limit
		"u=-3",              // negative integer structurally valid, range-checked later
	}
	invalid := []string{
		"u=1.1234",              // four fractional digits
		"u=0000000000000000",    // 16 digits
		`x="bad` + "\x00" + `"`, // control character in string
		`x="a`,                  // unterminated string
		`x="a\b"`,               // invalid string escape
		"x=:a b:",               // invalid base64 character
		"x=:",                   // missing closing colon
		"x=(1",                  // unterminated inner list
		"x=(1;",                 // unterminated parameters in inner list
		"i=?2",                  // boolean must be ?0 or ?1
		"x=1a",                  // number abuts token characters
		"1=u",                   // key must start with lcalpha
		"u= ",                   // missing bare item
		";u=1",                  // parameters without a member
	}

	for _, field := range valid {
		if _, ok := parseDictionary([]byte(field)); !ok {
			t.Errorf("expected valid dictionary: %q", field)
		}
	}
	for _, field := range invalid {
		if _, ok := parseDictionary([]byte(field)); ok {
			t.Errorf("expected invalid dictionary: %q", field)
		}
	}

	// The decimal override is a type error for the recognized "u" key.
	if _, ok := parsePriorityFieldValue([]byte("u=7, u=1.5")); ok {
		t.Fatal("decimal u must be rejected")
	}
	if _, ok := parsePriorityFieldValue([]byte("u=-3")); ok {
		t.Fatal("negative urgency must be rejected by range check")
	}
}

func TestConnErrorWrapping(t *testing.T) {
	err := &ConnError{err: ErrProtocolError}
	if !errors.Is(err, ErrProtocolError) {
		t.Fatal("ConnError must unwrap to its cause")
	}
	if err.Error() != ErrProtocolError.Error() {
		t.Fatalf("Error() = %q", err.Error())
	}

	s := New()
	// Closing an unknown stream is a no-op instead of allocating a tombstone.
	s.StreamClosed(77)
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("unknown close must not allocate state, got %d", got)
	}

	// Double open of the same stream is idempotent and keeps the entry.
	func() {
		first, openErr := s.StreamOpened(3)
		if openErr != nil || first != DefaultEntry {
			t.Fatalf("first open: %+v %v", first, openErr)
		}
		second, openErr := s.StreamOpened(3)
		if openErr != nil || second != DefaultEntry {
			t.Fatalf("second open: %+v %v", second, openErr)
		}
	}()
}
