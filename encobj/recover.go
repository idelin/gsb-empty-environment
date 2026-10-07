package encobj

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"
)

// recover loads the nonce allocator (already done by openNonceAlloc), the
// idempotency index and every object's committed state, then reclaims
// garbage left by torn commits: *.tmp-* staging files and unreferenced
// field/manifest files. Recovery only ever exposes complete old or new
// objects, never a splice of two versions.
func (s *Store) recover() error {
	if err := s.recoverOpsLog(); err != nil {
		return err
	}
	names, err := s.st.ReadFileNames()
	if err != nil {
		return err
	}
	// Group object files.
	byObj := map[string][]string{}
	var stray []string
	for _, n := range names {
		if strings.Contains(n, ".tmp-") {
			stray = append(stray, n) // incomplete atomic write staging
			continue
		}
		objID, file, ok := splitObjFile(n)
		if !ok {
			continue // writer.lock, nonce.log, ops.log, ...
		}
		byObj[objID] = append(byObj[objID], file)
		_ = file
	}
	for objID := range byObj {
		if err := s.recoverObject(objID, byObj[objID]); err != nil {
			return err
		}
	}
	// Reclaim staging files and unreferenced ciphertexts.
	for _, n := range stray {
		s.st.RemoveFile(n)
	}
	for objID, files := range byObj {
		o := s.objs[objID]
		keep := map[string]bool{"current": true}
		if o != nil && o.cur != nil {
			keep[o.cur.ManifestFile] = true
			for _, f := range o.m.Fields {
				keep[f.File] = true
			}
		}
		for _, f := range files {
			if !keep[f] {
				s.st.RemoveFile(objPrefix(objID) + f)
			}
		}
	}
	return s.st.SyncDir()
}

// recoverObject loads the committed-version index and the exact manifest it
// points to. Anything else (missing file, digest mismatch, AEAD failure,
// undecodable content) refuses recovery of the store with ErrCorrupt.
func (s *Store) recoverObject(objID string, files []string) error {
	have := map[string]bool{}
	for _, f := range files {
		have[f] = true
	}
	if !have["current"] {
		return nil // never committed or fully deleted; leftovers GC'd by caller
	}
	raw, err := s.st.ReadFile(objPrefix(objID) + "current")
	if err != nil {
		return err
	}
	cur, err := decodeCurrent(raw)
	if err != nil {
		return err
	}
	mbody, err := s.st.ReadFile(objPrefix(objID) + cur.ManifestFile)
	if err != nil {
		return fmt.Errorf("%w: manifest %s: %v", ErrCorrupt, cur.ManifestFile, err)
	}
	if sha256.Sum256(mbody) != cur.ManifestDigest {
		return fmt.Errorf("%w: manifest digest mismatch", ErrCorrupt)
	}
	m, err := s.verifyManifest(objID, cur, mbody)
	if err != nil {
		return err
	}
	// Every referenced field file must exist and verify; otherwise the
	// committed object is incomplete and we refuse to start.
	for _, f := range m.Fields {
		body, err := s.st.ReadFile(objPrefix(objID) + f.File)
		if err != nil {
			return fmt.Errorf("%w: field file %s: %v", ErrCorrupt, f.File, err)
		}
		if sha256.Sum256(body) != f.Digest {
			return fmt.Errorf("%w: field digest mismatch %s", ErrCorrupt, f.File)
		}
		if _, _, err := s.openField(objID, f, body); err != nil {
			return fmt.Errorf("%w: field %s: %v", ErrCorrupt, f.File, err)
		}
	}
	o := s.obj(objID)
	o.mu.Lock()
	o.cur, o.m = cur, m
	o.mu.Unlock()
	// Fold the manifest's own idempotency record into the index (covers a
	// crash between current.write and ops.append).
	if m.OpID != "" {
		s.opsMu.Lock()
		if rec, ok := s.ops[m.OpID]; ok && rec.digest != m.OpDigest {
			s.opsMu.Unlock()
			return fmt.Errorf("%w: op %s digest mismatch between ops.log and manifest", ErrCorrupt, m.OpID)
		}
		s.ops[m.OpID] = opRec{version: m.Version, digest: m.OpDigest}
		s.opsMu.Unlock()
	}
	return nil
}

// verifyManifest authenticates and decodes a manifest body against the
// committed-version index record.
func (s *Store) verifyManifest(objID string, cur *currentRecord, body []byte) (*manifest, error) {
	keyID, _, pt, err := openSealed(manifestMagic, body, func(hdrKeyID string) []byte {
		return manifestAD(objID, cur.Version, hdrKeyID)
	}, s.keys)
	if err != nil {
		return nil, err
	}
	m, err := decodeManifest(pt)
	if err != nil {
		return nil, err
	}
	// Cross-check self-describing content against the index.
	if m.Version != cur.Version || m.KeyID != keyID {
		return nil, fmt.Errorf("%w: manifest header/content mismatch", ErrCorrupt)
	}
	return m, nil
}

// openField authenticates one field file against its manifest reference.
func (s *Store) openField(objID string, f fieldRef, body []byte) ([]byte, string, error) {
	keyID, _, pt, err := openSealed(fieldMagic, body, func(hdrKeyID string) []byte {
		return fieldAD(objID, f.FieldID, f.Revision, hdrKeyID)
	}, s.keys)
	if err != nil {
		return nil, "", err
	}
	if keyID != f.KeyID {
		return nil, "", fmt.Errorf("%w: field key ID mismatch", ErrAuth)
	}
	return pt, keyID, nil
}

// recoverOpsLog validates ops.log, truncates a torn tail and rejects
// mid-log corruption.
func (s *Store) recoverOpsLog() error {
	raw, err := s.st.ReadFile("ops.log")
	if err == ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	off := 0
	valid := 0
	for off < len(raw) {
		if len(raw)-off < 4 {
			break // torn tail
		}
		sum := binary.LittleEndian.Uint32(raw[off:])
		d := &decoder{buf: raw[off+4:]}
		version := d.u64()
		opID := d.str()
		digestB := d.bytes()
		if d.err != nil || len(digestB) != 32 {
			break // torn tail (short record)
		}
		recLen := len(raw[off+4:]) - len(d.buf) + 4
		if crc32.ChecksumIEEE(raw[off+4:off+recLen]) != sum {
			if off+recLen == len(raw) {
				break // torn tail
			}
			return fmt.Errorf("%w: ops.log bad crc at offset %d", ErrCorrupt, off)
		}
		var digest [32]byte
		copy(digest[:], digestB)
		s.opsMu.Lock()
		if rec, ok := s.ops[opID]; ok && rec.digest != digest {
			s.opsMu.Unlock()
			return fmt.Errorf("%w: ops.log conflicting op %s", ErrCorrupt, opID)
		}
		s.ops[opID] = opRec{version: version, digest: digest}
		s.opsMu.Unlock()
		valid = off + recLen
		off += recLen
	}
	if valid != len(raw) {
		if err := s.st.WriteFileAtomic("ops.log", raw[:valid]); err != nil {
			return err
		}
	}
	return nil
}
