package verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mustOpen(t *testing.T, dir string) *Verifier {
	t.Helper()
	v, err := Open(dir, &SliceEmitter{})
	if err != nil {
		t.Fatalf("open verifier: %v", err)
	}
	return v
}

func mustStage(t *testing.T, v *Verifier, chunks []Chunk) {
	t.Helper()
	for _, c := range chunks {
		if err := v.StageChunk(c); err != nil {
			t.Fatalf("stage %s: %v", c.Name, err)
		}
	}
}

func findIssue(rep *Report, c Category) *Issue {
	for i := range rep.Issues {
		if rep.Issues[i].Category == c {
			return &rep.Issues[i]
		}
	}
	return nil
}

func TestCleanBundleSeals(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if rep.Status != "sealed" {
		t.Fatalf("expected sealed, got %s with issues %+v", rep.Status, rep.Issues)
	}
	if len(rep.Files) != 3 {
		t.Fatalf("expected 3 verified files, got %d", len(rep.Files))
	}
}

func TestMissingFileAndAtomicRetry(t *testing.T) {
	chunks, m := Synthesize(TamperMissing)
	dir := t.TempDir()
	v := mustOpen(t, dir)
	mustStage(t, v, chunks)

	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if rep.Status != "rejected" {
		t.Fatalf("expected rejected, got %s", rep.Status)
	}
	iss := findIssue(rep, CatMissingFile)
	if iss == nil || iss.File != "metrics.jsonl" {
		t.Fatalf("expected missing_file for metrics.jsonl, got %+v", rep.Issues)
	}

	// Atomicity: a rejected commit seals nothing and preserves staging.
	staged, sealed := v.Status()
	if len(sealed) != 0 {
		t.Fatalf("nothing may be sealed after a rejected commit, got %v", sealed)
	}
	if len(staged[m.Version]) != 2 {
		t.Fatalf("staging must survive a rejected commit, got %v", staged)
	}

	// The late chunk arrives and the retried commit seals.
	if err := v.StageChunk(Chunk{Version: m.Version, Name: "metrics.jsonl", Data: []byte(point("met-1", 3) + "\n" + point("met-2", 9) + "\n")}); err != nil {
		t.Fatalf("late stage: %v", err)
	}
	rep2, err := v.Commit(m)
	if err != nil {
		t.Fatalf("recommit: %v", err)
	}
	if rep2.Status != "sealed" {
		t.Fatalf("expected sealed after late arrival, got %+v", rep2.Issues)
	}
}

func TestEmptyFileDistinctFromMissing(t *testing.T) {
	chunks, m := Synthesize(TamperEmpty)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	// ghost.log is declared empty but never arrived: missing, not empty.
	iss := findIssue(rep, CatMissingFile)
	if iss == nil || iss.File != "ghost.log" {
		t.Fatalf("expected missing_file for ghost.log, got %+v", rep.Issues)
	}
	// notes.log arrived as a genuine empty file: verified, zero records.
	var notes *FileResult
	for i := range rep.Files {
		if rep.Files[i].Name == "notes.log" {
			notes = &rep.Files[i]
		}
	}
	if notes == nil || !notes.Empty || notes.Records != 0 {
		t.Fatalf("notes.log must verify as an empty file, got %+v", rep.Files)
	}
	if findIssue(rep, CatBadDigest) != nil {
		t.Fatalf("empty file must not raise bad_digest: %+v", rep.Issues)
	}
}

func TestBadDigestBlocksReferences(t *testing.T) {
	chunks, m := Synthesize(TamperDigest)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	iss := findIssue(rep, CatBadDigest)
	if iss == nil || iss.File != "app.log" {
		t.Fatalf("expected bad_digest for app.log, got %+v", rep.Issues)
	}
	// app.log failed verification, so both references into it must be
	// reported as unverified rather than silently checked.
	count := 0
	for _, i := range rep.Issues {
		if i.Category == CatReferenceUnverified && i.TargetFile == "app.log" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 reference_unverified issues, got %+v", rep.Issues)
	}
}

func TestDanglingReferenceLocatesSourceRecord(t *testing.T) {
	chunks, m := Synthesize(TamperReference)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	iss := findIssue(rep, CatDanglingReference)
	if iss == nil {
		t.Fatalf("expected dangling_reference, got %+v", rep.Issues)
	}
	if iss.File != "trace.jsonl" || iss.Record != "span-1" || iss.TargetFile != "app.log" || iss.TargetRecord != "log-999" {
		t.Fatalf("tampered reference must locate the source record, got %+v", *iss)
	}
	// The untouched second reference must still verify cleanly.
	if len(rep.Issues) != 1 {
		t.Fatalf("exactly one reference was tampered, got %+v", rep.Issues)
	}
}

