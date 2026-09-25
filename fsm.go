// Package segmentfsm implements a crash-safe state machine for the
// build -> validate -> publish -> reclaim lifecycle of log segments.
//
// Layout inside the caller-provided directory:
//
//	manifest.json   current committed manifest (the only source of truth)
//	journal.json    in-flight operation journal; absent means idle
//	seg-NNNNNN.dat  segment files, one per published generation
//	*.tmp           staging files, always safe to delete
//
// Crash recovery: every state transition is journaled (tmp + rename +
// dir fsync) BEFORE the effects of the next step are produced. Open()
// loads the manifest and journal, verifies integrity, and drives any
// interrupted operation to completion. Every step is idempotent: it
// first checks whether its effect is already on disk and skips if so.
//
// Power-loss boundary: a step's effects are durable only after the
// fsync ordering "file content -> rename -> directory" completes.
// A power cut before that leaves either the old or the new state, never
// a mixture; torn tmp files are discarded on recovery. Data is confirmed
// to the caller only when Ingest returns nil, at which point the manifest
// and all segments it references are fully fsynced. If the manifest
// references a segment that is missing or fails its checksum, Open
// reports ErrCorruption and never falls back to an older/empty state,
// because that would silently drop acknowledged data.
package segmentfsm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrCorruption marks unrecoverable on-disk inconsistency: a manifest or
// journal that fails its checksum, or a manifest that references a
// missing/mismatched segment. It is always surfaced, never papered over.
var ErrCorruption = errors.New("segmentfsm: corruption detected")

// State is the journaled "next step to execute" of an in-flight operation.
type State string

const (
	StateBuilding   State = "building"   // next: (re)build the new segment
	StateBuilt      State = "built"      // next: validate the built segment
	StateValidated  State = "validated"  // next: publish the new manifest
	StateReclaiming State = "reclaiming" // next: delete unreferenced old segments
)

// SegmentRef pins a segment file by content hash.
type SegmentRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the committed set of live segments. Anything not listed
// here is garbage and will be reclaimed.
type Manifest struct {
	Generation uint64       `json:"generation"`
	Segments   []SegmentRef `json:"segments"`
}

// journal is the durable record of an in-flight operation. It carries
// everything needed to redo the operation deterministically after a crash.
type journal struct {
	State       State        `json:"state"`
	Generation  uint64       `json:"generation"`
	Data        []string     `json:"data"`
	NewSegment  SegmentRef   `json:"newSegment"`
	OldSegments []SegmentRef `json:"oldSegments"`
}

// Observer receives low-level filesystem events ("write", "sync",
// "rename", "delete", "journal"); tests use it to assert fsync ordering.
type Observer func(op, path string)

// FSM is the segment lifecycle state machine bound to one directory.
type FSM struct {
	dir      string
	injector Injector
	observer Observer
	manifest Manifest
	j        *journal
}

// Option configures an FSM.
type Option func(*FSM)

// WithInjector installs a fault injector (tests / chaos runs).
func WithInjector(inj Injector) Option {
	return func(f *FSM) { f.injector = inj }
}

// WithObserver installs a filesystem event observer.
func WithObserver(ob Observer) Option {
	return func(f *FSM) { f.observer = ob }
}

