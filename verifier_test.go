package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

var testBase = time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)

func ts(min int) string {
	return testBase.Add(time.Duration(min) * time.Minute).Format(time.RFC3339Nano)
}

func testDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func entry(name string, kind Kind, data []byte) FileEntry {
	return FileEntry{Name: name, Kind: kind, Size: int64(len(data)),
		SHA256: testDigest(data), Start: testBase, End: testBase.Add(10 * time.Minute)}
}

// fixture builds a small but complete evidence package in memory.
type fixture struct {
	manifest Manifest
	files    map[string][]byte
}

func newFixture() *fixture {
	logData := []byte(fmt.Sprintf("L1|%s|boot\nL2|%s|req\n", ts(0), ts(5)))
	traceData := []byte(fmt.Sprintf("T1|%s|span ref=app.log:L1\nT2|%s|span ref=app.log:L2\n", ts(1), ts(4)))
	metricsData := []byte(fmt.Sprintf("M1|%s|cpu=0.5\n", ts(2)))
	f := &fixture{files: map[string][]byte{
		"app.log":      logData,
		"trace.jsonl":  traceData,
		"metrics.prom": metricsData,
	}}
	f.manifest = Manifest{Version: "v1", Files: []FileEntry{
		entry("app.log", KindLog, logData),
		entry("trace.jsonl", KindTrace, traceData),
		entry("metrics.prom", KindMetrics, metricsData),
	}}
	return f
}

// chunk describes one out-of-order delivery unit.
type chunk struct {
	version string
	name    string
	offset  int64
	data    []byte
}

// split cuts data into n roughly equal chunks.
func splitChunks(version, name string, data []byte, n int) []chunk {
	var out []chunk
	step := (len(data) + n - 1) / n
	for off := 0; off < len(data); off += step {
		end := off + step
		if end > len(data) {
			end = len(data)
		}
		out = append(out, chunk{version, name, int64(off), data[off:end]})
	}
	return out
}

func (f *fixture) allChunks(parts int) []chunk {
	var out []chunk
	for name, data := range f.files {
		out = append(out, splitChunks(f.manifest.Version, name, data, parts)...)
	}
	return out
}

func feed(v *Verifier, chunks []chunk) {
	for _, c := range chunks {
		v.AcceptChunk(c.version, c.name, c.offset, c.data)
	}
}

func newTestVerifier(t *testing.T, dir string) *Verifier {
	t.Helper()
	v, err := NewVerifier(dir)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func codes(rep Report) []Code {
	var out []Code
	for _, f := range rep.Findings {
		out = append(out, f.Code)
	}
	return out
}

func hasCode(rep Report, c Code) bool {
	for _, f := range rep.Findings {
		if f.Code == c {
			return true
		}
	}
	return false
}

func TestCleanPackageVerifies(t *testing.T) {
	f := newFixture()
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(3))
	rep := v.Verify("v1")
	if !rep.OK() {
		t.Fatalf("expected OK, got %v", rep.Findings)
	}
}

func TestMissingFileDistinctFromEmptyFile(t *testing.T) {
	f := newFixture()
	f.manifest.Files = append(f.manifest.Files,
		FileEntry{Name: "empty.log", Kind: KindLog, Size: 0, SHA256: testDigest(nil)},
		FileEntry{Name: "gone.log", Kind: KindLog, Size: 5, SHA256: testDigest([]byte("hello"))},
	)
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(1))
	rep := v.Verify("v1")
	if rep.OK() {
		t.Fatal("expected failure due to missing file")
	}
	var empty, missing bool
	for _, fnd := range rep.Findings {
		switch fnd.File {
		case "empty.log":
			empty = fnd.Code == CodeEmptyFile
		case "gone.log":
			missing = fnd.Code == CodeMissingFile
		}
	}
	if !empty || !missing {
		t.Fatalf("empty/missing not distinguished: %v", rep.Findings)
	}
}

func TestBadDigestReportedSeparately(t *testing.T) {
	f := newFixture()
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(f.files["app.log"])
	tampered[0] = 'X' // same length, wrong digest
	feed(v, splitChunks("v1", "trace.jsonl", f.files["trace.jsonl"], 1))
	feed(v, splitChunks("v1", "metrics.prom", f.files["metrics.prom"], 1))
	v.AcceptChunk("v1", "app.log", 0, tampered)
	rep := v.Verify("v1")
	if !hasCode(rep, CodeBadDigest) {
		t.Fatalf("expected BAD_DIGEST, got %v", rep.Findings)
	}
	if hasCode(rep, CodeUnknownVer) {
		t.Fatalf("bad digest must not be conflated with unknown version: %v", rep.Findings)
	}
	// The rejected file is also reported as missing, under its own code.
	var badDigest, missing bool
	for _, fnd := range rep.Findings {
		if fnd.File == "app.log" && fnd.Code == CodeBadDigest {
			badDigest = true
		}
		if fnd.File == "app.log" && fnd.Code == CodeMissingFile {
			missing = true
		}
	}
	if !badDigest || !missing {
		t.Fatalf("expected distinct BAD_DIGEST and MISSING_FILE codes, got %v", rep.Findings)
	}
}

