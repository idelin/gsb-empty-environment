package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestFragmentNormalization(t *testing.T) {
	t.Parallel()

	if err := Validate([]byte("\xEF\xBB\xBFuser.a = \"x\"\r\nuser.b = 2\r\n")); err != nil {
		t.Fatalf("BOM and CRLF fragment rejected: %v", err)
	}
	if err := Validate([]byte("user.a = \"\xFF\"")); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8 error = %v, want ErrInvalidUTF8", err)
	}
	if err := Validate([]byte("user.a = 1\n\xEF\xBB\xBFuser.b = 2\n")); !errors.Is(err, ErrInvalidTOML) {
		t.Fatalf("embedded BOM error = %v, want ErrInvalidTOML", err)
	}
}

func TestKeyPathAdmission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fragment string
		wantErr  error
	}{
		{name: "bare key", fragment: "user.a = 1"},
		{name: "dotted whitespace", fragment: "user . a = 1"},
		{name: "quoted segments", fragment: `"user"."a" = 1`},
		{name: "quoted table header", fragment: "[ user . \"a\" ]\nb = 1"},
		{name: "escaped root", fragment: `"\u0075ser"."\u0061" = 1`},
		{name: "escaped quoted header", fragment: "[\"\\u0075ser\".x]\ny = 1"},
		{name: "literal backslash", fragment: `'\u0075ser'.a = 1`, wantErr: ErrForbiddenRoot},
		{name: "literal quoted root", fragment: `'user'.a = 1`},
		{name: "quoted dot is one segment", fragment: `"user.a" = 1`, wantErr: ErrForbiddenRoot},
		{name: "escaped dot is one segment", fragment: `"user\u002Ea" = 1`, wantErr: ErrForbiddenRoot},
		{name: "quoted security", fragment: `"\u0073ecurity".x = 1`, wantErr: ErrForbiddenRoot},
		{name: "case sensitive root", fragment: `"\u0055ser".x = 1`, wantErr: ErrForbiddenRoot},
		{name: "uppercase bare root", fragment: `User.x = 1`, wantErr: ErrForbiddenRoot},
		{name: "bare security", fragment: `security.x = 1`, wantErr: ErrForbiddenRoot},
		{name: "quoted security header", fragment: "[\"\\u0073ecurity\"]\nx = 1", wantErr: ErrForbiddenRoot},
		{name: "quoted user subtable", fragment: `user."\u0073ecurity".x = 1`},
		{name: "empty quoted segment", fragment: `user."".x = 1`},
		{name: "dot in quoted segment", fragment: `user."a.b".c = 1`},
		{name: "missing segment", fragment: "user. = 1", wantErr: ErrInvalidTOML},
		{name: "double dot", fragment: "user..a = 1", wantErr: ErrInvalidTOML},
		{name: "unquoted unicode", fragment: "用户.a = 1", wantErr: ErrInvalidTOML},
		{name: "invalid unicode escape", fragment: `user."\uD800".a = 1`, wantErr: ErrInvalidTOML},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Validate([]byte(tt.fragment))
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate(%q) = %v, want nil", tt.fragment, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate(%q) = %v, want %v", tt.fragment, err, tt.wantErr)
			}
		})
	}
}

func TestCrossFragmentConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fragments []string
		wantErr   error
	}{
		{
			name:      "same path",
			fragments: []string{"user.a = 1\n", "user.a = 2\n"},
			wantErr:   ErrDuplicateKey,
		},
		{
			name:      "same decoded path",
			fragments: []string{"user.a = 1\n", "user.\"\\u0061\" = 2\n"},
			wantErr:   ErrDuplicateKey,
		},
		{
			name:      "same quoted table",
			fragments: []string{"[user.profile]\na = 1\n", "[\"user\".\"profile\"]\nb = 2\n"},
			wantErr:   ErrTableRedefined,
		},
		{
			name:      "dotted then header",
			fragments: []string{"user.profile.a = 1\n", "[user.profile]\nb = 2\n"},
			wantErr:   ErrTableRedefined,
		},
		{
			name:      "header then dotted",
			fragments: []string{"[user.profile]\na = 1\n", "user.profile.b = 2\n"},
			wantErr:   ErrTableRedefined,
		},
		{
			name:      "explicit parent then dotted",
			fragments: []string{"[user]\na = 1\n", "user.b = 2\n"},
			wantErr:   ErrTableRedefined,
		},
		{
			name:      "inline table append",
			fragments: []string{"user.profile = { a = 1 }\n", "user.profile.b = 2\n"},
			wantErr:   ErrInlineTableAppend,
		},
		{
			name:      "inline table header append",
			fragments: []string{"user.profile = { a = 1 }\n", "[user.profile]\nb = 2\n"},
			wantErr:   ErrInlineTableAppend,
		},
		{
			name:      "array table then table",
			fragments: []string{"[[user.items]]\nname = \"a\"\n", "[user.items]\nname = \"b\"\n"},
			wantErr:   ErrArrayTableMismatch,
		},
		{
			name:      "table then array table",
			fragments: []string{"[user.items]\nname = \"a\"\n", "[[user.items]]\nname = \"b\"\n"},
			wantErr:   ErrArrayTableMismatch,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			admitter := NewAdmitter()
			for _, fragment := range tt.fragments {
				err := admitter.Admit([]byte(fragment))
				if err == nil {
					continue
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Admit(%q) = %v, want %v", fragment, err, tt.wantErr)
				}
				return
			}
			t.Fatalf("all fragments accepted, want %v", tt.wantErr)
		})
	}
}

func TestDuplicateDefinitionsAndInlineConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fragment string
		wantErr  error
	}{
		{name: "duplicate key", fragment: "user.a = 1\nuser.a = 2\n", wantErr: ErrDuplicateKey},
		{name: "value then table header", fragment: "user.a = 1\n[user.a]\nb = 2\n", wantErr: ErrDuplicateKey},
		{name: "table then scalar dotted key", fragment: "[user.a]\nb = 1\n[user]\na.c = 2\n", wantErr: ErrTableRedefined},
		{name: "dotted then header", fragment: "user.a.b = 1\n[user.a]\nc = 2\n", wantErr: ErrTableRedefined},
		{name: "duplicate inline key", fragment: "user.a = { b = 1, b = 2 }\n", wantErr: ErrDuplicateKey},
		{name: "inline key and subtable", fragment: "user.a = { b = 1, b.c = 2 }\n", wantErr: ErrDuplicateKey},
		{name: "inline table append in fragment", fragment: "user.a = { b = 1 }\nuser.a.c = 2\n", wantErr: ErrInlineTableAppend},
		{name: "nested inline append", fragment: "user.a = { b = { c = 1 } }\nuser.a.b.d = 2\n", wantErr: ErrInlineTableAppend},
		{name: "array value append", fragment: "user.a = [1, 2]\nuser.a.b = 3\n", wantErr: ErrDuplicateKey},
		{name: "missing value", fragment: "user.a =\n", wantErr: ErrInvalidTOML},
		{name: "junk after value", fragment: "user.a = 1 x\n", wantErr: ErrInvalidTOML},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Validate([]byte(tt.fragment))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate(%q) = %v, want %v", tt.fragment, err, tt.wantErr)
			}
		})
	}
}

func TestArrayTables(t *testing.T) {
	t.Parallel()

	admitter := NewAdmitter()
	fragments := []string{
		"[[user.items]]\nname = \"one\"\n",
		"[[user.items]]\nname = \"two\"\n",
		"[user.items.detail]\nkind = \"x\"\n",
		"[[user.items.tags]]\nname = \"tag\"\n",
	}
	for _, fragment := range fragments {
		if err := admitter.Admit([]byte(fragment)); err != nil {
			t.Fatalf("Admit(%q) = %v", fragment, err)
		}
	}

	if err := admitter.Admit([]byte("[[user.items]]\nname = \"three\"\nname = \"duplicate\"\n")); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate inside new array element = %v, want ErrDuplicateKey", err)
	}
}