// Open loads (or initializes) the directory, verifies the manifest against
// the segments on disk, finishes any interrupted operation found in the
// journal, and reclaims orphan files. A manifest that references missing
// or corrupted segments makes Open fail with ErrCorruption.
func Open(dir string, opts ...Option) (*FSM, error) {
	f := &FSM{dir: dir}
	for _, opt := range opts {
		opt(f)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	j, err := f.loadJournal()
	if err != nil {
		return nil, err
	}
	f.j = j
	if err := f.loadManifest(); err != nil {
		return nil, err
	}
	if err := f.verifyManifest(); err != nil {
		return nil, err
	}
	if f.j != nil {
		// Resume the interrupted operation exactly where the journal
		// says we stopped; every remaining step is idempotent.
		if err := f.run(); err != nil {
			return nil, err
		}
	}
	if err := f.gcOrphans(); err != nil {
		return nil, err
	}
	return f, nil
}

// Generation returns the committed manifest generation (0 = empty store).
func (f *FSM) Generation() uint64 { return f.manifest.Generation }

// Segments returns the live segment set of the committed manifest.
func (f *FSM) Segments() []SegmentRef {
	return append([]SegmentRef(nil), f.manifest.Segments...)
}

// Dir returns the directory this FSM manages.
func (f *FSM) Dir() string { return f.dir }

// Ingest runs one full lifecycle: build a new segment containing all
// currently live records plus records, validate it, publish a manifest
// that references only the new segment, then reclaim the old ones.
// It is safe to call again after a crash or a transient error: Open
// completes the interrupted operation first, and a completed operation
// is never duplicated because the journal is removed only at the end.
func (f *FSM) Ingest(records []string) error {
	if f.j != nil {
		return errors.New("segmentfsm: another operation is already in flight")
	}
	for _, r := range records {
		if strings.ContainsAny(r, "\r\n") {
			return fmt.Errorf("segmentfsm: record must not contain newlines: %q", r)
		}
	}
	gen := f.manifest.Generation + 1
	f.j = &journal{
		State:       StateBuilding,
		Generation:  gen,
		Data:        append([]string(nil), records...),
		NewSegment:  SegmentRef{Name: segmentName(gen)},
		OldSegments: append([]SegmentRef(nil), f.manifest.Segments...),
	}
	if err := f.writeJournal(); err != nil {
		return err
	}
	return f.run()
}

// ReadAll returns all records of the committed manifest, verifying each
// segment against its pinned checksum. Missing or corrupted segments
// yield ErrCorruption.
func (f *FSM) ReadAll() ([]string, error) {
	var out []string
	for _, ref := range f.manifest.Segments {
		content, err := f.readSegmentVerified(ref)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
	}
	return out, nil
}

// run drives the journaled operation to completion.
func (f *FSM) run() error {
	for f.j != nil {
		var err error
		switch f.j.State {
		case StateBuilding:
			err = f.stepBuild()
		case StateBuilt:
			err = f.stepValidate()
		case StateValidated:
			err = f.stepPublish()
		case StateReclaiming:
			err = f.stepReclaim()
		default:
			err = fmt.Errorf("%w: unknown journal state %q", ErrCorruption, f.j.State)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// stepBuild writes the new segment (old live records + new records) via
// tmp file + atomic rename. Idempotent: if the final file already exists
// with the journaled checksum, the step is a no-op.
func (f *FSM) stepBuild() error {
	ref := f.j.NewSegment
	if ref.SHA256 != "" {
		if sum, _, err := hashFile(f.segmentPath(ref.Name)); err == nil && sum == ref.SHA256 {
			return f.advance(StateBuilt)
		}
	}
	if err := f.fault(FaultBuildWriteTmp); err != nil {
		return err
	}
	content, err := f.buildContent()
	if err != nil {
		return err
	}
	tmp := f.segmentPath(ref.Name) + tmpSuffix
	if err := f.writeFileSync(tmp, content); err != nil {
		return err
	}
	if err := f.fault(FaultBuildRename); err != nil {
		return err
	}
	if err := f.renameSync(tmp, f.segmentPath(ref.Name)); err != nil {
		return err
	}
	f.j.NewSegment.SHA256 = hashBytes(content)
	f.j.NewSegment.Size = int64(len(content))
	return f.advance(StateBuilt)
}

// stepValidate re-reads the segment from disk and checks it against the
// journaled checksum, catching bit rot between build and publish.
func (f *FSM) stepValidate() error {
	if err := f.fault(FaultValidateRead); err != nil {
		return err
	}
	if _, err := f.readSegmentVerified(f.j.NewSegment); err != nil {
		return err
	}
	return f.advance(StateValidated)
}

// stepPublish commits the new manifest via tmp + rename + dir fsync.
// Idempotent: if the committed manifest is already the target generation,
// the rename is skipped (crash between rename and journal advance).
func (f *FSM) stepPublish() error {
	target := Manifest{
		Generation: f.j.Generation,
		Segments:   []SegmentRef{f.j.NewSegment},
	}
	if manifestsEqual(f.manifest, target) {
		f.manifest = target
		return f.advance(StateReclaiming)
	}
	data, err := marshalEnvelope(target)
	if err != nil {
		return err
	}
	if err := f.fault(FaultPublishWriteTmp); err != nil {
		return err
	}
	tmp := f.manifestPath() + tmpSuffix
	if err := f.writeFileSync(tmp, data); err != nil {
		return err
	}
	if err := f.fault(FaultPublishRename); err != nil {
		return err
	}
	if err := f.renameSync(tmp, f.manifestPath()); err != nil {
		return err
	}
	f.manifest = target
	return f.advance(StateReclaiming)
}

// stepReclaim deletes the superseded segments of the old manifest. After
// publish they are unreferenced, hence safe to remove; before publish the
// NEW segment was the orphan (reclaimed by gcOrphans on rollback paths).
func (f *FSM) stepReclaim() error {
	live := map[string]bool{}
	for _, ref := range f.manifest.Segments {
		live[ref.Name] = true
	}
	for _, old := range f.j.OldSegments {
		if live[old.Name] {
			continue
		}
		if err := f.fault(FaultReclaimDelete); err != nil {
			return err
		}
		if err := f.removeSync(f.segmentPath(old.Name)); err != nil {
			return err
		}
	}
	// Operation complete: remove the journal. After this the directory is
	// back to the idle state (manifest only).
	if err := f.removeSync(f.journalPath()); err != nil {
		return err
	}
	f.j = nil
	return nil
}

// advance persists the next state of the in-flight operation.
func (f *FSM) advance(next State) error {
	f.j.State = next
	return f.writeJournal()
}

func (f *FSM) writeJournal() error {
	if err := f.fault(FaultJournalWrite); err != nil {
		return err
	}
	data, err := marshalEnvelope(f.j)
	if err != nil {
		return err
	}
	tmp := f.journalPath() + tmpSuffix
	if err := f.writeFileSync(tmp, data); err != nil {
		return err
	}
	if err := f.renameSync(tmp, f.journalPath()); err != nil {
		return err
	}
	f.observe("journal", string(f.j.State))
	return nil
}

// buildContent merges the records of the old live segments (verified
// against the old manifest) with the new records.
func (f *FSM) buildContent() ([]byte, error) {
	var b strings.Builder
	for _, ref := range f.j.OldSegments {
		content, err := f.readSegmentVerified(ref)
		if err != nil {
			return nil, err
		}
		b.Write(content)
	}
	for _, r := range f.j.Data {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// readSegmentVerified reads a segment and checks it against its reference.
func (f *FSM) readSegmentVerified(ref SegmentRef) ([]byte, error) {
	data, err := os.ReadFile(f.segmentPath(ref.Name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: segment %q is referenced but missing", ErrCorruption, ref.Name)
	}
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != ref.Size || hashBytes(data) != ref.SHA256 {
		return nil, fmt.Errorf("%w: segment %q fails checksum", ErrCorruption, ref.Name)
	}
	return data, nil
}

// loadManifest reads the committed manifest. A missing manifest is only
// legal in a directory that never completed a publish: a brand-new dir, or
// one whose journal proves the first operation crashed before publishing.
// Anything else (segments or staging files lying around with no journal to
// explain them) means the manifest was lost after acknowledging data, and
// we refuse to silently fall back to an empty state.
func (f *FSM) loadManifest() error {
	data, err := os.ReadFile(f.manifestPath())
	if errors.Is(err, fs.ErrNotExist) {
		entries, _ := os.ReadDir(f.dir)
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, "seg-") && n != manifestFileName+tmpSuffix {
				continue
			}
			// A journal for generation 1 that has not reached the reclaim
			// step explains why no manifest exists yet: the crash happened
			// before the first publish.
			crashedBeforeFirstPublish := f.j != nil && f.j.Generation == 1 && f.j.State != StateReclaiming
			if !crashedBeforeFirstPublish {
				return fmt.Errorf("%w: manifest missing but %q exists; refusing to fall back to empty state", ErrCorruption, n)
			}
		}
		f.manifest = Manifest{}
		return nil
	}
	if err != nil {
		return err
	}
	var m Manifest
	if err := unmarshalEnvelope(data, &m); err != nil {
		return err
	}
	f.manifest = m
	return nil
}

// verifyManifest checks that every referenced segment exists and matches
// its pinned checksum. This is what turns "manifest points at a missing
// segment" into a loud ErrCorruption instead of silent data loss.
func (f *FSM) verifyManifest() error {
	for _, ref := range f.manifest.Segments {
		if _, _, err := hashFile(f.segmentPath(ref.Name)); errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: manifest generation %d references missing segment %q", ErrCorruption, f.manifest.Generation, ref.Name)
		} else if err != nil {
			return err
		}
		if _, err := f.readSegmentVerified(ref); err != nil {
			return err
		}
	}
	return nil
}

func (f *FSM) loadJournal() (*journal, error) {
	data, err := os.ReadFile(f.journalPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j journal
	if err := unmarshalEnvelope(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// gcOrphans removes everything the committed manifest does not reference:
// leftover tmp files from crashed steps and segment files that were never
// published (or were superseded before a crash interrupted reclaim).
func (f *FSM) gcOrphans() error {
	live := map[string]bool{}
	for _, ref := range f.manifest.Segments {
		live[ref.Name] = true
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return err
	}
	removed := false
	for _, e := range entries {
		n := e.Name()
		collectable := strings.HasSuffix(n, tmpSuffix) ||
			(strings.HasPrefix(n, "seg-") && strings.HasSuffix(n, ".dat") && !live[n])
		if !collectable {
			continue
		}
		if err := os.Remove(filepath.Join(f.dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		f.observe("delete", filepath.Join(f.dir, n))
		removed = true
	}
	if removed {
		return f.syncDir()
	}
	return nil
}

func (f *FSM) fault(p FaultPoint) error {
	if f.injector == nil {
		return nil
	}
	return f.injector(p)
}

func (f *FSM) observe(op, path string) {
	if f.observer != nil {
		f.observer(op, path)
	}
}

func segmentName(gen uint64) string {
	return fmt.Sprintf("seg-%06d.dat", gen)
}

func manifestsEqual(a, b Manifest) bool {
	if a.Generation != b.Generation || len(a.Segments) != len(b.Segments) {
		return false
	}
	for i := range a.Segments {
		if a.Segments[i] != b.Segments[i] {
			return false
		}
	}
	return true
}