func TestUnknownVersionReportedSeparately(t *testing.T) {
	f := newFixture()
	v := newTestVerifier(t, "")
	v.AcceptChunk("v9", "app.log", 0, f.files["app.log"]) // no manifest for v9
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(1))
	rep := v.Verify("v1")
	if !rep.OK() {
		t.Fatalf("rejected foreign chunks must not pollute v1: %v", rep.Findings)
	}
	rep9 := v.Verify("v9")
	if len(rep9.Findings) == 0 {
		t.Fatalf("expected UNKNOWN_VERSION, got %v", rep9.Findings)
	}
	for _, fnd := range rep9.Findings {
		if fnd.Code != CodeUnknownVer {
			t.Fatalf("expected only UNKNOWN_VERSION findings, got %v", rep9.Findings)
		}
	}
}

func TestArrivalOrderEquivalence(t *testing.T) {
	f := newFixture()
	want := func() Report {
		v := newTestVerifier(t, "")
		if err := v.AcceptManifest(f.manifest); err != nil {
			t.Fatal(err)
		}
		feed(v, f.allChunks(4))
		return v.Verify("v1")
	}()
	for seed := int64(0); seed < 20; seed++ {
		v := newTestVerifier(t, "")
		if err := v.AcceptManifest(f.manifest); err != nil {
			t.Fatal(err)
		}
		chunks := f.allChunks(4)
		rand.New(rand.NewSource(seed)).Shuffle(len(chunks), func(i, j int) {
			chunks[i], chunks[j] = chunks[j], chunks[i]
		})
		feed(v, chunks)
		got := v.Verify("v1")
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("seed %d: arrival order changed report\nwant %v\ngot  %v", seed, want.Findings, got.Findings)
		}
		if !got.OK() {
			t.Fatalf("seed %d: expected OK", seed)
		}
	}
}

func TestRecoveryEquivalence(t *testing.T) {
	f := newFixture()
	dir := t.TempDir()

	// First process: seal half the package, then "crash".
	v1 := newTestVerifier(t, dir)
	if err := v1.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v1, splitChunks("v1", "app.log", f.files["app.log"], 2))
	feed(v1, splitChunks("v1", "trace.jsonl", f.files["trace.jsonl"], 2))

	// Second process on the same store dir: recover, deliver the rest.
	v2 := newTestVerifier(t, dir)
	if err := v2.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v2, splitChunks("v1", "metrics.prom", f.files["metrics.prom"], 2))
	recovered := v2.Verify("v1")

	// Reference: one-shot full verification.
	v3 := newTestVerifier(t, "")
	if err := v3.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v3, f.allChunks(2))
	oneshot := v3.Verify("v1")

	if !reflect.DeepEqual(recovered, oneshot) {
		t.Fatalf("recovery differs from one-shot\nrecovered %v\noneshot   %v", recovered.Findings, oneshot.Findings)
	}
	if !recovered.OK() {
		t.Fatalf("expected OK after recovery, got %v", recovered.Findings)
	}
}

func TestOldChunksCannotSpliceIntoNewManifest(t *testing.T) {
	f := newFixture()
	dir := t.TempDir()
	v := newTestVerifier(t, dir)
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(1)) // v1 fully sealed

	// v2 pins the same file names to new content (new digests).
	newLog := []byte(fmt.Sprintf("L1|%s|rotated\nL2|%s|rotated\nL9|%s|new\n", ts(0), ts(1), ts(2)))
	m2 := Manifest{Version: "v2", Files: []FileEntry{
		entry("app.log", KindLog, newLog),
		entry("trace.jsonl", KindTrace, f.files["trace.jsonl"]),
		entry("metrics.prom", KindMetrics, f.files["metrics.prom"]),
	}}
	if err := v.AcceptManifest(m2); err != nil {
		t.Fatal(err)
	}
	// Deliver only the files that are unchanged; app.log's old v1 chunks
	// must not satisfy the v2 pin.
	feed(v, splitChunks("v2", "trace.jsonl", f.files["trace.jsonl"], 1))
	feed(v, splitChunks("v2", "metrics.prom", f.files["metrics.prom"], 1))
	rep := v.Verify("v2")
	if !hasCode(rep, CodeMissingFile) {
		t.Fatalf("v2 app.log must be missing until its own chunks arrive: %v", rep.Findings)
	}
	// Now deliver v2's own app.log; the package becomes complete.
	feed(v, splitChunks("v2", "app.log", newLog, 2))
	rep = v.Verify("v2")
	if !rep.OK() {
		t.Fatalf("expected OK after v2 chunks arrived, got %v", rep.Findings)
	}
	// v1 remains intact and verifiable.
	if rep1 := v.Verify("v1"); !rep1.OK() {
		t.Fatalf("v1 must stay valid, got %v", rep1.Findings)
	}
}

