package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := t.TempDir() + "/framingcheck"
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper binary: %v\n%s", err, out)
	}
	return binary
}

func externalRequest(cl int, withTE bool, body []byte) []byte {
	var b bytes.Buffer
	b.WriteString("POST /x HTTP/1.1\r\nHost: h\r\n")
	if cl >= 0 {
		b.WriteString("Content-Length: ")
		b.WriteString(itoa(cl))
		b.WriteString("\r\n")
	}
	if withTE {
		b.WriteString("Transfer-Encoding: chunked\r\n")
	}
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes()
}

func TestExternalCLIBehavior(t *testing.T) {
	binary := buildBinary(t)

	run := func(data []byte) string {
		cmd := exec.Command(binary)
		cmd.Stdin = bytes.NewReader(data)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("binary failed: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	first := externalRequest(4, false, []byte("abcd"))
	second := externalRequest(13, true, []byte("0\r\n\r\nSMUGGLED"))
	got := run(append(append([]byte{}, first...), second...))
	secondHeadEnd := bytes.Index(second, []byte("\r\n\r\n")) + 4
	originEnd := len(first) + secondHeadEnd + 5
	gatewayEnd := len(first) + len(second)
	want := `{"start":` + itoa(len(first)) +
		`,"gatewayEnd":` + itoa(gatewayEnd) +
		`,"originEnd":` + itoa(originEnd) + `}`
	if got != want {
		t.Fatalf("pipeline finding = %q, want %q", got, want)
	}

	binaryBody := bytes.Repeat([]byte{0, 1, 2, 255}, 100)
	if got := run(externalRequest(len(binaryBody), false, binaryBody)); got != "OK" {
		t.Fatalf("binary body result = %q, want OK", got)
	}

	if got := run(nil); got != "OK" {
		t.Fatalf("empty stream = %q, want OK", got)
	}
}