func TestTimeRangeMismatch(t *testing.T) {
	chunks, m := Synthesize(TamperTimeRange)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	iss := findIssue(rep, CatTimeRange)
	if iss == nil || iss.File != "app.log" {
		t.Fatalf("expected time_range_mismatch for app.log, got %+v", rep.Issues)
	}
}

func TestSameNameDifferentDigestRejected(t *testing.T) {
	v := mustOpen(t, t.TempDir())
	c := Chunk{Version: "v1", Name: "app.log", Data: []byte("first")}
	if err := v.StageChunk(c); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	err := v.StageChunk(Chunk{Version: "v1", Name: "app.log", Data: []byte("second")})
	rej, ok := err.(*Rejection)
	if !ok || rej.Issue.Category != CatNameConflict {
		t.Fatalf("expected name_conflict rejection, got %v", err)
	}
	// Identical redelivery is idempotent, not an error.
	if err := v.StageChunk(c); err != nil {
		t.Fatalf("identical redelivery must be idempotent: %v", err)
	}
}

func TestUnknownVersionAndIdempotentCommit(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	rep, err := v.Commit(m)
	if err != nil || rep.Status != "sealed" {
		t.Fatalf("seal: %v %+v", err, rep)
	}

	// A chunk for the sealed version is an unknown-version rejection:
	// old blocks cannot be spliced onto a sealed manifest.
	err = v.StageChunk(Chunk{Version: m.Version, Name: "late.log", Data: []byte("x")})
	if rej, ok := err.(*Rejection); !ok || rej.Issue.Category != CatUnknownVersion {
		t.Fatalf("expected unknown_version for late chunk, got %v", err)
	}

	// A conflicting manifest under the sealed version is rejected.
	conflicting := m
	conflicting.Files = conflicting.Files[:2]
	_, err = v.Commit(conflicting)
	if rej, ok := err.(*Rejection); !ok || rej.Issue.Category != CatUnknownVersion {
		t.Fatalf("expected unknown_version for conflicting manifest, got %v", err)
	}

	// The identical manifest recommits idempotently with the same report.
	rep2, err := v.Commit(m)
	if err != nil {
		t.Fatalf("idempotent recommit: %v", err)
	}
	if !reflect.DeepEqual(rep, rep2) {
		t.Fatalf("idempotent recommit must return the stored report")
	}
}

func TestOldChunksCannotSpliceIntoNewManifest(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	v := mustOpen(t, t.TempDir())
	// All payloads arrive, but tagged with an older version.
	for _, c := range chunks {
		old := c
		old.Version = "v0"
		if err := v.StageChunk(old); err != nil {
			t.Fatalf("stage old: %v", err)
		}
	}
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if rep.Status != "rejected" || !rep.HasCategory(CatMissingFile) {
		t.Fatalf("v0 chunks must not satisfy the v1 manifest, got %+v", rep.Issues)
	}
	missing := 0
	for _, iss := range rep.Issues {
		if iss.Category == CatMissingFile {
			missing++
		}
	}
	if missing != len(m.Files) {
		t.Fatalf("every file must be reported missing, got %+v", rep.Issues)
	}
}

func TestUnexpectedExtraChunkBlocksSeal(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	if err := v.StageChunk(Chunk{Version: m.Version, Name: "stray.bin", Data: []byte("?")}); err != nil {
		t.Fatalf("stage stray: %v", err)
	}
	rep, err := v.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if iss := findIssue(rep, CatUnexpectedFile); iss == nil || iss.File != "stray.bin" {
		t.Fatalf("expected unexpected_file for stray.bin, got %+v", rep.Issues)
	}
}

func permutations(items []string) [][]string {
	if len(items) <= 1 {
		return [][]string{items}
	}
	var out [][]string
	for i, it := range items {
		rest := append([]string{}, items[:i]...)
		rest = append(rest, items[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]string{it}, p...))
		}
	}
	return out
}

