package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

var testLimits = Limits{
	MaxInstructions: 64,
	MaxOutputLen:    1 << 20,
	MaxInsertBytes:  1 << 20,
}

// setupImage writes the old image into a fresh directory and opens it
// read-only, returning the reader, its length, and the directory.
func setupImage(t *testing.T, image []byte) (*os.File, uint64, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "old.img")
	if err := os.WriteFile(path, image, 0o444); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, uint64(len(image)), dir
}

func manifestFor(out []byte) Manifest {
	return Manifest{OutputLen: uint64(len(out)), SHA256: sha256.Sum256(out)}
}

// assertUnpublished checks that target does not exist, no in-flight
// temporary file is left behind, and the old image is unchanged.
func assertUnpublished(t *testing.T, dir, target string, old []byte) {
	t.Helper()
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target must stay invisible, stat err = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "old.img" {
			t.Fatalf("unexpected leftover file in target dir: %s", e.Name())
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "old.img"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatal("old image was modified")
	}
}

func TestApplyOverlappingCopyRanges(t *testing.T) {
	old := []byte("0123456789")
	script := []Instruction{
		{Op: OpCopy, Off: 0, Len: 4},       // "0123"
		{Op: OpCopy, Off: 2, Len: 6},       // "234567", overlaps previous range
		{Op: OpCopy, Off: 3, Len: 4},       // "3456", overlaps both
		{Op: OpInsert, Data: []byte("AB")}, // "AB"
		{Op: OpZero, Len: 3},               // "\x00\x00\x00"
		{Op: OpCopy, Off: 6, Len: 4},       // "6789"
	}
	want := []byte("0123" + "234567" + "3456" + "AB" + "\x00\x00\x00" + "6789")

	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	if err := Apply(f, n, script, manifestFor(want), testLimits, target); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
	// Old image must be byte-identical after a successful run.
	orig, err := os.ReadFile(filepath.Join(dir, "old.img"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, old) {
		t.Fatal("old image was modified")
	}
}

func TestCopyReadsOriginalImageOnly(t *testing.T) {
	old := []byte("0123456789")
	// The INSERT and ZERO before the COPY shift the output position; the
	// COPY source range must still resolve against the original image,
	// never against bytes already emitted this run.
	script := []Instruction{
		{Op: OpInsert, Data: []byte("XY")},
		{Op: OpZero, Len: 1},
		{Op: OpCopy, Off: 0, Len: 2}, // must yield "01" from the old image
	}
	want := []byte("XY\x0001")

	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	if err := Apply(f, n, script, manifestFor(want), testLimits, target); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestShortReadRejected(t *testing.T) {
	old := []byte("0123456789")
	script := []Instruction{
		{Op: OpCopy, Off: 8, Len: 4}, // extends 2 bytes past the end
	}
	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	err := Apply(f, n, script, Manifest{OutputLen: 4}, testLimits, target)
	if !errors.Is(err, ErrRange) {
		t.Fatalf("err = %v, want ErrRange", err)
	}
	assertUnpublished(t, dir, target, old)
}

func TestRangeOverflowRejected(t *testing.T) {
	old := []byte("0123456789")
	script := []Instruction{
		{Op: OpCopy, Off: math.MaxUint64 - 1, Len: 4}, // Off+Len wraps around
	}
	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	err := Apply(f, n, script, Manifest{OutputLen: 4}, testLimits, target)
	if !errors.Is(err, ErrRange) {
		t.Fatalf("err = %v, want ErrRange", err)
	}
	assertUnpublished(t, dir, target, old)
}

func TestFailingLastInstruction(t *testing.T) {
	old := []byte("0123456789")
	// Every instruction but the last is valid; the final COPY reads past
	// the end of the image. Validation must reject the whole script
	// before anything is written.
	script := []Instruction{
		{Op: OpCopy, Off: 0, Len: 10},
		{Op: OpInsert, Data: []byte("payload")},
		{Op: OpZero, Len: 8},
		{Op: OpCopy, Off: 9, Len: 2}, // short read: only 1 byte remains
	}
	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	err := Apply(f, n, script, Manifest{OutputLen: 27}, testLimits, target)
	if !errors.Is(err, ErrRange) {
		t.Fatalf("err = %v, want ErrRange", err)
	}
	assertUnpublished(t, dir, target, old)
}

func TestDigestMismatchNotPublished(t *testing.T) {
	old := []byte("0123456789")
	script := []Instruction{
		{Op: OpCopy, Off: 0, Len: 10},
		{Op: OpInsert, Data: []byte("XY")},
	}
	out := []byte("0123456789XY")
	m := manifestFor(out)
	m.SHA256[0] ^= 0xff // corrupt the bound digest

	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	err := Apply(f, n, script, m, testLimits, target)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	assertUnpublished(t, dir, target, old)
}

func TestOutputLenMismatchRejected(t *testing.T) {
	old := []byte("0123456789")
	script := []Instruction{{Op: OpCopy, Off: 0, Len: 10}}
	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	m := manifestFor([]byte("0123456789"))
	m.OutputLen++ // script and manifest disagree on length
	err := Apply(f, n, script, m, testLimits, target)
	if !errors.Is(err, ErrOutputLen) {
		t.Fatalf("err = %v, want ErrOutputLen", err)
	}
	assertUnpublished(t, dir, target, old)
}

func TestBudgetExceeded(t *testing.T) {
	old := []byte("0123456789")
	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")

	cases := []struct {
		name   string
		script []Instruction
		lim    Limits
	}{
		{"instructions", []Instruction{{Op: OpZero, Len: 1}, {Op: OpZero, Len: 1}},
			Limits{MaxInstructions: 1, MaxOutputLen: 16, MaxInsertBytes: 16}},
		{"output", []Instruction{{Op: OpZero, Len: 32}},
			Limits{MaxInstructions: 4, MaxOutputLen: 16, MaxInsertBytes: 16}},
		{"insert", []Instruction{{Op: OpInsert, Data: []byte("0123456789abcdef")}},
			Limits{MaxInstructions: 4, MaxOutputLen: 32, MaxInsertBytes: 8}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Apply(f, n, tc.script, Manifest{}, tc.lim, target)
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("err = %v, want ErrBudget", err)
			}
			assertUnpublished(t, dir, target, old)
		})
	}
}

func TestUnknownOpRejected(t *testing.T) {
	old := []byte("0123456789")
	script := []Instruction{{Op: Op(0xff)}}
	f, n, dir := setupImage(t, old)
	target := filepath.Join(dir, "new.img")
	if err := Apply(f, n, script, Manifest{}, testLimits, target); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("err = %v, want ErrUnknownOp", err)
	}
	assertUnpublished(t, dir, target, old)
}