func TestCommonTOMLValues(t *testing.T) {
	t.Parallel()

	fragment := `user.string = "tab\tunicode\u0041"
user.literal = 'C:\Users\name'
user.multiline = """
line one
line two"""
user.multiline_literal = '''
literal line one
literal line two'''
user.array = [1, 2.5, "three", true, [4], { a = 5 }]
user.inline = { a = 1, b = [1, 2], "c.d" = { e = "f" } }
user.hex = 0xDEAD_BEEF
user.float = +1.0e-3
user.date = 1979-05-27
user.time = 07:32:00.999
user.datetime = 1979-05-27T07:32:00Z
user.local_datetime = 1979-05-27 07:32:00
user.boolean = true
user.nan = nan
user.comment = 1 # trailing comment
`
	if err := Validate([]byte(fragment)); err != nil {
		t.Fatalf("valid TOML values rejected: %v", err)
	}
}

func TestInvalidTOMLValues(t *testing.T) {
	t.Parallel()

	values := []string{
		"01",
		"1.",
		`"\q"`,
		`"unterminated`,
		"{ a = 1, }",
		"[1 2]",
		"true false",
		"1979-05-27T",
	}
	for _, value := range values {
		fragment := "user.bad = " + value + "\n"
		if err := Validate([]byte(fragment)); !errors.Is(err, ErrInvalidTOML) {
			t.Errorf("Validate(%q) = %v, want ErrInvalidTOML", fragment, err)
		}
	}
}

func TestFragmentSizeLimit(t *testing.T) {
	t.Parallel()

	const prefix = `user.a = "`
	const suffix = `"`
	fill := MaxFragmentBytes - len(prefix) - len(suffix)
	atLimit := prefix + string(bytes.Repeat([]byte{'x'}, fill)) + suffix
	if len(atLimit) != MaxFragmentBytes {
		t.Fatalf("test fragment length = %d", len(atLimit))
	}
	if err := Validate([]byte(atLimit)); err != nil {
		t.Fatalf("fragment at limit rejected: %v", err)
	}

	overLimit := atLimit + "x"
	if err := Validate([]byte(overLimit)); !errors.Is(err, ErrFragmentTooLarge) {
		t.Fatalf("oversized fragment error = %v, want ErrFragmentTooLarge", err)
	}
}

func TestRejectedFragmentDoesNotMutateState(t *testing.T) {
	t.Parallel()

	admitter := NewAdmitter()
	if err := admitter.Admit([]byte("user.a = 1\n")); err != nil {
		t.Fatalf("initial Admit: %v", err)
	}
	if err := admitter.Admit([]byte("user.a = 2\nuser.b = 3\n")); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("conflicting Admit = %v, want ErrDuplicateKey", err)
	}
	if err := admitter.Admit([]byte("user.b = 4\n")); err != nil {
		t.Fatalf("state was mutated by rejected fragment: %v", err)
	}
}

func TestErrorReportsDecodedPath(t *testing.T) {
	t.Parallel()

	err := Validate([]byte(`"\u0073ecurity".x = 1`))
	var admissionError *Error
	if !errors.As(err, &admissionError) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if want := []string{"security", "x"}; len(admissionError.Path) != len(want) ||
		admissionError.Path[0] != want[0] || admissionError.Path[1] != want[1] {
		t.Fatalf("decoded path = %q, want %q", admissionError.Path, want)
	}
}

func FuzzAdmitNeverPanics(f *testing.F) {
	seeds := []string{
		"",
		"user.a = 1\n",
		"\xEF\xBB\xBFuser.a = \"x\"\r\n",
		"user.a = { b = [1, 2, { c = 3 }] }\n",
		"[[user.items]]\nname = \"x\"\n",
		"user.\"\\u0073ecurity\".x = 1\n",
		"user.a = 1979-05-27 07:32:00Z\n",
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, fragment []byte) {
		if len(fragment) > MaxFragmentBytes {
			return
		}
		_ = NewAdmitter().Admit(fragment)
	})
}
