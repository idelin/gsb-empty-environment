package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNULRejectsWholeFile(t *testing.T) {
	clearEnv(t, "DOTENV_TEST_SAFE")
	clearEnv(t, "DOTENV_TEST_BAD")

	path := filepath.Join(t.TempDir(), ".env")
	data := "DOTENV_TEST_SAFE=present\nDOTENV_TEST_BAD=before\x00after\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	err := LoadFile(path)
	if !errors.Is(err, ErrNUL) {
		t.Fatalf("LoadFile error = %v, want ErrNUL", err)
	}
	if value, ok := os.LookupEnv("DOTENV_TEST_SAFE"); ok {
		t.Fatalf("partial environment update occurred: DOTENV_TEST_SAFE=%q", value)
	}
	if _, ok := os.LookupEnv("DOTENV_TEST_BAD"); ok {
		t.Fatal("variable containing a NUL byte was applied")
	}
}

func TestParseRejectsEscapedNUL(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{name: "double quoted", env: "BAD=\"a\\0b\"\n"},
		{name: "unquoted", env: "BAD=a\\0b\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.env))
			if !errors.Is(err, ErrNUL) {
				t.Fatalf("Parse error = %v, want ErrNUL", err)
			}
			if got != nil {
				t.Fatalf("Parse returned a partial map: %#v", got)
			}
		})
	}
}

func TestSingleQuotedValuesDoNotDecodeEscapes(t *testing.T) {
	values, err := Parse([]byte(`VALUE='a\nb\0c\\d'`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := values["VALUE"], `a\nb\0c\\d`; got != want {
		t.Fatalf("VALUE = %q, want %q", got, want)
	}
}

func TestProtectedVariablesAreRejected(t *testing.T) {
	names := []string{
		"LD_PRELOAD",
		"LD_LIBRARY_PATH",
		"DYLD_INSERT_LIBRARIES",
		"DYLD_LIBRARY_PATH",
		"BASH_ENV",
		"ENV",
		"IFS",
		"PATH",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			values, err := Parse([]byte(name + "=value\n"))
			if !errors.Is(err, ErrProtected) {
				t.Fatalf("Parse error = %v, want ErrProtected", err)
			}
			if values != nil {
				t.Fatalf("Parse returned protected variables: %#v", values)
			}
		})
	}
}

func TestCallerProtectedSetIsRejected(t *testing.T) {
	values, err := Parse([]byte("CUSTOM_PROTECTED=value\nSAFE=value\n"), "CUSTOM_PROTECTED")
	if !errors.Is(err, ErrProtected) {
		t.Fatalf("Parse error = %v, want ErrProtected", err)
	}
	if values != nil {
		t.Fatalf("Parse returned a partial map: %#v", values)
	}
}

func TestProtectedVariablePreventsAllEnvMutation(t *testing.T) {
	clearEnv(t, "DOTENV_TEST_SAFE")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("DOTENV_TEST_SAFE=present\nPATH=/tmp/attacker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadFile(path); !errors.Is(err, ErrProtected) {
		t.Fatalf("LoadFile error = %v, want ErrProtected", err)
	}
	if _, ok := os.LookupEnv("DOTENV_TEST_SAFE"); ok {
		t.Fatal("partial environment update occurred")
	}
}

func TestBOMOnlyAllowedAtFileStart(t *testing.T) {
	const bom = "\xef\xbb\xbf"

	t.Run("leading BOM", func(t *testing.T) {
		values, err := Parse([]byte(bom + "KEY=value\n"))
		if err != nil {
			t.Fatal(err)
		}
		if values["KEY"] != "value" {
			t.Fatalf("KEY = %q, want value", values["KEY"])
		}
	})

	tests := []struct {
		name string
		data string
	}{
		{name: "after newline", data: "FIRST=1\n" + bom + "SECOND=2\n"},
		{name: "inside value", data: "KEY=pre" + bom + "post\n"},
		{name: "after leading whitespace", data: " " + bom + "KEY=value\n"},
		{name: "inside quoted value", data: "KEY=\"" + bom + "\"\n"},
		{name: "second BOM", data: bom + bom + "KEY=value\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := Parse([]byte(tt.data))
			if !errors.Is(err, ErrBOM) {
				t.Fatalf("Parse error = %v, want ErrBOM", err)
			}
			if values != nil {
				t.Fatalf("Parse returned a partial map: %#v", values)
			}
		})
	}
}

