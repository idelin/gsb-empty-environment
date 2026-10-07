package binpatch

import (
	"bytes"
	"crypto/sha256"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "binpatch-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func randomBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func sha256Of(b []byte) [sha256.Size]byte { return sha256.Sum256(b) }

// referenceTarget reconstructs the target from ops using only the baseline,
// mirroring the spec: copies read the baseline, inserts append literals.
func referenceTarget(baseline []byte, ops []Op) []byte {
	var out []byte
	for _, op := range ops {
		switch op.Kind {
		case OpCopy:
			out = append(out, baseline[op.Offset:op.Offset+op.Length]...)
		case OpInsert:
			out = append(out, op.Data...)
		}
	}
	return out
}

// randomOps builds a random but always in-bounds op sequence and the target
// it reconstructs.
func randomOps(rng *rand.Rand, baseline []byte) ([]Op, []byte) {
	var ops []Op
	var target []byte
	n := 1 + rng.Intn(12)
	for i := 0; i < n; i++ {
		if len(baseline) > 0 && rng.Intn(2) == 0 {
			off := rng.Intn(len(baseline) + 1)
			length := rng.Intn(len(baseline) - off + 1)
			ops = append(ops, Copy(uint64(off), uint64(length)))
			target = append(target, baseline[off:off+length]...)
		} else {
			data := randomBytes(rng, rng.Intn(64))
			ops = append(ops, Insert(data))
			target = append(target, data...)
		}
	}
	return ops, target
}

func patchFor(baseline, target []byte, ops []Op) *Patch {
	return &Patch{
		BaselineHash: sha256Of(baseline),
		TargetLength: uint64(len(target)),
		TargetHash:   sha256Of(target),
		Ops:          ops,
	}
}

func applyToFile(t *testing.T, baseline []byte, patch *Patch, dir string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	if err := patch.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	target := filepath.Join(dir, "target.bin")
	err := Apply(bytes.NewReader(baseline), target, &buf)
	return target, err
}

func TestRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		baseline := randomBytes(rng, rng.Intn(512))
		ops, target := randomOps(rng, baseline)
		patch := patchFor(baseline, target, ops)

		dir := tempDir(t)
		defer os.RemoveAll(dir)
		targetPath, err := applyToFile(t, baseline, patch, dir)
		if err != nil {
			t.Fatalf("trial %d: apply: %v", trial, err)
		}
		got, err := ioutil.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("trial %d: read target: %v", trial, err)
		}
		if !bytes.Equal(got, target) {
			t.Fatalf("trial %d: target mismatch: got %d bytes, want %d", trial, len(got), len(target))
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	baseline := randomBytes(rng, 256)
	ops, target := randomOps(rng, baseline)
	patch := patchFor(baseline, target, ops)

	var buf bytes.Buffer
	if err := patch.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := Decode(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.BaselineHash != patch.BaselineHash ||
		decoded.TargetHash != patch.TargetHash ||
		decoded.TargetLength != patch.TargetLength ||
		len(decoded.Ops) != len(patch.Ops) {
		t.Fatal("decoded patch header/ops differ")
	}
	for i, op := range decoded.Ops {
		want := patch.Ops[i]
		if op.Kind != want.Kind || op.Offset != want.Offset || op.Length != want.Length ||
			!bytes.Equal(op.Data, want.Data) {
			t.Fatalf("op %d differs after round trip", i)
		}
	}
}

// TestCopyReadsBaselineOnly proves copy ops never read the generated output:
// the inserted bytes occupy the output offsets that the copy would hit, so
// an implementation reading its own output would produce "BBBB" instead of
// "BBAA".
func TestCopyReadsBaselineOnly(t *testing.T) {
	baseline := []byte("AAAA")
	ops := []Op{Insert([]byte("BB")), Copy(0, 2)}
	target := referenceTarget(baseline, ops)
	patch := patchFor(baseline, target, ops)

	dir := tempDir(t)
	defer os.RemoveAll(dir)
	targetPath, err := applyToFile(t, baseline, patch, dir)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := ioutil.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("BBAA"); !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q (copy must read baseline, not output)", got, want)
	}
}

func TestOutOfBoundsCopy(t *testing.T) {
	baseline := []byte("0123456789")
	cases := []Op{
		Copy(10, 1),                    // offset at end, nonzero length
		Copy(0, 11),                    // length past end
		Copy(9, 2),                     // straddles end
		Copy(1<<62, 1<<62),             // overflow-safe bounds check
		Copy(uint64(len(baseline)), 0), // zero length at end is fine, must not fail
	}
	for i, op := range cases[:4] {
		target := referenceTarget(baseline, []Op{Insert([]byte("x"))})
		patch := patchFor(baseline, target, []Op{op})
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		targetPath, err := applyToFile(t, baseline, patch, dir)
		if err == nil {
			t.Fatalf("case %d: expected error, got none", i)
		}
		oe, ok := err.(*OpError)
		if !ok || oe.Err != ErrOutOfBounds {
			t.Fatalf("case %d: got %v, want OpError wrapping ErrOutOfBounds", i, err)
		}
		if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
			t.Fatalf("case %d: partial target published at %s", i, targetPath)
		}
	}
	// Zero-length copy at end of baseline is in bounds.
	target := referenceTarget(baseline, []Op{cases[4]})
	patch := patchFor(baseline, target, []Op{cases[4]})
	if _, err := applyToFile(t, baseline, patch, tempDir(t)); err != nil {
		t.Fatalf("zero-length copy at end should succeed: %v", err)
	}
}

