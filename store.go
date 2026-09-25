package segstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store is a single-directory segment store. All state lives in Dir; a
// Store value holds no durable state, so a crash is modelled by discarding
// it and calling Open on the same directory again.
type Store struct {
	dir      string
	fault    FaultFunc
	manifest Manifest
}

// Open creates Dir if needed and rolls forward any interrupted commit.
func Open(dir string, fault FaultFunc) (*Store, error) {
	if fault == nil {
		fault = noFault
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, fault: fault}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir returns the store directory.
func (s *Store) Dir() string { return s.dir }

// Manifest returns the validated current manifest.
func (s *Store) Manifest() Manifest { return s.manifest }

// Commit runs the state machine:
//
//	building -> validating -> publishing -> reclaiming -> idle
//
// Every transition is journaled and every step is idempotent, so Commit is
// safe to call again after any error, including a simulated crash.
func (s *Store) Commit(data []byte) error {
	// Roll forward an interrupted transaction before starting a new one.
	if err := s.recover(); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	txn := s.manifest.Version + 1
	j := &journal{
		State:       StateBuilding,
		Txn:         txn,
		NewSegment:  fmt.Sprintf("seg-%06d.dat", txn),
		Size:        int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		OldSegments: segmentNames(s.manifest),
	}
	if err := s.writeJournal(j); err != nil {
		return err
	}
	return s.run(j, data)
}

// run executes the machine starting from j.State. data may be nil when
// resuming: it is only needed if the segment file is not durable yet.
func (s *Store) run(j *journal, data []byte) error {
	if j.State == StateBuilding {
		if err := s.fault(FaultBeforeBuild); err != nil {
			return err
		}
		if err := s.writeSegment(j, data); err != nil {
			return err
		}
		if err := s.fault(FaultAfterBuild); err != nil {
			return err
		}
		j.State = StateValidating
		if err := s.writeJournal(j); err != nil {
			return err
		}
	}

	if j.State == StateValidating {
		if err := s.fault(FaultBeforeValidate); err != nil {
			return err
		}
		if err := s.validateSegment(j); err != nil {
			return err
		}
		if err := s.fault(FaultAfterValidate); err != nil {
			return err
		}
		j.State = StatePublishing
		if err := s.writeJournal(j); err != nil {
			return err
		}
	}

	if j.State == StatePublishing {
		if err := s.fault(FaultBeforePublish); err != nil {
			return err
		}
		next := Manifest{
			Version:  j.Txn,
			Segments: []SegmentRef{{Name: j.NewSegment, Size: j.Size, SHA256: j.SHA256}},
		}
		// Atomic swap: either the old or the new, self-consistent manifest
		// is visible after a crash.
		if err := s.writeManifest(next); err != nil {
			return err
		}
		if err := s.fault(FaultAfterPublish); err != nil {
			return err
		}
		j.State = StateReclaiming
		if err := s.writeJournal(j); err != nil {
			return err
		}
	}

	if j.State == StateReclaiming {
		if err := s.fault(FaultBeforeReclaim); err != nil {
			return err
		}
		if err := s.reclaim(j); err != nil {
			return err
		}
		if err := s.fault(FaultAfterReclaim); err != nil {
			return err
		}
	}

	if err := s.clearJournal(); err != nil {
		return err
	}
	return s.loadManifest(false)
}

// recover resumes the on-disk transaction, if any.
//
// In the building phase the new segment is not yet acknowledged data:
//   - if a complete, checksum-matching segment is already durable, the
//     machine advances to validating and rolls forward;
//   - otherwise the partial segment is an orphan and the transaction is
//     aborted (journal removed), leaving the previously published manifest
//     untouched. The caller retries Commit.
func (s *Store) recover() error {
	// Read the journal before the manifest: a journaled in-flight commit
	// legitimately has segment files in place before any manifest exists.
	j, err := s.readJournal()
	if err != nil {
		return err
	}
	if err := s.loadManifest(j != nil); err != nil {
		return err
	}
	if j == nil {
		return nil
	}

	if j.State == StateBuilding {
		matches, err := s.segmentMatches(j)
		if err != nil {
			return err
		}
		if !matches {
			if err := os.Remove(filepath.Join(s.dir, j.NewSegment)); err != nil &&
				!errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := s.clearJournal(); err != nil {
				return err
			}
			return s.loadManifest(false)
		}
		j.State = StateValidating
		if err := s.writeJournal(j); err != nil {
			return err
		}
	}
	return s.run(j, nil)
}

// writeSegment is idempotent: an already durable, checksum-matching file is
// left untouched.
func (s *Store) writeSegment(j *journal, data []byte) error {
	matches, err := s.segmentMatches(j)
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	if data == nil {
		return fmt.Errorf("%w: segment %s incomplete and no payload supplied",
			ErrCorrupt, j.NewSegment)
	}
	return writeFileAtomic(s.dir, j.NewSegment, data)
}

func (s *Store) validateSegment(j *journal) error {
	matches, err := s.segmentMatches(j)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("%w: segment %s failed validation", ErrCorrupt, j.NewSegment)
	}
	return nil
}

// segmentMatches reports whether the segment exists with the journaled
// size and checksum.
func (s *Store) segmentMatches(j *journal) (bool, error) {
	path := filepath.Join(s.dir, j.NewSegment)
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if st.IsDir() || st.Size() != j.Size {
		return false, nil
	}
	got, err := hashFile(path)
	if err != nil {
		return false, err
	}
	return got == j.SHA256, nil
}

// reclaim deletes segments superseded by the published manifest. Removal is
// idempotent: a crash here simply repeats the deletes on recovery.
func (s *Store) reclaim(j *journal) error {
	for _, name := range j.OldSegments {
		if name == j.NewSegment {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return syncDir(s.dir)
}

// CollectOrphans deletes every segment file not reachable by reference:
// referenced means it is in the current manifest, or it is the new segment
// of an in-flight journal (before publishing). Leftover *.tmp files are
// collected as well. It returns the removed file names.
func (s *Store) CollectOrphans() ([]string, error) {
	protected := make(map[string]bool)
	for _, ref := range s.manifest.Segments {
		protected[ref.Name] = true
	}
	j, err := s.readJournal()
	if err != nil {
		return nil, err
	}
	// In reclaiming state the new segment is already manifest-referenced.
	if j != nil && j.State != StateReclaiming {
		protected[j.NewSegment] = true
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		isSegment := strings.HasPrefix(name, "seg-") && strings.HasSuffix(name, ".dat")
		isTemp := strings.HasSuffix(name, ".tmp")
		if (isSegment && !protected[name]) || isTemp {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
				return removed, err
			}
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	return removed, syncDir(s.dir)
}

func segmentNames(m Manifest) []string {
	names := make([]string, 0, len(m.Segments))
	for _, ref := range m.Segments {
		names = append(names, ref.Name)
	}
	return names
}

// writeFileAtomic is the durability primitive: write to a temp file, fsync
// the data, atomically rename into place, then fsync the directory so the
// rename survives power loss on POSIX filesystems.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, name+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
