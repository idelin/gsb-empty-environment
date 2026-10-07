package encobj

import (
	"crypto/sha256"
	"sort"
)

// RotateObject re-encrypts one object whose manifest or fields still use a
// non-active key. It returns true when a rotation commit happened.
//
// The candidate is built from the version read at the start; installation
// compares the still-current version under the object lock and atomically
// replaces the index record only when it is unchanged. A foreground commit
// in between invalidates the candidate, which is discarded and retried, so
// stale plaintext can never overwrite a newer version. Rotation keeps the
// object version number: it re-commits the same logical content under the
// new key.
func (s *Store) RotateObject(objID string) (bool, error) {
	if err := s.checkLive(); err != nil {
		return false, err
	}
	o := s.obj(objID)
	for attempt := 0; attempt < 8; attempt++ {
		o.mu.Lock()
		if o.cur == nil {
			o.mu.Unlock()
			return false, ErrObjectNotFound
		}
		base := *o.cur
		m0 := o.m
		o.mu.Unlock()

		_, active, err := s.keys.active()
		if err != nil {
			return false, err
		}
		if m0.KeyID == active.id && allFieldsOn(m0, active.id) {
			return false, nil // already current
		}

		// Build the candidate outside the object lock.
		newFields := make([]fieldRef, 0, len(m0.Fields))
		var staged []string
		fail := func(err error) (bool, error) {
			o.unpin(staged...)
			for _, f := range staged {
				s.st.RemoveFile(objPrefix(objID) + f)
			}
			s.st.SyncDir()
			return false, err
		}
		for _, f := range m0.Fields {
			if f.KeyID == active.id {
				newFields = append(newFields, f)
				continue
			}
			body, err := s.st.ReadFile(objPrefix(objID) + f.File)
			if err != nil {
				return fail(err)
			}
			if sha256.Sum256(body) != f.Digest {
				return fail(ErrAuth)
			}
			pt, _, err := s.openField(objID, f, body)
			if err != nil {
				return fail(err)
			}
			nonce, err := s.alloc.Next()
			if err != nil {
				return fail(err)
			}
			nf := f
			nf.KeyID = active.id
			ad := fieldAD(objID, nf.FieldID, nf.Revision, active.id)
			nbody := sealFile(fieldMagic, active.id, nonce[:], pt, ad, active.aead)
			if s.hookEncrypt != nil {
				s.hookEncrypt(active.id, nonce)
			}
			nf.File = fieldFileName(nf.FieldID, nf.Revision, nbody)
			nf.Digest = sha256.Sum256(nbody)
			// Pin before writing so a concurrent commit's reclamation
			// cannot remove the staged candidate.
			o.pin(nf.File)
			if err := s.persistHook("rotate.field.write"); err != nil {
				o.unpin(nf.File)
				return fail(err)
			}
			if err := s.st.WriteFileAtomic(objPrefix(objID)+nf.File, nbody); err != nil {
				o.unpin(nf.File)
				return fail(err)
			}
			staged = append(staged, nf.File)
			newFields = append(newFields, nf)
		}
		sort.Slice(newFields, func(i, j int) bool { return newFields[i].Name < newFields[j].Name })
		cand := &manifest{
			Version:  m0.Version,
			KeyID:    active.id,
			OpID:     m0.OpID,
			OpDigest: m0.OpDigest,
			NextFID:  m0.NextFID,
			Fields:   newFields,
		}
		mnonce, err := s.alloc.Next()
		if err != nil {
			return fail(err)
		}
		mbody := sealFile(manifestMagic, active.id, mnonce[:], cand.encode(), manifestAD(objID, cand.Version, active.id), active.aead)
		if s.hookEncrypt != nil {
			s.hookEncrypt(active.id, mnonce)
		}
		mfile := manifestFileName(cand.Version, mbody)
		o.pin(mfile)
		if err := s.persistHook("rotate.manifest.write"); err != nil {
			o.unpin(mfile)
			return fail(err)
		}
		if err := s.st.WriteFileAtomic(objPrefix(objID)+mfile, mbody); err != nil {
			o.unpin(mfile)
			return fail(err)
		}
		staged = append(staged, mfile)
		newCur := &currentRecord{Version: cand.Version, ManifestFile: mfile, ManifestDigest: sha256.Sum256(mbody)}

		// Compare-and-swap on the committed version.
		o.mu.Lock()
		if o.cur == nil || o.cur.Version != base.Version || o.cur.ManifestDigest != base.ManifestDigest {
			// Foreground commit (or delete, or a racing rotation) won:
			// discard the stale candidate and retry from fresh state.
			o.mu.Unlock()
			o.unpin(staged...)
			for _, f := range staged {
				s.st.RemoveFile(objPrefix(objID) + f)
			}
			s.st.SyncDir()
			continue
		}
		if err := s.persistHook("rotate.current.write"); err != nil {
			o.mu.Unlock()
			return fail(err)
		}
		if err := s.st.WriteFileAtomic(objPrefix(objID)+"current", encodeCurrent(newCur)); err != nil {
			o.mu.Unlock()
			return fail(err)
		}
		oldFiles := filesOf(o.cur, o.m)
		o.cur, o.m = newCur, cand
		s.gcObjectLocked(o, oldFiles)
		o.mu.Unlock()
		o.unpin(staged...) // now referenced by the committed manifest
		return true, nil
	}
	return false, nil // contended; a later pass will finish
}

func allFieldsOn(m *manifest, keyID string) bool {
	for _, f := range m.Fields {
		if f.KeyID != keyID {
			return false
		}
	}
	return true
}

// RotateAll rotates every object not yet on the active key. It returns the
// number of objects rotated.
func (s *Store) RotateAll() (int, error) {
	if err := s.checkLive(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	states := make([]*objState, 0, len(s.objs))
	for _, o := range s.objs {
		states = append(states, o)
	}
	s.mu.Unlock()
	var ids []string
	for _, o := range states {
		o.mu.Lock()
		if o.cur != nil {
			ids = append(ids, o.id)
		}
		o.mu.Unlock()
	}
	sort.Strings(ids)
	n := 0
	for _, id := range ids {
		rot, err := s.RotateObject(id)
		if err != nil {
			return n, err
		}
		if rot {
			n++
		}
	}
	return n, nil
}
