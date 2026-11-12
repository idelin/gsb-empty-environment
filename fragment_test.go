package netsec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidateFragmentAcceptsUserPaths(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "bare", src: "user.a = 1\n"},
		{name: "basic quoted", src: "\"user\".a = 1\n"},
		{name: "literal quoted", src: "'user'.a = 1\n"},
		{name: "escaped u", src: "\"\\u0075ser\".a = 1\n"},
		{name: "escaped U", src: "\"\\U00000075ser\".a = 1\n"},
		{name: "escaped suffix", src: "\"us\\u0065r\".a = 1\n"},
		{name: "quoted dotted suffix", src: "user.\"a.b\".c = 1\n"},
		{name: "whitespace around dots", src: "user \t . \t \"a.b\" = 1\n"},
		{name: "header whitespace", src: "[ user ]\na = 1\n"},
		{name: "header escaped", src: "[\"\\u0075ser\"]\na = 1\n"},
		{name: "header dotted", src: "[ user . a ]\nb = 1\n"},
		{name: "header literal", src: "['user']\na = 1\n"},
		{name: "nested security", src: "[user]\nsecurity.x = 1\n"},
		{name: "root nested security", src: "user.security.x = 1\n"},
		{name: "quoted nested security", src: "user.\"\\u0073ecurity\".x = 1\n"},
		{name: "quoted nested security header", src: "[user.\"\\u0073ecurity\"]\nx = 1\n"},
		{name: "empty table", src: "[user]\n"},
		{name: "comments", src: "# line\nuser.a = 1 # tail\n\t# tab comment\n"},
		{name: "crlf", src: "user.a = 1\r\n[user.b]\r\nc = 2\r\n"},
		{name: "repeated dotted prefix", src: "[user]\na.b = 1\na.c = 2\n"},
		{name: "subtable after dotted", src: "user.a.b = 1\n[user.a.c]\nd = 2\n"},
		{name: "implicit parent later", src: "[user.a.b]\nx = 1\n[user.a]\ny = 2\n"},
		{name: "array of tables repeated", src: "[[user.items]]\nname = \"one\"\n[[user.items]]\nname = \"two\"\n"},
		{name: "array of tables whitespace", src: "[[ user . items ]]\nname = \"one\"\n[[ user . items ]]\nname = \"two\"\n"},
		{name: "nested array of tables", src: "[[user.groups]]\n[[user.groups.items]]\nname = \"x\"\n[[user.groups.items]]\nname = \"y\"\n"},
		{name: "inline table", src: "user.a = { b = 1, c = { d = 2 } }\n"},
		{name: "inline dotted prefix", src: "user.a = { b.c = 1, b.d = 2 }\n"},
		{name: "empty inline table", src: "user.a = {}\n"},
		{name: "multiline basic string with fake header", src: "user.a = \"\"\"\n[security]\n\"\"\"\n"},
		{name: "multiline literal string with fake header", src: "user.a = '''\n[security]\n'''\n"},
		{name: "array with fake header", src: "user.a = [\"[security]\", { b = 1 }, { c = 2 }]\n"},
		{name: "array with comments", src: "user.a = [\n  1, # [security]\n  2,\n]\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateFragment([]byte(tt.src)); err != nil {
				t.Fatalf("ValidateFragment() error = %v\nsource:\n%s", err, tt.src)
			}
		})
	}
}

func TestValidateFragmentRejectsNonUserRoots(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "bare header", src: "[security]\nx = 1\n"},
		{name: "bare dotted", src: "security.x = 1\n"},
		{name: "basic header", src: "[\"security\"]\nx = 1\n"},
		{name: "literal header", src: "['security']\nx = 1\n"},
		{name: "escaped header first rune", src: "[\"\\u0073ecurity\"]\nx = 1\n"},
		{name: "escaped header middle", src: "[\"se\\u0063urity\"]\nx = 1\n"},
		{name: "escaped header U", src: "[\"\\U00000073ecurity\"]\nx = 1\n"},
		{name: "escaped key first rune", src: "\"\\u0073ecurity\".x = 1\n"},
		{name: "escaped key middle", src: "\"se\\u0063urity\".x = 1\n"},
		{name: "literal quoted escape looks decoded", src: "'\\u0073ecurity'.x = 1\n"},
		{name: "quoted key contains dot", src: "\"user.security\".x = 1\n"},
		{name: "quoted key escaped dot", src: "\"user\\u002esecurity\".x = 1\n"},
		{name: "case sensitivity", src: "[User]\nx = 1\n"},
		{name: "leading space in quoted key", src: "[\" user\"]\nx = 1\n"},
		{name: "nested root after header", src: "[\"user.a\"]\nx = 1\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFragment([]byte(tt.src))
			if !errors.Is(err, ErrForbiddenRoot) {
				t.Fatalf("ValidateFragment() error = %v, want ErrForbiddenRoot", err)
			}
		})
	}
}

