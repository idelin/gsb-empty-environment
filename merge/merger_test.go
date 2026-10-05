package merge

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func testOpts(inputs []string, dir string) Options {
	return Options{
		Inputs:          inputs,
		OutputPath:      filepath.Join(dir, "out.run"),
		CheckpointPath:  filepath.Join(dir, "out.ckpt"),
		MemLimit:        32 << 10,
		BlockSize:       2 << 10,
		CheckpointEvery: 100,
		Resume:          true,
	}
}

func runOnce(t *testing.T, opts Options) (*Merger, error) {
	t.Helper()
	m, err := NewMerger(opts)
	if err != nil {
		t.Fatalf("NewMerger: %v", err)
	}
	runErr := m.Run()
	peak := m.PeakMem
	m.Close()
	m.PeakMem = peak
	return m, runErr
}

func mustVerify(t *testing.T, opts Options) {
	t.Helper()
	ck, err := LoadCheckpoint(opts.CheckpointPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if ck == nil {
		t.Fatalf("no checkpoint written")
	}
	if err := Verify(opts.OutputPath, opts.Inputs, ck); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	recs := []Record{
		{Key: "a", Source: 0, Seq: 0},
		{Key: "hello", Source: 300, Seq: 1 << 40, Payload: []byte{1, 2, 3}},
		{Key: "", Source: 1<<32 - 1, Seq: 1<<64 - 1, Payload: make([]byte, 1000)},
	}
	var buf []byte
	for _, r := range recs {
		buf = AppendRecord(buf, r)
	}
	off := 0
	for i, want := range recs {
		got, n, err := DecodeRecord(buf[off:])
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if !equalRecord(got, want) {
			t.Fatalf("record %d: got %+v want %+v", i, got, want)
		}
		if n != want.framedSize() {
			t.Fatalf("record %d: consumed %d, framedSize %d", i, n, want.framedSize())
		}
		off += n
	}
	if off != len(buf) {
		t.Fatalf("consumed %d of %d bytes", off, len(buf))
	}
	// A truncated record must report ErrIncomplete, not corruption.
	one := AppendRecord(nil, recs[2])
	if _, _, err := DecodeRecord(one[:len(one)-1]); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("truncated decode: got %v, want ErrIncomplete", err)
	}
}

func TestBasicMerge(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(1))
	inputs, err := GenerateInputs(dir, 6, 500, 1000, 64, rng)
	if err != nil {
		t.Fatal(err)
	}
	opts := testOpts(inputs, dir)
	m, err := runOnce(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The limit bounds read-ahead buffering; on top of it the merge may
	// hold one in-flight record per source, which is inherent to k-way
	// merging.
	var maxRec int64
	for _, in := range inputs {
		recs, _, _, err := ScanFile(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			if s := int64(r.framedSize()); s > maxRec {
				maxRec = s
			}
		}
	}
	if limit := opts.MemLimit + int64(len(inputs))*maxRec; m.PeakMem > limit {
		t.Fatalf("peak memory %d exceeds bound %d", m.PeakMem, limit)
	}
	mustVerify(t, opts)
}

func TestOversizeRecord(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(2))
	inputs, err := GenerateInputs(dir, 3, 100, 500, 32, rng)
	if err != nil {
		t.Fatal(err)
	}
	// Append one record far larger than a block (and than the memory limit)
	// to source 0, inserted so the run stays sorted.
	big := Record{Key: "k0250", Source: 0, Seq: 1 << 20, Payload: make([]byte, 200<<10)}
	rng.Read(big.Payload)
	recs, _, _, err := ScanFile(inputs[0])
	if err != nil {
		t.Fatal(err)
	}
	recs = append(recs, big)
	sort.SliceStable(recs, func(a, b int) bool { return Less(recs[a], recs[b]) })
	var buf []byte
	for _, r := range recs {
		buf = AppendRecord(buf, r)
	}
	if err := os.WriteFile(inputs[0], buf, 0o644); err != nil {
		t.Fatal(err)
	}

	opts := testOpts(inputs, dir)
	opts.MemLimit = 16 << 10
	opts.BlockSize = 1 << 10
	m, err := runOnce(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m.PeakMem <= opts.MemLimit {
		t.Fatalf("expected oversize record to push peak past limit, peak=%d limit=%d", m.PeakMem, opts.MemLimit)
	}
	if m.PeakMem > int64(big.framedSize())+opts.MemLimit+int64(opts.BlockSize) {
		t.Fatalf("peak %d unreasonably high", m.PeakMem)
	}
	mustVerify(t, opts)
}

