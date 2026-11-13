package netsec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShellQuoteSingleQuoteRule(t *testing.T) {
	if got, want := ShellQuote("a'b"), `'a'\''b'`; got != want {
		t.Errorf("ShellQuote = %q, want %q", got, want)
	}
	if got, want := EscapeSingleQuotes(`a'b'c`), `a'\''b'\''c`; got != want {
		t.Errorf("EscapeSingleQuotes = %q, want %q", got, want)
	}
}

func TestShellQuotePreventsCommandSubstitution(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "should-not-exist")
	output := filepath.Join(dir, "quoted-value")
	value := "a'b $(touch " + marker + ") `touch " + marker + "`\nend"

	command := fmt.Sprintf("printf '%%s' %s > %s", ShellQuote(value), ShellQuote(output))
	if out, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("shell command failed: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker exists: command substitution escaped quoting")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != value {
		t.Errorf("quoted value = %q, want %q", data, value)
	}
}
