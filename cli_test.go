package main

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) []string {
	t.Helper()
	var paths []string
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func TestEndToEndCLI(t *testing.T) {
	dir := t.TempDir()
	trainPaths := writeFiles(t, dir, map[string]string{
		"a.txt": "the quick brown fox jumps over the lazy dog. the quick brown fox is quick.",
		"b.txt": "the lazy dog sleeps. the quick brown fox runs. the lazy dog dreams.",
		"c.txt": "quick brown fox? the lazy dog barks at the quick brown fox.",
	})
	validPaths := writeFiles(t, dir, map[string]string{
		"v1.txt": "the quick brown fox meets the lazy dog again.",
		"v2.txt": "the lazy dog and the quick brown fox are friends.",
	})
	dictPath := filepath.Join(dir, "dict.bin")

	var out bytes.Buffer
	args := []string{"train", "-mode", "heuristic", "-budget", "256", "-o", dictPath}
	for _, v := range validPaths {
		args = append(args, "-valid", v)
	}
	args = append(args, trainPaths...)
	if code := run(args, &out, &out); code != 0 {
		t.Fatalf("train failed: %s", out.String())
	}
	for _, want := range []string{"mode: heuristic", "train:", "valid:", "dictionary:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("train output missing %q:\n%s", want, out.String())
		}
	}

	// Exact mode on a tiny input reports optimality.
	tiny := writeFiles(t, dir, map[string]string{
		"t1.txt": "abcabcabcabcabcabcabcabcabcabc",
		"t2.txt": "abcabcabcabcabc",
	})
	out.Reset()
	args = []string{"train", "-mode", "exact", "-budget", "256", "-minlen", "3", "-maxlen", "6"}
	args = append(args, tiny...)
	if code := run(args, &out, &out); code != 0 {
		t.Fatalf("exact train failed: %s", out.String())
	}
	if !strings.Contains(out.String(), "mode: exact") {
		t.Fatalf("exact mode output missing:\n%s", out.String())
	}

	// Encode and decode a validation file; decoded bytes must match exactly.
	encPath := filepath.Join(dir, "v1.dcs")
	out.Reset()
	if code := run([]string{"encode", "-dict", dictPath, "-o", encPath, validPaths[0]}, &out, &out); code != 0 {
		t.Fatalf("encode failed")
	}
	decPath := filepath.Join(dir, "v1.out")
	out.Reset()
	if code := run([]string{"decode", "-dict", dictPath, "-maxout", "4096", "-o", decPath, encPath}, &out, &out); code != 0 {
		t.Fatalf("decode failed")
	}
	orig, _ := os.ReadFile(validPaths[0])
	dec, _ := os.ReadFile(decPath)
	if !bytes.Equal(orig, dec) {
		t.Fatalf("roundtrip mismatch")
	}

	// Decode with a too-small bound must fail.
	out.Reset()
	if code := run([]string{"decode", "-dict", dictPath, "-maxout", "2", "-o", decPath, encPath}, &out, &out); code == 0 {
		t.Fatalf("decode with tiny maxout should fail")
	}

	// Eval reports ratios including dictionary overhead.
	out.Reset()
	if code := run([]string{"eval", "-dict", dictPath, validPaths[0], validPaths[1]}, &out, &out); code != 0 {
		t.Fatalf("eval failed")
	}
	if !strings.Contains(out.String(), "eval:") {
		t.Fatalf("eval output missing report:\n%s", out.String())
	}
}

func TestTrainHeuristicAndNoBenefitMessage(t *testing.T) {
	dir := t.TempDir()
	// Highly repetitive train data.
	repetitive := strings.Repeat("lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 20)
	trainPaths := writeFiles(t, dir, map[string]string{
		"t1.txt": repetitive,
		"t2.txt": repetitive + repetitive,
	})
	var out bytes.Buffer
	args := []string{"train", "-mode", "heuristic", "-budget", "512"}
	args = append(args, trainPaths...)
	if code := run(args, &out, &out); code != 0 {
		t.Fatalf("train failed: %s", out.String())
	}
	if !strings.Contains(out.String(), "upper bound") {
		t.Fatalf("heuristic mode must report an upper bound:\n%s", out.String())
	}

	// Random data: honest no-benefit reporting.
	rnd := make([]byte, 600)
	rand.New(rand.NewSource(9)).Read(rnd)
	randPath := filepath.Join(dir, "rand.bin")
	os.WriteFile(randPath, rnd, 0644)
	out.Reset()
	if code := run([]string{"train", "-mode", "heuristic", "-budget", "512", randPath}, &out, &out); code != 0 {
		t.Fatalf("train failed: %s", out.String())
	}
	if !strings.Contains(out.String(), "no dictionary fits") && !strings.Contains(out.String(), "not worthwhile") {
		t.Fatalf("expected honest no-benefit note:\n%s", out.String())
	}
}
