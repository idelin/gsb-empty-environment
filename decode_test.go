package netsec

import (
	"bytes"
	"errors"
	"testing"
	"unicode/utf8"
)

func TestCESU8SurrogatePairIsNotCombined(t *testing.T) {
	// U+1F600 encoded as CESU-8: U+D83D U+DE00, three bytes each.
	pair := []byte{0xED, 0xA0, 0xBD, 0xED, 0xB8, 0x80}

	if Valid(pair) {
		t.Fatal("CESU-8 surrogate pair accepted as valid UTF-8")
	}
	if !ContainsCESU8(pair) {
		t.Fatal("CESU-8 surrogate pair not detected")
	}
	if !IsCESU8Pair(pair) {
		t.Fatal("CESU-8 high/low surrogate pair not detected")
	}

	r, size := DecodeRune(pair)
	if r != utf8.RuneError || size != 1 {
		t.Fatalf("DecodeRune(pair) = %U, %d; want RuneError, 1", r, size)
	}

	got := Decode(pair)
	for i, r := range got {
		if r != utf8.RuneError {
			t.Fatalf("Decode(pair)[%d] = %U; want RuneError", i, r)
		}
	}
	if len(got) != len(pair) {
		t.Fatalf("Decode(pair) returned %d runes; want one replacement per invalid byte", len(got))
	}
}

func TestLoneSurrogatesFail(t *testing.T) {
	cases := map[string][]byte{
		"high surrogate": {0xED, 0xA0, 0x80},
		"low surrogate":  {0xED, 0xB0, 0x80},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if Valid(in) {
				t.Fatal("lone surrogate accepted as valid UTF-8")
			}
			r, size := DecodeRune(in)
			if r != utf8.RuneError || size != 1 {
				t.Fatalf("DecodeRune = %U, %d; want RuneError, 1", r, size)
			}
		})
	}
}

func TestFourByteSupplementaryCharacterDecodes(t *testing.T) {
	// U+1F600 encoded as standard UTF-8.
	in := []byte{0xF0, 0x9F, 0x98, 0x80}
	if !Valid(in) {
		t.Fatal("genuine four-byte UTF-8 rejected")
	}
	r, size := DecodeRune(in)
	if r != '\U0001F600' || size != 4 {
		t.Fatalf("DecodeRune = %U, %d; want U+1F600, 4", r, size)
	}
	if got := Decode(in); len(got) != 1 || got[0] != '\U0001F600' {
		t.Fatalf("Decode = %U; want [U+1F600]", got)
	}
}

func TestStrictAndSanitize(t *testing.T) {
	in := append([]byte("ok "), []byte{0xED, 0xA0, 0xBD, 0xED, 0xB8, 0x80}...)
	in = append(in, []byte(" done")...)

	if _, err := DecodeStrict(in); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("DecodeStrict error = %v; want ErrInvalidUTF8", err)
	}
	if err := ValidateString("hello \U0001F600"); err != nil {
		t.Fatalf("ValidateString(valid) = %v", err)
	}
	if err := ValidateString(string(in)); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("ValidateString(CESU-8) = %v; want ErrInvalidUTF8", err)
	}

	got := Sanitize(in)
	if !bytes.HasPrefix(got, []byte("ok ")) || !bytes.HasSuffix(got, []byte(" done")) {
		t.Fatalf("Sanitize changed valid text: %q", got)
	}
	if bytes.Contains(got, []byte{0xED, 0xA0}) {
		t.Fatalf("Sanitize preserved invalid CESU-8 bytes: %q", got)
	}
	if SanitizeString("hello \U0001F600") != "hello \U0001F600" {
		t.Fatal("SanitizeString changed a valid supplementary character")
	}
}
