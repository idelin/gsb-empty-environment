package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestJSONEndToEnd: bytes may be JSON numbers or strings; big values must
// survive exactly; the output must be deterministic JSON.
func TestJSONEndToEnd(t *testing.T) {
	raw := `{
	  "files": [
	    {"id": "base", "bytes": "100000000000000000000", "corrupted": false, "dependencies": []},
	    {"id": "inc", "bytes": 7, "dependencies": ["base"]}
	  ],
	  "shards": [
	    {"name": "s1", "target": 2, "edges": [
	      {"from": 0, "to": 1, "file": "base"},
	      {"from": 1, "to": 2, "file": "inc"}
	    ]}
	  ],
	  "budget": 1000
	}`
	var in Input
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	out := solve(&in, 1000)
	if out.Status != StatusOptimal {
		t.Fatalf("status = %s", out.Status)
	}
	if out.Solution.TotalBytes != "100000000000000000007" {
		t.Fatalf("total = %s", out.Solution.TotalBytes)
	}
	buf, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf), `"totalBytes":"100000000000000000007"`) {
		t.Fatalf("output json: %s", buf)
	}

	// Negative byte counts are rejected at parse time.
	bad := `{"files":[{"id":"x","bytes":-5}]}`
	var bin Input
	if err := json.Unmarshal([]byte(bad), &bin); err == nil {
		t.Fatal("negative bytes must be rejected")
	}
}
