package kvlog

const (
	logFileName     = "data.log"
	dirFileName     = "data.dir"
	dirTmpFileName  = "data.dir.tmp"
	compactFileName = "data.log.new"
)

// Store is a key/value store whose data log is the single source of truth.
// The key directory (in memory and as an on-disk snapshot) is only a hint:
// appends and directory updates may be interrupted independently, so every
// directory hit is validated against the record it points at, and any
// doubt is resolved by scanning the log.
type Store struct {
	fs       FS
	log      File
	gen      uint64 // generation of the current log file
	tail     int64  // end of valid log data
	dir      map[string]dirEntry
	versions map[string]uint64 // latest version per key, from the log
}

// Open opens (or creates) the store. It always rebuilds truth from the
// data log: an incomplete tail record is ignored and truncated away, then
// a directory snapshot is loaded only if it survives validation.
func Open(fs FS) (*Store, error) {
	// Clean up orphans from an interrupted compaction or directory write.
	fs.Remove(compactFileName)
	fs.Remove(dirTmpFileName)
	log, err := fs.OpenFile(logFileName)
	if err != nil {
		return nil, err
	}
	s := &Store{
		fs:       fs,
		log:      log,
		dir:      map[string]dirEntry{},
		versions: map[string]uint64{},
	}
	if err := s.rebuild(); err != nil {
		return nil, err
	}
	s.loadDirSnapshot()
	return s, nil
}

func (s *Store) Close() error { return s.log.Close() }

// rebuild scans the log from offset zero, establishing the valid tail, the
// file generation, and the latest version of every key. The first record
// that is incomplete, torn, or corrupt ends the log: everything past it is
// ignored and truncated away.
func (s *Store) rebuild() error {
	off := int64(0)
	gen := uint64(0)
	for {
		rec, n, err := readRecordAt(s.log, off)
		if err != nil {
			break
		}
		if rec.version > s.versions[rec.key] {
			s.versions[rec.key] = rec.version
		}
		if rec.gen > gen {
			gen = rec.gen
		}
		off += int64(n)
	}
	s.gen = gen
	s.tail = off
	return s.log.Truncate(off)
}

// loadDirSnapshot adopts directory entries from the on-disk snapshot, but
// only those that agree with the log: the snapshot generation must match
// the current log generation, and each entry must name the latest version
// of its key. Anything else is dropped and later resolved by log scan.
func (s *Store) loadDirSnapshot() {
	f, err := s.fs.OpenFile(dirFileName)
	if err != nil {
		return
	}
	defer f.Close()
	entries, gen, err := decodeDirFile(f)
	if err != nil || gen != s.gen {
		return
	}
	for k, e := range entries {
		if s.versions[k] == e.version {
			s.dir[k] = e
		}
	}
}

// Append writes the record to the log and syncs it BEFORE touching the
// directory. If a crash hits before the next PersistDir, the directory is
// simply stale and the next Open rebuilds from the log.
func (s *Store) Append(key string, value []byte) error {
	ver := s.versions[key] + 1
	rec := record{gen: s.gen, version: ver, key: key, value: value}
	buf := encodeRecord(rec)
	if _, err := s.log.WriteAt(buf, s.tail); err != nil {
		return err
	}
	if err := s.log.Sync(); err != nil {
		return err
	}
	s.versions[key] = ver
	s.dir[key] = dirEntry{offset: s.tail, version: ver}
	s.tail += int64(len(buf))
	return nil
}

// Get resolves a key. A directory hit is trusted only after the record at
// the referenced offset is validated: same generation, same key, same
// version, and that version must still be the latest. Any mismatch falls
// back to an authoritative log scan, so a stale offset can never misread.
func (s *Store) Get(key string) ([]byte, bool, error) {
	if e, ok := s.dir[key]; ok && e.version == s.versions[key] {
		rec, _, err := readRecordAt(s.log, e.offset)
		if err == nil && rec.gen == s.gen && rec.key == key && rec.version == e.version {
			return rec.value, true, nil
		}
	}
	return s.scanLog(key)
}

// scanLog is the authoritative path: latest version of key in the log.
func (s *Store) scanLog(key string) ([]byte, bool, error) {
	off := int64(0)
	var val []byte
	var ver uint64
	found := false
	for off < s.tail {
		rec, n, err := readRecordAt(s.log, off)
		if err != nil {
			break
		}
		if rec.key == key && rec.version >= ver {
			val, ver, found = rec.value, rec.version, true
		}
		off += int64(n)
	}
	return val, found, nil
}

// PersistDir writes the directory snapshot via temp file + atomic rename.
// It is allowed to be lost or torn: the log remains the source of truth.
func (s *Store) PersistDir() error {
	buf := encodeDirFile(s.gen, s.dir)
	tmp, err := s.fs.OpenFile(dirTmpFileName)
	if err != nil {
		return err
	}
	if err := tmp.Truncate(0); err != nil {
		return err
	}
	if _, err := tmp.WriteAt(buf, 0); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	tmp.Close()
	return s.fs.Rename(dirTmpFileName, dirFileName)
}

// Compact rewrites all live records into a new log generation and
// atomically switches to it. Records keep their versions but get the new
// generation, so any offset cached against the old file fails validation
// after the switch instead of misreading.
func (s *Store) Compact() error {
	newGen, newDir, newTail, err := s.writeCompacted()
	if err != nil {
		return err
	}
	if err := s.fs.Rename(compactFileName, logFileName); err != nil {
		return err
	}
	log, err := s.fs.OpenFile(logFileName)
	if err != nil {
		return err
	}
	s.log.Close()
	s.log = log
	s.gen = newGen
	s.dir = newDir
	s.tail = newTail
	return nil
}

// writeCompacted builds the replacement log next to the live one. A crash
// anywhere before the rename leaves the old log fully intact; the orphan
// temp file is removed by the next Open.
func (s *Store) writeCompacted() (uint64, map[string]dirEntry, int64, error) {
	var order []string
	values := map[string][]byte{}
	off := int64(0)
	for off < s.tail {
		rec, n, err := readRecordAt(s.log, off)
		if err != nil {
			break
		}
		if _, seen := values[rec.key]; !seen {
			order = append(order, rec.key)
		}
		values[rec.key] = rec.value
		off += int64(n)
	}
	nf, err := s.fs.OpenFile(compactFileName)
	if err != nil {
		return 0, nil, 0, err
	}
	if err := nf.Truncate(0); err != nil {
		return 0, nil, 0, err
	}
	newGen := s.gen + 1
	newDir := map[string]dirEntry{}
	woff := int64(0)
	for _, key := range order {
		rec := record{gen: newGen, version: s.versions[key], key: key, value: values[key]}
		buf := encodeRecord(rec)
		if _, err := nf.WriteAt(buf, woff); err != nil {
			return 0, nil, 0, err
		}
		newDir[key] = dirEntry{offset: woff, version: rec.version}
		woff += int64(len(buf))
	}
	if err := nf.Sync(); err != nil {
		return 0, nil, 0, err
	}
	nf.Close()
	return newGen, newDir, woff, nil
}