func TestQuotedEscapeDecodingAndMultilineValues(t *testing.T) {
	const data = `DQ="a\nb\\c\"d"
SQ='a\nb\\c"d'
MULTI="first
second
third"
`

	values, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := values["DQ"], "a\nb\\c\"d"; got != want {
		t.Fatalf("DQ = %q, want %q", got, want)
	}
	if got, want := values["SQ"], `a\nb\\c"d`; got != want {
		t.Fatalf("SQ = %q, want %q", got, want)
	}
	if got, want := values["MULTI"], "first\nsecond\nthird"; got != want {
		t.Fatalf("MULTI = %q, want %q", got, want)
	}
}

func TestExportPrefixAndCRLF(t *testing.T) {
	values, err := Parse([]byte("export FIRST=one\r\nexport\tSECOND=two\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if values["FIRST"] != "one" || values["SECOND"] != "two" {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestInvalidNamesAndSyntax(t *testing.T) {
	tests := []string{
		"1NAME=value\n",
		"BAD NAME=value\n",
		"=value\n",
		"NO_EQUALS\n",
		`NAME="unterminated` + "\n",
		`NAME='unterminated` + "\n",
		`NAME="value" trailing` + "\n",
	}

	for _, data := range tests {
		t.Run(strings.TrimSpace(data), func(t *testing.T) {
			if _, err := Parse([]byte(data)); !errors.Is(err, ErrInvalidSyntax) {
				t.Fatalf("Parse error = %v, want ErrInvalidSyntax", err)
			}
		})
	}
}

func TestFileAndLineLimits(t *testing.T) {
	maxSize := "K=" + strings.Repeat("a", MaxFileSize-2)
	if values, err := Parse([]byte(maxSize)); err != nil {
		t.Fatalf("exactly 64 KiB was rejected: %v", err)
	} else if len(values["K"]) != MaxFileSize-2 {
		t.Fatalf("parsed value length = %d", len(values["K"]))
	}

	if _, err := Parse([]byte(maxSize + "x")); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("64 KiB+1 error = %v, want ErrFileTooLarge", err)
	}

	var b strings.Builder
	for i := 0; i < MaxLines; i++ {
		fmt.Fprintf(&b, "K%d=%d\n", i, i)
	}
	if values, err := Parse([]byte(b.String())); err != nil {
		t.Fatalf("exactly 512 lines were rejected: %v", err)
	} else if len(values) != MaxLines {
		t.Fatalf("parsed %d values, want %d", len(values), MaxLines)
	}

	b.WriteString("K513=513\n")
	if _, err := Parse([]byte(b.String())); !errors.Is(err, ErrTooManyLines) {
		t.Fatalf("513-line error = %v, want ErrTooManyLines", err)
	}
}

func TestLoaderAppliesValidatedValues(t *testing.T) {
	clearEnv(t, "DOTENV_TEST_APPLIED")

	if err := LoadReader(strings.NewReader("DOTENV_TEST_APPLIED=yes\n")); err != nil {
		t.Fatal(err)
	}
	if value, ok := os.LookupEnv("DOTENV_TEST_APPLIED"); !ok || value != "yes" {
		t.Fatalf("DOTENV_TEST_APPLIED = %q, %v; want yes, true", value, ok)
	}
}

func clearEnv(t *testing.T, key string) {
	t.Helper()
	oldValue, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, oldValue)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