func TestSameNameDifferentDigestRejected(t *testing.T) {
	m := Manifest{Version: "v1", Files: []FileEntry{
		{Name: "a.log", Kind: KindLog, Size: 1, SHA256: testDigest([]byte("x"))},
		{Name: "a.log", Kind: KindLog, Size: 1, SHA256: testDigest([]byte("y"))},
	}}
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(m); err == nil {
		t.Fatal("conflicting digests for one name must be rejected")
	}
	rep := v.Verify("v1")
	if !hasCode(rep, CodeNameConflict) {
		t.Fatalf("expected NAME_DIGEST_CONFLICT, got %v", rep.Findings)
	}
}

func TestDanglingReferenceLocatesSourceRecord(t *testing.T) {
	f := newFixture()
	// Trace T2 now references a log record that does not exist.
	f.files["trace.jsonl"] = []byte(fmt.Sprintf(
		"T1|%s|span ref=app.log:L1\nT2|%s|span ref=app.log:GHOST\n", ts(1), ts(4)))
	f.manifest.Files[1] = entry("trace.jsonl", KindTrace, f.files["trace.jsonl"])

	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(1))
	rep := v.Verify("v1")
	var found *Finding
	for i := range rep.Findings {
		if rep.Findings[i].Code == CodeDanglingRef {
			found = &rep.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("expected DANGLING_REFERENCE, got %v", rep.Findings)
	}
	if found.File != "trace.jsonl" || found.Record != "T2" {
		t.Fatalf("tampered reference must locate the source record, got %+v", *found)
	}
}

func TestReferencesOnlyCheckedAfterTargetVerified(t *testing.T) {
	f := newFixture()
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	// Deliver trace but not the log it references.
	feed(v, splitChunks("v1", "trace.jsonl", f.files["trace.jsonl"], 1))
	feed(v, splitChunks("v1", "metrics.prom", f.files["metrics.prom"], 1))
	rep := v.Verify("v1")
	if hasCode(rep, CodeDanglingRef) {
		t.Fatalf("refs must not be evaluated before the target file verifies: %v", rep.Findings)
	}
	if !hasCode(rep, CodeMissingFile) {
		t.Fatalf("expected MISSING_FILE for app.log, got %v", rep.Findings)
	}
}

func TestTimeRangeMismatch(t *testing.T) {
	f := newFixture()
	// Declare a range that excludes the last log record.
	for i := range f.manifest.Files {
		if f.manifest.Files[i].Name == "app.log" {
			f.manifest.Files[i].End = testBase.Add(2 * time.Minute)
		}
	}
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(1))
	rep := v.Verify("v1")
	if !hasCode(rep, CodeTimeRange) {
		t.Fatalf("expected TIME_RANGE_MISMATCH, got %v", rep.Findings)
	}
	for _, fnd := range rep.Findings {
		if fnd.Code == CodeTimeRange && (fnd.File != "app.log" || fnd.Record != "L2") {
			t.Fatalf("time range finding must locate the offending record, got %+v", fnd)
		}
	}
}

func TestTelemetryFixtureReplay(t *testing.T) {
	f := newFixture()
	v := newTestVerifier(t, "")
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	feed(v, f.allChunks(2))
	v.Verify("v1")

	var buf bytes.Buffer
	if err := v.Telemetry().SaveFixture(&buf); err != nil {
		t.Fatal(err)
	}
	replayed, err := ReplayFixture(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Telemetry().Events(), replayed) {
		t.Fatal("fixture replay must reproduce the live event sequence")
	}
	if len(replayed) == 0 {
		t.Fatal("expected telemetry events")
	}
	var types []string
	for _, ev := range replayed {
		types = append(types, ev.Type)
	}
	sort.Strings(types)
	if types[len(types)-1] != "manifest_accepted" && !contains(types, "file_sealed") {
		t.Fatalf("unexpected event types %v", types)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestInputsNeverModified(t *testing.T) {
	f := newFixture()
	// Write the evidence package to a "source" dir the verifier only reads.
	src := t.TempDir()
	before := map[string]string{}
	for name, data := range f.files {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
		before[name] = testDigest(data)
	}
	v := newTestVerifier(t, t.TempDir())
	if err := v.AcceptManifest(f.manifest); err != nil {
		t.Fatal(err)
	}
	for name := range f.files {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		feed(v, splitChunks("v1", name, data, 3))
	}
	if rep := v.Verify("v1"); !rep.OK() {
		t.Fatalf("expected OK, got %v", rep.Findings)
	}
	for name, want := range before {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := testDigest(data); got != want {
			t.Fatalf("input %s was modified", name)
		}
	}
	// The verifier's store must be a separate directory.
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(f.files) {
		t.Fatalf("verifier wrote into the source dir: %v", entries)
	}
}
