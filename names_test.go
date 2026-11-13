package netsec

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{"a", "foo.txt", "...", "文件", "café", strings.Repeat("x", 255)}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{
		"", ".", "..",
		"a/b", "/", "a/",
		"a\x00b", "\x00",
		string([]byte{0xff, 0xfe}),
		string([]byte{'a', 0x80}),
	}
	for _, name := range invalid {
		if err := ValidateName(name); err != ErrInvalidName {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		name string
		mode CaseMode
		want string
	}{
		{"FooBar.TXT", CaseFoldASCII, "foobar.txt"},
		{"FooBar.TXT", CaseSensitive, "FooBar.TXT"},
		{"文件A", CaseFoldASCII, "文件a"},
		{"ÀÉÜ", CaseFoldASCII, "ÀÉÜ"},       // 非 ASCII 不折叠
		{"Straße", CaseFoldASCII, "straße"}, // ß 不变，仅 ASCII 折叠
		{"café", CaseFoldASCII, "café"},
		{"CAFÉ", CaseFoldASCII, "cafÉ"}, // É 为非 ASCII，保留
	}
	for _, c := range cases {
		if got := Canonicalize(c.name, c.mode); got != c.want {
			t.Errorf("Canonicalize(%q, %d) = %q, want %q", c.name, c.mode, got, c.want)
		}
	}
}