func TestArbitraryArrivalOrderEqualsFullVerification(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	byName := map[string]Chunk{}
	var names []string
	for _, c := range chunks {
		byName[c.Name] = c
		names = append(names, c.Name)
	}

	base := mustOpen(t, t.TempDir())
	mustStage(t, base, chunks)
	want, err := base.Commit(m)
	if err != nil {
		t.Fatalf("baseline commit: %v", err)
	}

	for _, order := range permutations(names) {
		v := mustOpen(t, t.TempDir())
		for _, n := range order {
			if err := v.StageChunk(byName[n]); err != nil {
				t.Fatalf("stage %s: %v", n, err)
			}
		}
		got, err := v.Commit(m)
		if err != nil {
			t.Fatalf("commit in order %v: %v", order, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("order %v diverged:\nwant %+v\ngot  %+v", order, want, got)
		}
	}
}

func TestRecoveryEqualsUninterruptedRun(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	actions := Actions(chunks, m)

	uninterrupted, err := Replay(t.TempDir(), actions, 0)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	// Reopen the verifier from disk after every single action.
	recovered, err := Replay(t.TempDir(), actions, 1)
	if err != nil {
		t.Fatalf("replay with recovery: %v", err)
	}
	if !reflect.DeepEqual(uninterrupted.Reports, recovered.Reports) {
		t.Fatalf("recovery diverged:\nwant %+v\ngot  %+v", uninterrupted.Reports, recovered.Reports)
	}
	if recovered.FinalReport().Status != "sealed" {
		t.Fatalf("expected sealed, got %+v", recovered.FinalReport())
	}
}

func TestRecoveryMidStreamKeepsStaging(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	dir := t.TempDir()
	v := mustOpen(t, dir)
	if err := v.StageChunk(chunks[0]); err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Simulate a crash: abandon the instance, recover from disk.
	v2 := mustOpen(t, dir)
	mustStage(t, v2, chunks[1:])
	rep, err := v2.Commit(m)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if rep.Status != "sealed" {
		t.Fatalf("recovered verifier must seal, got %+v", rep.Issues)
	}
}

func TestInputsNeverModified(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	before := map[string]string{}
	for _, c := range chunks {
		sum := sha256.Sum256(c.Data)
		before[c.Name] = hex.EncodeToString(sum[:])
	}
	manifestBefore := m.Digest()

	v := mustOpen(t, t.TempDir())
	mustStage(t, v, chunks)
	if _, err := v.Commit(m); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, c := range chunks {
		sum := sha256.Sum256(c.Data)
		if got := hex.EncodeToString(sum[:]); got != before[c.Name] {
			t.Fatalf("input chunk %s was modified", c.Name)
		}
	}
	if m.Digest() != manifestBefore {
		t.Fatalf("input manifest was modified")
	}
}

func TestTelemetrySynthesizedLocally(t *testing.T) {
	chunks, m := Synthesize(TamperNone)
	em := &SliceEmitter{}
	v, err := Open(t.TempDir(), em)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	mustStage(t, v, chunks)
	if _, err := v.Commit(m); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var types []string
	for _, e := range em.Events() {
		types = append(types, e.Type)
		if e.Seq == 0 {
			t.Fatalf("events must be sequenced: %+v", e)
		}
	}
	want := []string{EventRecovered, EventChunkStaged, EventChunkStaged, EventChunkStaged, EventCommitStarted, EventCommitSealed}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("telemetry sequence mismatch:\nwant %v\ngot  %v", want, types)
	}
}

func TestFixtureReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixture_v1.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	actions, err := UnmarshalFixture(raw)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	res, err := Replay(t.TempDir(), actions, 0)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	rep := res.FinalReport()
	if rep == nil || rep.Status != "sealed" || rep.Version != "v1" {
		t.Fatalf("fixture must replay to a sealed v1 report, got %+v", rep)
	}
	if len(res.Rejections) != 0 {
		t.Fatalf("clean fixture must see no rejections, got %+v", res.Rejections)
	}
	if len(res.Events) == 0 {
		t.Fatalf("replay must synthesize telemetry locally")
	}
}

// TestGenerateFixture regenerates testdata/fixture_v1.json when run with
// GEN_FIXTURE=1. It is skipped in normal test runs.
func TestGenerateFixture(t *testing.T) {
	if os.Getenv("GEN_FIXTURE") == "" {
		t.Skip("set GEN_FIXTURE=1 to regenerate the fixture")
	}
	chunks, m := Synthesize(TamperNone)
	raw, err := MarshalFixture(Actions(chunks, m))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "fixture_v1.json"), raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
