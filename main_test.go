package main

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func mustHex(t *testing.T, b []byte) string {
	t.Helper()
	return hex.EncodeToString(b)
}

func decodeResult(t *testing.T, raw any) []any {
	t.Helper()
	buf, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out []any
	if err := json.Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestProcessLineValidNamesAndPointers(t *testing.T) {
	// 0: foo com 0 ; 9: <ptr 0>
	packet := []byte{3, 'f', 'o', 'o', 3, 'c', 'o', 'm', 0}
	packet = append(packet, 0xC0, 0x00)

	line := []byte(`{"hex":"` + mustHex(t, packet) + `","at":[0,9,-1,11]}`)
	out, err := processLine(line)
	if err != nil {
		t.Fatalf("processLine: %v", err)
	}
	results := decodeResult(t, out)
	if len(results) != 4 {
		t.Fatalf("results = %v", results)
	}

	first := results[0].([]any)
	labels := first[0].([]any)
	if len(labels) != 2 || labels[0].([]any)[0] != "66" || first[1].(float64) != 9 {
		t.Fatalf("entry 0 = %v", first)
	}

	second := results[1].([]any)
	if second[1].(float64) != 11 {
		t.Fatalf("pointer next offset = %v, want 11 (not target 0)", second[1])
	}
	if results[2] != nil || results[3] != nil {
		t.Fatalf("out-of-bounds entries must be null: %v", results)
	}
}

func TestProcessLineMaliciousPacket(t *testing.T) {
	// Self loop plus a valid root name in one packet.
	packet := []byte{0xC0, 0x00, 0x00}
	line := []byte(`{"hex":"` + mustHex(t, packet) + `","at":[0,2]}`)
	out, err := processLine(line)
	if err != nil {
		t.Fatalf("processLine: %v", err)
	}
	results := decodeResult(t, out)
	if results[0] != nil {
		t.Fatalf("loop must be null: %v", results[0])
	}
	root := results[1].([]any)
	if root[1].(float64) != 3 {
		t.Fatalf("root next = %v, want 3", root[1])
	}
}

func TestProcessLineInvalidHexAndJSON(t *testing.T) {
	out, err := processLine([]byte(`{"hex":"zz","at":[0]}`))
	if err != nil {
		t.Fatalf("invalid hex should not error: %v", err)
	}
	results := decodeResult(t, out)
	if len(results) != 1 || results[0] != nil {
		t.Fatalf("invalid hex entry = %v", results)
	}

	if _, err := processLine([]byte(`{not json`)); err == nil {
		t.Fatal("malformed JSON must return an error")
	}
}

func TestProcessLineEmptyAt(t *testing.T) {
	out, err := processLine([]byte(`{"hex":"00","at":[]}`))
	if err != nil {
		t.Fatalf("processLine: %v", err)
	}
	results := decodeResult(t, out)
	if len(results) != 0 {
		t.Fatalf("empty at must produce [], got %v", results)
	}
}

func TestHexWithWhitespace(t *testing.T) {
	out, err := processLine([]byte(`{"hex":"03 66 6f\n6f 00","at":[0]}`))
	if err != nil {
		t.Fatalf("processLine: %v", err)
	}
	results := decodeResult(t, out)
	entry := results[0].([]any)
	if entry[1].(float64) != 5 {
		t.Fatalf("whitespace hex entry = %v", entry)
	}
}
