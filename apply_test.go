package binpatch

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io/ioutil"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile is a small test helper.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// applyToDir applies patch to baseline and returns the published bytes.
func applyToDir(t *testing.T, dir string, baseline, patch []byte) ([]byte, error) {
	t.Helper()
	basePath := filepath.Join(dir, "baseline.bin")
	dstPath := filepath.Join(dir, "out.bin")
	writeFile(t, basePath, baseline)
	err := ApplyFile(basePath, bytes.NewReader(patch), dstPath)
	if err != nil {
		return nil, err
	}
	out, err := ioutil.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	return out, nil
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "binpatch-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// randomBytes returns n crypto-random bytes.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// mutateTarget derives a target from baseline by applying random structural
// edits: chunk copies, deletions, insertions, and replacements.
func mutateTarget(rng *mathrand.Rand, baseline []byte) []byte {
	var out []byte
	for len(out) < cap(baseline)+rng.Intn(512) || rng.Intn(4) != 0 {
		if len(out) > 3*len(baseline)+1024 {
			break
		}
		switch rng.Intn(4) {
		case 0, 1: // copy a random baseline chunk
			if len(baseline) == 0 {
				continue
			}
			off := rng.Intn(len(baseline))
			n := 1 + rng.Intn(len(baseline)-off)
			if n > 256 {
				n = 256
			}
			out = append(out, baseline[off:off+n]...)
		case 2: // insert fresh random bytes
			n := 1 + rng.Intn(128)
			b := make([]byte, n)
			rng.Read(b)
			out = append(out, b...)
		case 3: // duplicate a chunk of what we already produced
			if len(out) == 0 {
				continue
			}
			off := rng.Intn(len(out))
			n := 1 + rng.Intn(len(out)-off)
			if n > 128 {
				n = 128
			}
			dup := append([]byte(nil), out[off:off+n]...)
			out = append(out, dup...)
		}
		if len(baseline) == 0 && rng.Intn(8) == 0 {
			break
		}
	}
	return out
}

// TestRoundTripRandom diffs random baselines against randomly mutated
// targets, applies the encoded patch, and requires an exact reproduction.
func TestRoundTripRandom(t *testing.T) {
	rng := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	for iter := 0; iter < 60; iter++ {
		baseline := randomBytes(t, rng.Intn(4096))
		target := mutateTarget(rng, baseline)
		patch, err := PatchBytes(baseline, target)
		if err != nil {
			t.Fatalf("iter %d: diff: %v", iter, err)
		}
		got, err := applyToDir(t, dir, baseline, patch)
		if err != nil {
			t.Fatalf("iter %d: apply: %v", iter, err)
		}
		if !bytes.Equal(got, target) {
			t.Fatalf("iter %d: round trip mismatch: got %d bytes, want %d",
				iter, len(got), len(target))
		}
	}
}

// TestCopiesReadBaselineOnly pins the semantics that copy offsets always
// address the original baseline, never the output produced so far.
func TestCopiesReadBaselineOnly(t *testing.T) {
	baseline := []byte("abcd")
	target := []byte("abcdabcd")
	h := Header{
		BaselineHash: sha256.Sum256(baseline),
		TargetLength: uint64(len(target)),
		TargetHash:   sha256.Sum256(target),
	}
	// Two copies of the same baseline range; an implementation reading from
	// its own output would have to use shifting offsets instead.
	ops := []Op{
		{Offset: 0, Length: 4},
		{Offset: 0, Length: 4},
	}
	var buf bytes.Buffer
	if err := Encode(&buf, h, ops); err != nil {
		t.Fatal(err)
	}
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	got, err := applyToDir(t, dir, baseline, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatalf("got %q, want %q", got, target)
	}
}

// TestEmptyBaselineAndTarget covers the degenerate all-insert and
// zero-operation patches.
func TestEmptyBaselineAndTarget(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	// Empty baseline, pure-insert target.
	target := randomBytes(t, 100)
	patch, err := PatchBytes(nil, target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := applyToDir(t, dir, nil, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatal("empty-baseline round trip mismatch")
	}

	// Non-empty baseline, empty target: zero operations.
	baseline := randomBytes(t, 64)
	patch, err = PatchBytes(baseline, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = applyToDir(t, dir, baseline, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(got))
	}
}

// locateCopyOps returns byte offsets of copy-op offset/length fields inside
// an encoded patch, so tests can corrupt them precisely.
func locateCopyOps(t *testing.T, patch []byte) (offField, lenField int) {
	t.Helper()
	const headerLen = 8 + 32 + 8 + 32 + 8
	pos := headerLen
	for pos < len(patch) {
		tag := patch[pos]
		pos++
		switch tag {
		case tagCopy:
			return pos, pos + 8
		case tagInsert:
			n := binary.BigEndian.Uint64(patch[pos:])
			pos += 8 + int(n)
		default:
			t.Fatalf("unexpected tag %d while scanning", tag)
		}
	}
	t.Fatal("no copy op found in patch")
	return 0, 0
}

// patchWithCopy builds a patch against baseline that contains at least one
// copy operation.
func patchWithCopy(t *testing.T, baseline []byte) []byte {
	t.Helper()
	if len(baseline) < 2*blockSize {
		t.Fatal("baseline too small")
	}
	target := append(append([]byte("hdr"), baseline...), []byte("ftr")...)
	patch, err := PatchBytes(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	return patch
}

// TestOutOfBoundsCopy corrupts copy offsets/lengths beyond the baseline and
// requires ErrOutOfBounds with nothing published.
func TestOutOfBoundsCopy(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	baseline := randomBytes(t, 512)
	basePath := filepath.Join(dir, "baseline.bin")
	dstPath := filepath.Join(dir, "out.bin")
	writeFile(t, basePath, baseline)

	corrupt := func(mutate func(p []byte)) []byte {
		p := patchWithCopy(t, baseline)
		mutate(p)
		return p
	}
	putU64 := func(p []byte, at int, v uint64) {
		binary.BigEndian.PutUint64(p[at:], v)
	}

	cases := map[string][]byte{
		"offset beyond baseline": corrupt(func(p []byte) {
			off, _ := locateCopyOps(t, p)
			putU64(p, off, uint64(len(baseline))+1000)
		}),
		"length beyond baseline": corrupt(func(p []byte) {
			_, ln := locateCopyOps(t, p)
			putU64(p, ln, uint64(len(baseline))+1000)
		}),
		"offset+length overflow": corrupt(func(p []byte) {
			off, ln := locateCopyOps(t, p)
			putU64(p, off, ^uint64(0)-3)
			putU64(p, ln, 10)
		}),
		"end exactly past limit": corrupt(func(p []byte) {
			off, ln := locateCopyOps(t, p)
			putU64(p, off, uint64(len(baseline))-1)
			putU64(p, ln, 2)
		}),
	}
	for name, patch := range cases {
		os.Remove(dstPath)
		err := ApplyFile(basePath, bytes.NewReader(patch), dstPath)
		if err != ErrOutOfBounds {
			t.Errorf("%s: got error %v, want ErrOutOfBounds", name, err)
		}
		if _, statErr := os.Stat(dstPath); !os.IsNotExist(statErr) {
			t.Errorf("%s: partial output was published", name)
		}
	}
}

// TestTruncatedPatch cuts a valid patch at every possible length and
// requires an error and no published output each time.
func TestTruncatedPatch(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	baseline := randomBytes(t, 300)
	target := append(append(randomBytes(t, 50), baseline...), randomBytes(t, 20)...)
	full, err := PatchBytes(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(dir, "baseline.bin")
	dstPath := filepath.Join(dir, "out.bin")
	writeFile(t, basePath, baseline)

	for cut := 0; cut < len(full); cut++ {
		os.Remove(dstPath)
		err := ApplyFile(basePath, bytes.NewReader(full[:cut]), dstPath)
		if err == nil {
			t.Fatalf("cut at %d: apply unexpectedly succeeded", cut)
		}
		if _, statErr := os.Stat(dstPath); !os.IsNotExist(statErr) {
			t.Fatalf("cut at %d: partial output was published", cut)
		}
	}
}

// TestCorruptHeader flips bytes in each header field and requires the
// matching verification error.
func TestCorruptHeader(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	baseline := randomBytes(t, 256)
	target := randomBytes(t, 256)
	full, err := PatchBytes(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(dir, "baseline.bin")
	dstPath := filepath.Join(dir, "out.bin")
	writeFile(t, basePath, baseline)

	const (
		magicOff   = 0
		baseHashAt = 8
		targetLen  = 8 + 32
		targetHash = 8 + 32 + 8
	)
	cases := []struct {
		name string
		at   int
		want error
	}{
		{"magic", magicOff, ErrBadMagic},
		{"baseline hash", baseHashAt, ErrBaselineHash},
		{"target length", targetLen, ErrTargetLength},
		{"target hash", targetHash, ErrTargetHash},
	}
	for _, c := range cases {
		patch := append([]byte(nil), full...)
		patch[c.at] ^= 0xFF
		os.Remove(dstPath)
		err := ApplyFile(basePath, bytes.NewReader(patch), dstPath)
		if err != c.want {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		if _, statErr := os.Stat(dstPath); !os.IsNotExist(statErr) {
			t.Errorf("%s: partial output was published", c.name)
		}
	}
}

// TestUnknownOpAndTrailingData covers malformed operation streams.
func TestUnknownOpAndTrailingData(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	baseline := randomBytes(t, 128)
	target := randomBytes(t, 128)
	full, err := PatchBytes(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(dir, "baseline.bin")
	dstPath := filepath.Join(dir, "out.bin")
	writeFile(t, basePath, baseline)

	// Unknown operation tag: corrupt the first op's tag byte.
	bad := append([]byte(nil), full...)
	bad[8+32+8+32+8] = 0x7F
	if err := ApplyFile(basePath, bytes.NewReader(bad), dstPath); err == nil ||
		!strings.Contains(err.Error(), ErrUnknownOp.Error()) {
		t.Errorf("unknown tag: got %v", err)
	}

	// Trailing garbage after the final operation.
	bad = append(append([]byte(nil), full...), 0x00)
	if err := ApplyFile(basePath, bytes.NewReader(bad), dstPath); err != ErrExtraData {
		t.Errorf("trailing data: got %v, want ErrExtraData", err)
	}
}

// TestAtomicPublish verifies that a pre-existing destination survives a
// failed apply untouched, that no staging files leak, and that a successful
// apply atomically replaces it.
func TestAtomicPublish(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	baseline := randomBytes(t, 256)
	target := randomBytes(t, 512)
	good, err := PatchBytes(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(dir, "baseline.bin")
	dstPath := filepath.Join(dir, "out.bin")
	writeFile(t, basePath, baseline)

	original := []byte("pre-existing destination content")
	writeFile(t, dstPath, original)

	// Failing apply: destination must be byte-for-byte intact.
	bad := append([]byte(nil), good...)
	bad[8] ^= 0xFF // corrupt baseline hash
	if err := ApplyFile(basePath, bytes.NewReader(bad), dstPath); err != ErrBaselineHash {
		t.Fatalf("got %v, want ErrBaselineHash", err)
	}
	got, err := ioutil.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("failed apply modified the existing destination")
	}

	// No staging files may remain in the directory.
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("staging file leaked: %s", e.Name())
		}
	}

	// Successful apply replaces the destination with exactly the target.
	if err := ApplyFile(basePath, bytes.NewReader(good), dstPath); err != nil {
		t.Fatal(err)
	}
	got, err = ioutil.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatal("published content does not match target")
	}
}

// TestStreamingInsertSource ensures the applier streams patches from a
// reader rather than requiring the whole patch in memory: a reader that
// fails after the patch bytes must not break a successful apply, and one
// that truncates mid-insert must fail.
func TestStreamingInsertSource(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	baseline := randomBytes(t, 64)
	target := randomBytes(t, 4096) // forces large insert payloads
	patch, err := PatchBytes(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(dir, "baseline.bin")
	writeFile(t, basePath, baseline)

	got, err := applyToDir(t, dir, baseline, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatal("streamed apply mismatch")
	}
}