func TestCrashAckLoss(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(3))
	inputs, err := GenerateInputs(dir, 5, 400, 800, 48, rng)
	if err != nil {
		t.Fatal(err)
	}
	opts := testOpts(inputs, dir)

	// Crash at the second checkpoint, after output is fsynced but before the
	// checkpoint ack is written: the output file will hold unacked bytes.
	phases := 0
	opts.Hook = func(p Phase) error {
		phases++
		if phases == 3 && p == PhaseAfterOutputSync {
			return ErrCrashed
		}
		return nil
	}
	if _, err := runOnce(t, opts); !errors.Is(err, ErrCrashed) {
		t.Fatalf("expected crash, got %v", err)
	}

	// Confirm ack loss really happened: output is longer than the ack.
	ck, err := LoadCheckpoint(opts.CheckpointPath)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(opts.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= ck.OutputOffset {
		t.Fatalf("fault not injected: output size %d, acked offset %d", fi.Size(), ck.OutputOffset)
	}

	// Resume: unacked output must be truncated away, not duplicated.
	opts.Hook = nil
	if _, err := runOnce(t, opts); err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	mustVerify(t, opts)
}

func TestInputTruncation(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(4))
	inputs, err := GenerateInputs(dir, 4, 300, 600, 40, rng)
	if err != nil {
		t.Fatal(err)
	}
	// Chop the last byte off input 2: its final record becomes a partial
	// trailing record and must be discarded, not error the merge.
	fi, err := os.Stat(inputs[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(inputs[2], fi.Size()-1); err != nil {
		t.Fatal(err)
	}
	opts := testOpts(inputs, dir)
	m, err := runOnce(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !m.Truncated(2) {
		t.Fatalf("source 2 truncation not detected")
	}
	mustVerify(t, opts)
}

func TestCrashLoopWithTruncation(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(5))
	inputs, err := GenerateInputs(dir, 6, 500, 1000, 64, rng)
	if err != nil {
		t.Fatal(err)
	}
	opts := testOpts(inputs, dir)

	crashes := 0
	truncated := false
	for attempt := 0; attempt < 100; attempt++ {
		crashAt := 1 + rng.Intn(4) // crash after 1-4 fault-injection phases
		opts.Hook = func(p Phase) error {
			crashAt--
			if crashAt == 0 {
				crashes++
				return ErrCrashed
			}
			return nil
		}
		_, err := runOnce(t, opts)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrCrashed) {
			t.Fatalf("Run: %v", err)
		}
		// Once, between crash and resume, truncate an input tail that no
		// checkpoint has consumed yet.
		if !truncated {
			ck, ckErr := LoadCheckpoint(opts.CheckpointPath)
			if ckErr != nil {
				t.Fatal(ckErr)
			}
			for i, in := range inputs {
				fi, statErr := os.Stat(in)
				if statErr != nil {
					t.Fatal(statErr)
				}
				var consumed int64
				if ck != nil {
					consumed = ck.InputOffsets[i]
				}
				if consumed < fi.Size()-1 {
					if err := os.Truncate(in, fi.Size()-1); err != nil {
						t.Fatal(err)
					}
					truncated = true
					break
				}
			}
		}
	}
	if crashes == 0 {
		t.Fatalf("no crash was injected")
	}
	if !truncated {
		t.Fatalf("no truncation was injected")
	}
	mustVerify(t, opts)
}

func TestResumeAfterDone(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(6))
	inputs, err := GenerateInputs(dir, 3, 200, 500, 32, rng)
	if err != nil {
		t.Fatal(err)
	}
	opts := testOpts(inputs, dir)
	if _, err := runOnce(t, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	// Re-running a completed merge must be a no-op.
	if _, err := runOnce(t, opts); !errors.Is(err, ErrDone) {
		t.Fatalf("expected ErrDone, got %v", err)
	}
	after, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("output changed after done: %d -> %d bytes", len(before), len(after))
	}
	mustVerify(t, opts)
}

func TestEmptyAndSingleRecord(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.run")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(dir, "one.run")
	if err := os.WriteFile(one, AppendRecord(nil, Record{Key: "x", Source: 1, Seq: 0, Payload: []byte("p")}), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := testOpts([]string{empty, one}, dir)
	if _, err := runOnce(t, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustVerify(t, opts)
}
