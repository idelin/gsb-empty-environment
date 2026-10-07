package encobj

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// Snapshot is a consistent, fully decrypted read view of one object
// version. It holds a lease on every key it used; those keys cannot be
// retired until Close. The plaintext is assembled only after the manifest
// and every referenced field authenticate: a failed read delivers nothing.
type Snapshot struct {
	objID   string
	version uint64
	fields  map[string][]byte
	release func()
	once    sync.Once
}

func (sn *Snapshot) Version() uint64 { return sn.version }

// Get returns a copy of the field value.
func (sn *Snapshot) Get(name string) ([]byte, bool) {
	v, ok := sn.fields[name]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// Fields returns a copy of the full field map.
func (sn *Snapshot) Fields() map[string][]byte {
	out := make(map[string][]byte, len(sn.fields))
	for k, v := range sn.fields {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// Close releases key leases and file pins.
func (sn *Snapshot) Close() { sn.once.Do(sn.release) }

// Snapshot reads and verifies the current record, the manifest and every
// referenced field from disk, then delivers the complete object. Any
// authentication failure returns an error and no plaintext.
func (s *Store) Snapshot(objID string) (*Snapshot, error) {
	if err := s.checkLive(); err != nil {
		return nil, err
	}
	o := s.obj(objID)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur == nil {
		return nil, ErrObjectNotFound
	}
	// Re-read the committed-version index from disk and require it to
	// match the in-memory index: a replaced or rolled-back current file
	// is detected here, locally, without consulting AEAD freshness.
	rawCur, err := s.st.ReadFile(objPrefix(objID) + "current")
	if err != nil {
		return nil, err
	}
	diskCur, err := decodeCurrent(rawCur)
	if err != nil {
		return nil, err
	}
	if *diskCur != *o.cur {
		return nil, fmt.Errorf("%w: current record diverges from committed index", ErrCorrupt)
	}
	mbody, err := s.st.ReadFile(objPrefix(objID) + o.cur.ManifestFile)
	if err != nil {
		return nil, err
	}
	if sha256.Sum256(mbody) != o.cur.ManifestDigest {
		return nil, fmt.Errorf("%w: manifest replaced or stale", ErrAuth)
	}
	m, err := s.verifyManifest(objID, o.cur, mbody)
	if err != nil {
		return nil, err
	}
	// Verify and decrypt every field before delivering anything.
	plaintext := make(map[string][]byte, len(m.Fields))
	var keyIDs []string
	keyIDs = append(keyIDs, m.KeyID)
	for _, f := range m.Fields {
		body, err := s.st.ReadFile(objPrefix(objID) + f.File)
		if err != nil {
			return nil, fmt.Errorf("%w: field %s unreadable: %v", ErrAuth, f.Name, err)
		}
		if sha256.Sum256(body) != f.Digest {
			return nil, fmt.Errorf("%w: field %s digest mismatch", ErrAuth, f.Name)
		}
		pt, keyID, err := s.openField(objID, f, body)
		if err != nil {
			return nil, err
		}
		plaintext[f.Name] = pt
		keyIDs = append(keyIDs, keyID)
	}
	release, err := s.keys.acquire(keyIDs)
	if err != nil {
		return nil, err
	}
	// Pin the files so reclamation cannot remove them under this snapshot.
	files := filesOf(o.cur, m)
	for _, f := range files {
		o.pinFiles[f]++
	}
	sn := &Snapshot{
		objID:   objID,
		version: m.Version,
		fields:  plaintext,
	}
	sn.release = func() {
		release()
		o.mu.Lock()
		for _, f := range files {
			o.pinFiles[f]--
			if o.pinFiles[f] <= 0 {
				delete(o.pinFiles, f)
			}
		}
		// If the object was deleted or rotated while pinned, reclaim now.
		s.gcObjectLocked(o, files)
		o.mu.Unlock()
	}
	return sn, nil
}
