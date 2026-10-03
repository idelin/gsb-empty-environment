package versionset

import (
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	currentFileName = "CURRENT"
	manifestPrefix  = "MANIFEST-"
	sstSuffix       = ".sst"
)

// Version is an immutable snapshot of the SSTable layout per level.
type Version struct {
	Files [NumLevels][]FileMetadata
}

func newVersion() *Version { return &Version{} }

func (v *Version) clone() *Version {
	nv := newVersion()
	for l := 0; l < NumLevels; l++ {
		if len(v.Files[l]) > 0 {
			nv.Files[l] = make([]FileMetadata, len(v.Files[l]))
			copy(nv.Files[l], v.Files[l])
		}
	}
	return nv
}

// apply mutates the version according to one edit.
func (v *Version) apply(edit *VersionEdit) error {
	for _, del := range edit.DeletedFiles {
		if del.Level < 0 || del.Level >= NumLevels {
			return fmt.Errorf("versionset: bad level %d", del.Level)
		}
		files := v.Files[del.Level]
		idx := -1
		for i := range files {
			if files[i].Number == del.Number {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("versionset: delete of unknown file %d at L%d", del.Number, del.Level)
		}
		v.Files[del.Level] = append(files[:idx], files[idx+1:]...)
	}
	for _, add := range edit.NewFiles {
		if add.Level < 0 || add.Level >= NumLevels {
			return fmt.Errorf("versionset: bad level %d", add.Level)
		}
		for _, f := range v.Files[add.Level] {
			if f.Number == add.Meta.Number {
				return fmt.Errorf("versionset: duplicate file %d at L%d", add.Meta.Number, add.Level)
			}
		}
		v.Files[add.Level] = append(v.Files[add.Level], add.Meta)
		sort.Slice(v.Files[add.Level], func(i, j int) bool {
			return v.Files[add.Level][i].Number < v.Files[add.Level][j].Number
		})
	}
	return nil
}

// liveSet returns the set of file numbers referenced by the version.
func (v *Version) liveSet() map[uint64]bool {
	set := make(map[uint64]bool)
	for l := 0; l < NumLevels; l++ {
		for _, f := range v.Files[l] {
			set[f.Number] = true
		}
	}
	return set
}

// VersionSet manages the current Version and the MANIFEST log.
type VersionSet struct {
	dir string

	mu             sync.Mutex
	current        *Version
	manifest       *os.File
	manifestName   string
	nextFileNumber uint64
	lastSequence   uint64
	logNumber      uint64
}

// Open creates or recovers a VersionSet rooted at dir.
//
// Recovery procedure: read CURRENT to find the live MANIFEST, replay all
// valid records (a torn tail is ignored), rebuild the latest Version, then
// delete any *.sst file on disk that the recovered version does not
// reference (orphans left by a compaction that crashed before commit).
func Open(dir string) (*VersionSet, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	vs := &VersionSet{
		dir:            dir,
		current:        newVersion(),
		nextFileNumber: 2, // 1 is reserved for the first MANIFEST
	}
	data, err := ioutil.ReadFile(filepath.Join(dir, currentFileName))
	if os.IsNotExist(err) {
		if err := vs.createManifest(); err != nil {
			return nil, err
		}
		return vs, nil
	}
	if err != nil {
		return nil, err
	}
	vs.manifestName = strings.TrimSpace(string(data))
	if err := vs.replay(filepath.Join(dir, vs.manifestName)); err != nil {
		return nil, err
	}
	// Never reuse the number of any file that exists on disk (e.g. an
	// orphan from a crashed compaction), even though it is about to be
	// deleted: this keeps file numbers monotonic across restarts.
	if err := vs.markExistingFileNumbers(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, vs.manifestName), os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	vs.manifest = f
	if err := vs.removeObsoleteFiles(); err != nil {
		return nil, err
	}
	return vs, nil
}

// createManifest initializes a fresh database directory.
func (vs *VersionSet) createManifest() error {
	vs.manifestName = manifestPrefix + fmt.Sprintf("%06d", 1)
	f, err := os.OpenFile(filepath.Join(vs.dir, vs.manifestName), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	vs.manifest = f
	// Persist a snapshot edit so replay learns the initial counters.
	if err := vs.logAndApplyLocked(&VersionEdit{}); err != nil {
		f.Close()
		return err
	}
	return writeFileAtomic(vs.dir, currentFileName, []byte(vs.manifestName+"\n"))
}

// replay rebuilds state by applying every valid record in the manifest.
func (vs *VersionSet) replay(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	v := newVersion()
	for {
		edit, err := decodeRecord(f)
		if err == io.EOF {
			break // clean end of log
		}
		if err == io.ErrUnexpectedEOF || err == ErrCorruptRecord {
			break // torn tail from a crash: ignore the partial record
		}
		if err != nil {
			return err
		}
		if err := v.apply(edit); err != nil {
			return err
		}
		if edit.NextFileNumber != 0 {
			vs.nextFileNumber = edit.NextFileNumber
		}
		if edit.LastSequence != 0 {
			vs.lastSequence = edit.LastSequence
		}
		if edit.LogNumber != 0 {
			vs.logNumber = edit.LogNumber
		}
	}
	vs.current = v
	return nil
}

// markExistingFileNumbers bumps nextFileNumber past every *.sst file
// currently present in the directory.
func (vs *VersionSet) markExistingFileNumbers() error {
	entries, err := ioutil.ReadDir(vs.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, sstSuffix) {
			continue
		}
		num, err := strconv.ParseUint(strings.TrimSuffix(name, sstSuffix), 10, 64)
		if err != nil {
			continue
		}
		if num >= vs.nextFileNumber {
			vs.nextFileNumber = num + 1
		}
	}
	return nil
}

// removeObsoleteFiles deletes SSTables not referenced by the live version.
func (vs *VersionSet) removeObsoleteFiles() error {
	live := vs.current.liveSet()
	entries, err := ioutil.ReadDir(vs.dir)
	if err != nil {
		return err
	}
	removed := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, sstSuffix) {
			continue
		}
		num, err := strconv.ParseUint(strings.TrimSuffix(name, sstSuffix), 10, 64)
		if err != nil {
			continue
		}
		if !live[num] {
			if err := os.Remove(filepath.Join(vs.dir, name)); err != nil {
				return err
			}
			removed = true
		}
	}
	if removed {
		return syncDir(vs.dir)
	}
	return nil
}

