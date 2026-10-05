package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bpmatch/internal/matcher"
)

const sampleInput = `{
  "tasks": ["a", "b"],
  "employees": ["x", "y"],
  "edges": [
    {"task": "a", "employee": "x", "profit": -1},
    {"task": "a", "employee": "y", "profit": 2},
    {"task": "b", "employee": "y", "profit": 3}
  ]
}`

func solveString(t *testing.T, in string) *matcher.ResultJSON {
	t.Helper()
	var out bytes.Buffer
	if err := run([]string{"solve"}, strings.NewReader(in), &out); err != nil {
		t.Fatalf("solve: %v", err)
	}
	var rj matcher.ResultJSON
	if err := json.Unmarshal(out.Bytes(), &rj); err != nil {
		t.Fatalf("decoding solve output: %v\n%s", err, out.String())
	}
	return &rj
}

func TestCLISolve(t *testing.T) {
	rj := solveString(t, sampleInput)
	// Max cardinality 2 forces the negative edge a-x.
	if rj.Cardinality != 2 || rj.TotalProfit != 2 {
		t.Fatalf("got (%d,%d), want (2,2)", rj.Cardinality, rj.TotalProfit)
	}
	if len(rj.Assignment) != 2 || rj.Assignment[0] == nil || *rj.Assignment[0] != "x" ||
		rj.Assignment[1] == nil || *rj.Assignment[1] != "y" {
		t.Fatalf("unexpected assignment: %+v", rj.Assignment)
	}
	if len(rj.MinVertexCover.Tasks)+len(rj.MinVertexCover.Employees) != 2 {
		t.Fatalf("cover size != cardinality: %+v", rj.MinVertexCover)
	}
}

func TestCLIUpdateAndVerify(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.json")
	solPath := filepath.Join(dir, "sol.json")
	if err := os.WriteFile(inPath, []byte(sampleInput), 0o644); err != nil {
		t.Fatal(err)
	}
	// Add edge b-x:10, then the optimum becomes a-y(2) + b-x(10) = 12.
	var out bytes.Buffer
	if err := run([]string{"update", "-in", inPath, "-add", "b=x:10"}, nil, &out); err != nil {
		t.Fatalf("update -add: %v", err)
	}
	var payload struct {
		Input  matcher.Instance   `json:"input"`
		Result matcher.ResultJSON `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decoding update output: %v", err)
	}
	if payload.Result.Cardinality != 2 || payload.Result.TotalProfit != 12 {
		t.Fatalf("after add: got (%d,%d), want (2,12)",
			payload.Result.Cardinality, payload.Result.TotalProfit)
	}
	// Persist the updated input, delete the edge again, and confirm the
	// result matches a fresh solve of the original instance.
	updated, err := json.Marshal(payload.Input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inPath, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"update", "-in", inPath, "-del", "b=x"}, nil, &out); err != nil {
		t.Fatalf("update -del: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Result.Cardinality != 2 || payload.Result.TotalProfit != 2 {
		t.Fatalf("after del: got (%d,%d), want (2,2)",
			payload.Result.Cardinality, payload.Result.TotalProfit)
	}
	// Write the solution and verify it against the original input.
	solData, err := json.Marshal(payload.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(solPath, solData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inPath, []byte(sampleInput), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"verify", "-in", inPath, "-sol", solPath}, nil, &out); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(out.String(), "OK") {
		t.Fatalf("unexpected verify output: %q", out.String())
	}
	// A tampered solution must be rejected.
	payload.Result.TotalProfit = 99
	bad, err := json.Marshal(payload.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(solPath, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-in", inPath, "-sol", solPath}, nil, &out); err == nil {
		t.Fatal("tampered solution passed verification")
	}
}

func TestCLIUpdateRequiresOneOp(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"update"}, strings.NewReader(sampleInput), &out); err == nil {
		t.Fatal("expected error without -add/-del")
	}
	if err := run([]string{"update", "-add", "a=x:1", "-del", "a=x"}, strings.NewReader(sampleInput), &out); err == nil {
		t.Fatal("expected error with both -add and -del")
	}
}