func TestTruncatedPatch(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	baseline := randomBytes(rng, 128)
	ops, target := randomOps(rng, baseline)
	patch := patchFor(baseline, target, ops)

	var buf bytes.Buffer
	if err := patch.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	full := buf.Bytes()
	// Every strict prefix must fail to apply and must not publish a target.
	for _, n := range []int{0, 1, len(magic) - 1, len(magic), len(magic) + 10,
		len(full) / 2, len(full) - 1} {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		targetPath := filepath.Join(dir, "target.bin")
		err := Apply(bytes.NewReader(baseline), targetPath, bytes.NewReader(full[:n]))
		if err == nil {
			t.Fatalf("truncated to %d bytes: expected error", n)
		}
		if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
			t.Fatalf("truncated to %d bytes: partial target published", n)
		}
	}
}

func TestBaselineHashMismatch(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	baseline := randomBytes(rng, 64)
	ops, target := randomOps(rng, baseline)
	patch := patchFor(baseline, target, ops)

	other := randomBytes(rng, 64)
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	targetPath, err := applyToFile(t, other, patch, dir)
	if err != ErrBaselineHash {
		t.Fatalf("got %v, want ErrBaselineHash", err)
	}
	if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
		t.Fatal("target published despite baseline hash mismatch")
	}
}

func TestTargetVerificationFailure(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	baseline := randomBytes(rng, 64)
	ops, target := randomOps(rng, baseline)

	badHash := patchFor(baseline, target, ops)
	badHash.TargetHash[0] ^= 0xff

	badLength := patchFor(baseline, target, ops)
	badLength.TargetLength++

	for name, patch := range map[string]*Patch{"hash": badHash, "length": badLength} {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		targetPath, err := applyToFile(t, baseline, patch, dir)
		if err == nil {
			t.Fatalf("%s mismatch: expected error", name)
		}
		if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
			t.Fatalf("%s mismatch: partial target published", name)
		}
	}
}

// TestAtomicPublish verifies that a failed apply leaves a pre-existing target
// untouched and cleans up its temp file, while a successful apply replaces
// the old content.
func TestAtomicPublish(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	baseline := randomBytes(rng, 128)
	ops, target := randomOps(rng, baseline)
	good := patchFor(baseline, target, ops)

	dir := tempDir(t)
	defer os.RemoveAll(dir)
	targetPath := filepath.Join(dir, "target.bin")
	original := []byte("previous release")
	if err := ioutil.WriteFile(targetPath, original, 0644); err != nil {
		t.Fatal(err)
	}

	// Failing apply: original content must survive, temp file must be gone.
	bad := patchFor(baseline, target, ops)
	bad.TargetHash[0] ^= 0xff
	var buf bytes.Buffer
	if err := bad.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if err := Apply(bytes.NewReader(baseline), targetPath, &buf); err == nil {
		t.Fatal("expected apply to fail")
	}
	got, err := ioutil.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("failed apply clobbered existing target: got %q", got)
	}
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".binpatch-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	// Successful apply: old content is replaced atomically.
	buf.Reset()
	if err := good.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if err := Apply(bytes.NewReader(baseline), targetPath, &buf); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err = ioutil.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatal("successful apply did not publish new target")
	}
}

// TestMutatedOps feeds randomly mutated op streams through the applier; it
// must never panic and never publish a target on error.
func TestMutatedOps(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 500; trial++ {
		baseline := randomBytes(rng, rng.Intn(128))
		ops, target := randomOps(rng, baseline)
		patch := patchFor(baseline, target, ops)

		// Mutate: random offset/length on a copy op, or corrupt the header.
		var copyIdx []int
		for i, op := range patch.Ops {
			if op.Kind == OpCopy {
				copyIdx = append(copyIdx, i)
			}
		}
		if len(copyIdx) > 0 && rng.Intn(2) == 0 {
			i := copyIdx[rng.Intn(len(copyIdx))]
			patch.Ops[i].Offset = rng.Uint64()
			patch.Ops[i].Length = rng.Uint64()
		} else {
			switch rng.Intn(3) {
			case 0:
				patch.TargetLength = rng.Uint64()
			case 1:
				patch.TargetHash[rng.Intn(sha256.Size)] ^= byte(1 << uint(rng.Intn(8)))
			case 2:
				patch.BaselineHash[rng.Intn(sha256.Size)] ^= byte(1 << uint(rng.Intn(8)))
			}
		}

		dir := tempDir(t)
		defer os.RemoveAll(dir)
		targetPath, err := applyToFile(t, baseline, patch, dir)
		if err == nil {
			// A mutation that still verifies is fine, but then the file
			// must match the (mutated) declared target exactly.
			got, readErr := ioutil.ReadFile(targetPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if uint64(len(got)) != patch.TargetLength || sha256Of(got) != patch.TargetHash {
				t.Fatalf("trial %d: published target fails its own header", trial)
			}
			continue
		}
		if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
			t.Fatalf("trial %d: partial target published after error %v", trial, err)
		}
	}
}