// LogAndApply atomically commits one VersionEdit: the record is appended to
// the MANIFEST and fsynced before the in-memory version is swapped. If the
// process crashes before the fsync completes, recovery simply never sees the
// edit and the previous version remains in effect.
func (vs *VersionSet) LogAndApply(edit *VersionEdit) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.logAndApplyLocked(edit)
}

func (vs *VersionSet) logAndApplyLocked(edit *VersionEdit) error {
	if edit.NextFileNumber == 0 {
		edit.NextFileNumber = vs.nextFileNumber
	}
	if edit.LastSequence == 0 {
		edit.LastSequence = vs.lastSequence
	}
	if edit.LogNumber == 0 {
		edit.LogNumber = vs.logNumber
	}
	next := vs.current.clone()
	if err := next.apply(edit); err != nil {
		return err
	}
	if err := encodeRecord(vs.manifest, edit); err != nil {
		return err
	}
	if err := vs.manifest.Sync(); err != nil {
		return err
	}
	vs.current = next
	vs.nextFileNumber = edit.NextFileNumber
	vs.lastSequence = edit.LastSequence
	vs.logNumber = edit.LogNumber
	return nil
}

// Current returns a copy of the live version.
func (vs *VersionSet) Current() *Version {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.current.clone()
}

// AllocateFileNumber reserves a fresh file number.
func (vs *VersionSet) AllocateFileNumber() uint64 {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	n := vs.nextFileNumber
	vs.nextFileNumber++
	return n
}

// LastSequence returns the last committed sequence number.
func (vs *VersionSet) LastSequence() uint64 {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.lastSequence
}

// SSTPath returns the on-disk path of an SSTable file number.
func (vs *VersionSet) SSTPath(number uint64) string {
	return filepath.Join(vs.dir, fmt.Sprintf("%06d%s", number, sstSuffix))
}

// WriteSST writes data as a new SSTable with a reserved file number,
// fsyncing the file and the directory. The file is not yet referenced by
// any version; it becomes live only when a VersionEdit adding it is
// committed via LogAndApply. If the process crashes in between, recovery
// removes it as an orphan.
func (vs *VersionSet) WriteSST(smallest, largest string, data []byte) (FileMetadata, error) {
	num := vs.AllocateFileNumber()
	path := vs.SSTPath(num)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return FileMetadata{}, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return FileMetadata{}, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return FileMetadata{}, err
	}
	if err := f.Close(); err != nil {
		return FileMetadata{}, err
	}
	if err := syncDir(vs.dir); err != nil {
		return FileMetadata{}, err
	}
	return FileMetadata{
		Number:   num,
		Size:     uint64(len(data)),
		Smallest: smallest,
		Largest:  largest,
		Checksum: crc32.ChecksumIEEE(data),
	}, nil
}

// VerifyChecksums recomputes the CRC32 of every live SSTable and compares
// it against the checksum recorded in the manifest.
func (vs *VersionSet) VerifyChecksums() error {
	v := vs.Current()
	for l := 0; l < NumLevels; l++ {
		for _, meta := range v.Files[l] {
			data, err := ioutil.ReadFile(vs.SSTPath(meta.Number))
			if err != nil {
				return fmt.Errorf("versionset: read %06d.sst: %v", meta.Number, err)
			}
			if crc32.ChecksumIEEE(data) != meta.Checksum {
				return fmt.Errorf("versionset: checksum mismatch on %06d.sst", meta.Number)
			}
		}
	}
	return nil
}

// Close flushes and closes the manifest.
func (vs *VersionSet) Close() error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.manifest == nil {
		return nil
	}
	err := vs.manifest.Close()
	vs.manifest = nil
	return err
}

// writeFileAtomic writes name under dir via a temp file + rename + dir sync.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, name+".tmp")
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
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
