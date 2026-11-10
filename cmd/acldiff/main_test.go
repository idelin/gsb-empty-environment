package main

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "acldiff")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	return binary
}

func runCLI(t *testing.T, binary string, args []string, input string) ([][3]string, string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var got [][3]string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout.String())
	}
	return got, stderr.String()
}

func blockAt(blocks [][3]string, addr string) ([3]string, bool) {
	parsed := netip.MustParseAddr(addr)
	value := uint32(parsed.As4()[0])<<24 | uint32(parsed.As4()[1])<<16 |
		uint32(parsed.As4()[2])<<8 | uint32(parsed.As4()[3])
	for _, block := range blocks {
		prefix := netip.MustParsePrefix(block[0])
		base := prefix.Masked().Addr().As4()
		baseValue := uint32(base[0])<<24 | uint32(base[1])<<16 |
			uint32(base[2])<<8 | uint32(base[3])
		size := uint64(1) << uint(32-prefix.Bits())
		if uint64(value) >= uint64(baseValue) &&
			uint64(value) < uint64(baseValue)+size {
			return block, true
		}
	}
	return [3]string{}, false
}

func TestCLIEndToEnd(t *testing.T) {
	binary := buildBinary(t)
	input := `{
		"old": [
			["10.1.2.3/24", "allow"],
			["10.1.0.0/16", "deny"],
			["10.0.0.0/8", "allow"],
			["10.1.0.0/16", "deny"],
			["10.1.2.3/24", "allow"]
		],
		"new": [
			["10.0.0.0/8", "allow"],
			["192.168.0.0/16", "allow"]
		]
	}`

	blocks, stderr := runCLI(t, binary, []string{"-verify", "-samples", "2000"}, input)
	if !strings.Contains(stderr, "verification ok") {
		t.Fatalf("want verification note on stderr, got %q", stderr)
	}

	cases := []struct {
		addr     string
		old, new string
	}{
		{"10.1.0.1", "deny", "allow"},
		{"10.1.1.1", "deny", "allow"},
		{"10.1.2.3", "", ""},
		{"10.2.0.1", "", ""},
		{"192.168.1.1", "deny", "allow"},
		{"8.8.8.8", "", ""},
	}
	for _, tc := range cases {
		block, ok := blockAt(blocks, tc.addr)
		if tc.old == "" {
			if ok {
				t.Fatalf("%s should be unchanged, found block %v", tc.addr, block)
			}
			continue
		}
		if !ok {
			t.Fatalf("%s missing from blocks: %v", tc.addr, blocks)
		}
		if block[1] != tc.old || block[2] != tc.new {
			t.Fatalf("%s block = %v, want (%s,%s)", tc.addr, block, tc.old, tc.new)
		}
	}

	var previous uint64
	for _, block := range blocks {
		prefix := netip.MustParsePrefix(block[0])
		base := prefix.Masked().Addr().As4()
		start := uint64(base[0])<<24 | uint64(base[1])<<16 |
			uint64(base[2])<<8 | uint64(base[3])
		if start <= previous && previous != 0 {
			t.Fatalf("blocks not sorted by start address: %v", blocks)
		}
		previous = start
	}
}

func TestCLIAcceptsEmptyRules(t *testing.T) {
	binary := buildBinary(t)
	blocks, _ := runCLI(t, binary, nil, `{"old":[],"new":[]}`)
	if len(blocks) != 0 {
		t.Fatalf("want empty array, got %v", blocks)
	}
}

func TestCLIRejectsInvalidAction(t *testing.T) {
	binary := buildBinary(t)
	cmd := exec.Command(binary)
	cmd.Stdin = strings.NewReader(`{"old":[["10.0.0.0/8","permit"]],"new":[]}`)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, output=%s", output)
	}
	if !strings.Contains(string(output), "invalid action") {
		t.Fatalf("want invalid action error, got %s", output)
	}
}
