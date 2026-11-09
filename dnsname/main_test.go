package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMain(t *testing.T) {
	in := strings.NewReader(
		`{"hex":"0361626300","at":[0]}` + "\n" +
			`{"hex":"zz","at":[0]}` + "\n" +
			`not json` + "\n" +
			`{"hex":"c00f","at":[0]}` + "\n" +
			"\n",
	)
	var out, errOut bytes.Buffer
	if err := runMain(in, &out, &errOut); err != nil {
		t.Fatalf("runMain: %v", err)
	}

	want := strings.Join([]string{
		`[[["616263"],5]]`,
		`null`,
		`null`,
		`[null]`,
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("stdout:\n got %q\nwant %q", out.String(), want)
	}
	if !strings.Contains(errOut.String(), "invalid hex") ||
		!strings.Contains(errOut.String(), "invalid JSON") {
		t.Fatalf("expected malformed-line diagnostics, got %q", errOut.String())
	}
}
