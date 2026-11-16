package netsec

import (
	"errors"
	"testing"
)

func TestUnescapeStandardSequences(t *testing.T) {
	rules := DefaultFieldRules()
	cases := map[string]string{
		`plain`:                  "plain",
		`a\F\b`:                  "a" + string(rules.Field) + "b",
		`\S\`:                    string(rules.Component),
		`\T\`:                    string(rules.Subcomponent),
		`\R\`:                    string(rules.Repetition),
		`\E\`:                    string(rules.Escape),
		`\H\highlight\N\`:        "highlight",
		`\X41\`:                  "A",
		`\X4d4c4c\`:              "MLL",
		`mixed\F\\X20\\X0a\done`: "mixed" + string(rules.Field) + " \n" + "done",
		`lower \x7a\ hex`:        "lower z hex",
		``:                       "",
	}
	for input, want := range cases {
		got, err := UnescapeFieldWithRules([]byte(input), rules)
		if err != nil {
			t.Fatalf("UnescapeField(%q): %v", input, err)
		}
		if string(got) != want {
			t.Fatalf("UnescapeField(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUnescapeRejectsFramingBytes(t *testing.T) {
	forbidden := []string{
		`\X0B\`,
		`\X0b\`,
		`\X1C\`,
		`prefix\X1c\suffix`,
		`\X0B\1C\`,
		string(VT),
		string(FS),
		`before\X0B\after`,
	}
	for _, input := range forbidden {
		got, err := UnescapeField([]byte(input))
		if !errors.Is(err, ErrEscape) {
			t.Fatalf("UnescapeField(%q): want ErrEscape, got frame=%q err=%v", input, got, err)
		}
		if err := ValidateStoredField(got); err == nil && got != nil {
			t.Fatalf("decoded output % X must not pass storage validation", got)
		}
	}
}

func TestUnescapeMalformed(t *testing.T) {
	malformed := []string{
		`unterminated\X41`,
		`\X4\`,
		`\XZZ\`,
		`\Q\`,
		`\`,
		`abc\`,
		`\X\`,
	}
	for _, input := range malformed {
		if _, err := UnescapeField([]byte(input)); !errors.Is(err, ErrEscape) {
			t.Fatalf("UnescapeField(%q): want ErrEscape, got %v", input, err)
		}
	}
}

func TestValidateStoredField(t *testing.T) {
	clean := [][]byte{
		[]byte("normal field value 123"),
		{0x01, 0x02, 'a', CR},
		nil,
	}
	for _, value := range clean {
		if err := ValidateStoredField(value); err != nil {
			t.Fatalf("clean value % X rejected: %v", value, err)
		}
	}

	dirty := [][]byte{
		{VT},
		{FS},
		[]byte("data" + string(FS) + string(CR) + string(VT)),
	}
	for _, value := range dirty {
		if err := ValidateStoredField(value); !errors.Is(err, ErrEscape) {
			t.Fatalf("dirty value % X: want ErrEscape, got %v", value, err)
		}
	}
}

func TestCustomFieldRules(t *testing.T) {
	rules := FieldRules{
		Field:        '#',
		Component:    '<',
		Repetition:   ';',
		Escape:       '/',
		Subcomponent: '>',
	}
	got, err := UnescapeFieldWithRules([]byte(`a/F/b/S/c/T/d/R/e/E/`), rules)
	if err != nil {
		t.Fatalf("custom rules: %v", err)
	}
	want := "a#b<c>d;e/"
	if string(got) != want {
		t.Fatalf("custom rules = %q, want %q", got, want)
	}
}
