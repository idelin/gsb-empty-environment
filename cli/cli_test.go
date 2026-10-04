package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runScript feeds the script (one JSON request per line) and returns the
// decoded responses.
func runScript(t *testing.T, script string) []map[string]interface{} {
	t.Helper()
	var out bytes.Buffer
	if err := Run(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resps []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad response JSON %q: %v", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

func num(t *testing.T, m map[string]interface{}, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("response %v: key %q missing or not a number", m, key)
	}
	return v
}

func TestCLIProtocol(t *testing.T) {
	resps := runScript(t, `
{"cmd":"new","rows":5,"cols":5,"fill":0}
{"cmd":"set","row":2,"col":2,"value":1}
{"cmd":"query","row":0,"col":0}
{"cmd":"query","row":0,"col":0,"version":0}
{"cmd":"flip","row0":2,"col0":2,"row1":3,"col1":3}
{"cmd":"query","row":0,"col":0}
{"cmd":"query","row":0,"col":0,"version":1}
{"cmd":"dump","version":2}
{"cmd":"query","row":9,"col":0}
{"cmd":"bogus"}
`)
	if len(resps) != 10 {
		t.Fatalf("got %d responses, want 10", len(resps))
	}

	// new -> version 0.
	if resps[0]["ok"] != true || num(t, resps[0], "version") != 0 {
		t.Fatalf("new: %v", resps[0])
	}
	// set -> version 1, full recompute of 25 pixels, all 25 changed.
	if resps[1]["ok"] != true || num(t, resps[1], "version") != 1 {
		t.Fatalf("set: %v", resps[1])
	}
	if num(t, resps[1], "examinedPixels") != 25 || num(t, resps[1], "changedCount") != 25 {
		t.Fatalf("set stats: %v", resps[1])
	}
	// Latest query: (0,0) sees the unique 1 at (2,2), squared distance 8.
	if resps[2]["hasTarget"] != true || num(t, resps[2], "dist") != 8 {
		t.Fatalf("query latest: %v", resps[2])
	}
	src := resps[2]["source"].(map[string]interface{})
	if src["row"].(float64) != 2 || src["col"].(float64) != 2 {
		t.Fatalf("query source: %v", resps[2])
	}
	// Old snapshot: no target.
	if resps[3]["hasTarget"] != false || num(t, resps[3], "dist") != -1 {
		t.Fatalf("query v0: %v", resps[3])
	}
	// Flip deletes the unique opposite pixel: version 2, all 25 changed.
	if num(t, resps[4], "version") != 2 || num(t, resps[4], "changedCount") != 25 {
		t.Fatalf("flip: %v", resps[4])
	}
	// Latest is uniform again: no target.
	if resps[5]["hasTarget"] != false {
		t.Fatalf("query after delete: %v", resps[5])
	}
	// Version 1 still serves its old distances.
	if resps[6]["hasTarget"] != true || num(t, resps[6], "dist") != 8 {
		t.Fatalf("query v1: %v", resps[6])
	}
	// Dump of version 2: 25 cells, none with a target.
	if num(t, resps[7], "version") != 2 {
		t.Fatalf("dump: %v", resps[7])
	}
	cells := resps[7]["cells"].([]interface{})
	if len(cells) != 25 {
		t.Fatalf("dump cells: %d", len(cells))
	}
	for _, c := range cells {
		if c.(map[string]interface{})["hasTarget"] != false {
			t.Fatalf("dump cell has target: %v", c)
		}
	}
	// Errors are reported, not fatal.
	if resps[8]["ok"] != false || resps[9]["ok"] != false {
		t.Fatalf("error handling: %v %v", resps[8], resps[9])
	}
}

func TestCLINoOpFlip(t *testing.T) {
	resps := runScript(t, `
{"cmd":"new","rows":3,"cols":3,"fill":0}
{"cmd":"flip","row0":1,"col0":1,"row1":1,"col1":2}
{"cmd":"set","row":0,"col":0,"value":0}
`)
	if resps[1]["noOp"] != true || num(t, resps[1], "examinedPixels") != 0 {
		t.Fatalf("empty flip: %v", resps[1])
	}
	if num(t, resps[1], "version") != 0 {
		t.Fatalf("no-op must not create a version: %v", resps[1])
	}
	if resps[2]["noOp"] != true || num(t, resps[2], "version") != 0 {
		t.Fatalf("redundant set: %v", resps[2])
	}
}