func TestValidateFragmentRejectsDuplicateDefinitions(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "same header", src: "[user]\na = 1\n[user]\nb = 2\n"},
		{name: "same decoded header", src: "[user]\na = 1\n[\"\\u0075ser\"]\nb = 2\n"},
		{name: "same quoted header", src: "['user']\na = 1\n[\"user\"]\nb = 2\n"},
		{name: "same dotted key", src: "user.a = 1\n\"user\".\"a\" = 2\n"},
		{name: "header after dotted root", src: "user.a = 1\n[user]\nb = 2\n"},
		{name: "header after dotted child", src: "[user]\na.b = 1\n[user.a]\nc = 2\n"},
		{name: "dotted after header child", src: "[user.a]\nx = 1\n[user]\na.y = 2\n"},
		{name: "dotted after escaped header", src: "[\"\\u0075ser\".a]\nx = 1\n[user]\na.y = 2\n"},
		{name: "header after dotted implicit", src: "[user.a.b]\nx = 1\n[user]\na.c = 2\n[user.a]\ny = 3\n"},
		{name: "key collides with table", src: "[user]\na.b = 1\na = 2\n"},
		{name: "header collides with key", src: "[user]\na = 1\n[user.a]\nb = 2\n"},
		{name: "duplicate inline key", src: "user.a = { b = 1, b = 2 }\n"},
		{name: "inline value then dotted", src: "user.a = { b = 1, b.c = 2 }\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFragment([]byte(tt.src))
			if !errors.Is(err, ErrDuplicateDefinition) {
				t.Fatalf("ValidateFragment() error = %v, want ErrDuplicateDefinition", err)
			}
		})
	}
}

func TestValidateFragmentRejectsInlineTableExtension(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "dotted append", src: "user.a = { b = 1 }\nuser.a.c = 2\n"},
		{name: "header append", src: "user.a = { b = 1 }\n[user.a]\nc = 2\n"},
		{name: "nested header append", src: "user.a = { b = 1 }\n[user.a.b]\nc = 2\n"},
		{name: "nested value append", src: "user.a = { b = { c = 1 } }\nuser.a.b.d = 2\n"},
		{name: "append after header", src: "[user]\na = { b = 1 }\na.c = 2\n"},
		{name: "append subtable after header", src: "[user]\na = { b = 1 }\n[user.a.b]\nc = 2\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFragment([]byte(tt.src))
			if !errors.Is(err, ErrInlineTableExtension) {
				t.Fatalf("ValidateFragment() error = %v, want ErrInlineTableExtension", err)
			}
		})
	}
}

func TestValidateFragmentRejectsTableArrayMix(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "table then array", src: "[user]\na = 1\n[[user]]\nb = 2\n"},
		{name: "array then table", src: "[[user]]\na = 1\n[user]\nb = 2\n"},
		{name: "nested table then array", src: "[[user.items]]\na = 1\n[user.items]\nb = 2\n"},
		{name: "nested array then table", src: "[user.items]\na = 1\n[[user.items]]\nb = 2\n"},
		{name: "static array then array table", src: "user.items = []\n[[user.items]]\na = 1\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFragment([]byte(tt.src))
			if !errors.Is(err, ErrTableArrayConflict) {
				t.Fatalf("ValidateFragment() error = %v, want ErrTableArrayConflict", err)
			}
		})
	}
}

func TestValidateFragmentRejectsMalformedKeys(t *testing.T) {
	tests := []string{
		".user.a = 1\n",
		"user..a = 1\n",
		"user. = 1\n",
		"user . = 1\n",
		"user.a\n",
		"\"unterminated = 1\n",
		"\"bad\\q\".a = 1\n",
		"\"bad\\u12\".a = 1\n",
		"\"bad\\uD800\".a = 1\n",
		"[user\n",
		"[[user]\n",
		"[user]]\n",
		"user.a = 1 extra\n",
	}

	for _, src := range tests {
		t.Run(strings.ReplaceAll(src, "\n", "_"), func(t *testing.T) {
			err := ValidateFragment([]byte(src))
			if !errors.Is(err, ErrInvalidFragment) {
				t.Fatalf("ValidateFragment() error = %v, want ErrInvalidFragment\nsource:\n%s", err, src)
			}
		})
	}
}

func TestValidateFragmentSizeLimit(t *testing.T) {
	prefix := "user.a = 1\n#"
	exact := []byte(prefix + strings.Repeat("x", MaxFragmentSize-len(prefix)))
	if len(exact) != MaxFragmentSize {
		t.Fatalf("test setup produced %d bytes", len(exact))
	}
	if err := ValidateFragment(exact); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}

	tooLarge := append(bytes.Clone(exact), 'x')
	err := ValidateFragment(tooLarge)
	if !errors.Is(err, ErrFragmentTooLarge) {
		t.Fatalf("ValidateFragment() error = %v, want ErrFragmentTooLarge", err)
	}
}

func TestValidateFragmentStringAndReader(t *testing.T) {
	if err := ValidateFragmentString("user.a = 1\n"); err != nil {
		t.Fatalf("ValidateFragmentString() error = %v", err)
	}
	if err := ValidateFragmentReader(strings.NewReader("user.a = 1\n")); err != nil {
		t.Fatalf("ValidateFragmentReader() error = %v", err)
	}
	if !IsValidFragment([]byte("user.a = 1\n")) {
		t.Fatal("IsValidFragment() = false, want true")
	}
	if IsValidFragment([]byte("security.a = 1\n")) {
		t.Fatal("IsValidFragment() = true, want false")
	}
}
