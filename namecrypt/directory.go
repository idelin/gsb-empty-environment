package namecrypt

import "sync"

// Directory applies one immutable policy and one master handle to a backing
// Store. All exported methods are safe for concurrent use.
type Directory struct {
	mu        sync.Mutex
	policy    Policy
	master    []byte
	store     Store
	hasher    Hasher
	rotating  bool
	targetGen uint64

	// RotationHook, when set, is invoked once per migrated record during
	// RotateKey/ResumeRotation. Returning an error aborts that migration at a
	// transaction boundary, simulating a crash mid-rotation; the already
	// migrated entries stay committed and the rest can be resumed.
	RotationHook func(canonical string, from, to uint64) error
}

// NewDirectory builds a directory. masterHandle is the secret keying material
// from which per-directory name keys are derived. hasher may be nil to use the
// production HMAC hasher.
func NewDirectory(p Policy, masterHandle []byte, store Store, hasher Hasher) (*Directory, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(masterHandle) == 0 {
		return nil, ErrInvalidPolicy
	}
	if store == nil {
		return nil, ErrInvalidPolicy
	}
	if hasher == nil {
		hasher = HMACHasher{}
	}
	master := append([]byte(nil), masterHandle...)
	return &Directory{policy: p, master: master, store: store, hasher: hasher}, nil
}

// Policy returns a copy of the current directory policy.
func (d *Directory) Policy() Policy {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.policy
}

// IsRotating reports whether a key rotation was started but not finished.
func (d *Directory) IsRotating() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rotating
}

func (d *Directory) nameKey(generation uint64) []byte {
	return DeriveNameKey(d.master, d.policy, generation)
}

func (d *Directory) lookupKey(canonical string, generation uint64) LookupKey {
	return d.hasher.LookupKey(d.nameKey(generation), canonical)
}

// scanGenerations lists the key generations whose lookup buckets a lookup must
// consider. During rotation both the old and the new generation are live.
func (d *Directory) scanGenerations() []uint64 {
	if d.rotating {
		return []uint64{d.targetGen, d.policy.KeyGeneration}
	}
	return []uint64{d.policy.KeyGeneration}
}

// findConflictingEntry atomically inspects every candidate in the lookup
// bucket and returns a conflicting existing record. Digest-colliding entries
// with a different canonical name coexist; the same canonical name backed by a
// different raw name (or inode) is a hard conflict.
func (d *Directory) findConflictingEntry(canonical string, generation uint64) (*Record, error) {
	bucket := d.lookupKey(canonical, generation)
	candidates, err := d.store.GetBucket(bucket)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if candidate.Canonical == canonical {
			return candidate, nil
		}
	}
	return nil, nil
}

func (d *Directory) seal(generation uint64, raw, canonical string, inode InodeRef) (*Record, error) {
	nonce, ciphertext, err := sealName(d.nameKey(generation), d.policy, generation, canonical, inode, raw)
	if err != nil {
		return nil, err
	}
	return &Record{
		EntryNonce:    nonce,
		Ciphertext:    ciphertext,
		Canonical:     canonical,
		Inode:         inode,
		KeyGeneration: generation,
	}, nil
}

// open authenticates and decrypts a record, verifying the policy digest,
// canonical hash and inode identity through the AEAD associated data.
func (d *Directory) open(rec *Record) (string, error) {
	return openName(
		d.nameKey(rec.KeyGeneration),
		d.policy,
		rec.KeyGeneration,
		rec.Canonical,
		rec.Inode,
		rec.EntryNonce,
		rec.Ciphertext,
	)
}

// Create seals raw and stores it as a new directory entry bound to inode.
func (d *Directory) Create(raw string, inode InodeRef) error {
	if inode.ID == "" {
		return ErrInodeMismatch
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.rotating {
		return ErrRotationInProgress
	}
	canonical, err := d.policy.Canonical(raw)
	if err != nil {
		return err
	}
	generation := d.policy.KeyGeneration
	existing, err := d.findConflictingEntry(canonical, generation)
	if err != nil {
		return err
	}
	if existing != nil {
		existingRaw, openErr := d.open(existing)
		if openErr != nil {
			return openErr
		}
		if existingRaw != raw || existing.Inode != inode {
			return ErrCanonicalConflict
		}
		return ErrExists
	}

	rec, err := d.seal(generation, raw, canonical, inode)
	if err != nil {
		return err
	}
	tx, err := d.store.Begin()
	if err != nil {
		return err
	}
	tx.Put(d.lookupKey(canonical, generation), rec)
	return tx.Commit()
}

// Lookup finds raw by name. A successful lookup always decrypts the record and
// verifies its inode identity.
func (d *Directory) Lookup(raw string) (InodeRef, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	canonical, err := d.policy.Canonical(raw)
	if err != nil {
		return InodeRef{}, "", err
	}
	return d.lookupLocked(raw, canonical)
}

// LookupInode behaves like Lookup but additionally requires the decrypted
// entry to point exactly at want.
func (d *Directory) LookupInode(raw string, want InodeRef) (string, error) {
	inode, decrypted, err := d.Lookup(raw)
	if err != nil {
		return "", err
	}
	if inode != want {
		return "", ErrInodeMismatch
	}
	return decrypted, nil
}

func (d *Directory) lookupLocked(raw, canonical string) (InodeRef, string, error) {
	var matched []*Record
	for _, generation := range d.scanGenerations() {
		bucket := d.lookupKey(canonical, generation)
		candidates, err := d.store.GetBucket(bucket)
		if err != nil {
			return InodeRef{}, "", err
		}
		for _, candidate := range candidates {
			if candidate.Canonical == canonical {
				matched = append(matched, candidate)
			}
		}
	}
	if len(matched) == 0 {
		return InodeRef{}, "", ErrNotFound
	}

	var lastErr error
	for _, candidate := range matched {
		decrypted, err := d.open(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		// Under CaseSensitive the stored spelling must equal the requested
		// spelling. Under CaseFoldASCII any spelling sharing the canonical name
		// resolves to the single stored entry.
		if d.policy.CaseMode == CaseSensitive && decrypted != raw {
			// Same canonical name but a different raw spelling.
			lastErr = ErrCanonicalConflict
			continue
		}
		return candidate.Inode, decrypted, nil
	}
	if lastErr == nil {
		lastErr = ErrBadRecord
	}
	return InodeRef{}, "", lastErr
}

// Rename moves oldName to newName. Cross-directory renames decrypt the source
// and reseal it under the destination policy. The source entry is removed only
// after the destination entry is durably present; any failure leaves the
// source untouched.
func (d *Directory) Rename(oldName, newName string, dst *Directory) error {
	if dst == nil {
		dst = d
	}
	first, second := d, dst
	if dst != d && dst.policy.DirectoryID < d.policy.DirectoryID {
		first, second = dst, d
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if second != first {
		second.mu.Lock()
		defer second.mu.Unlock()
	}

	srcCanonical, err := d.policy.Canonical(oldName)
	if err != nil {
		return err
	}
	dstCanonical, err := dst.policy.Canonical(newName)
	if err != nil {
		return err
	}

	sourceRec, err := d.resolveSource(oldName, srcCanonical)
	if err != nil {
		return err
	}
	if dst.rotating {
		return ErrRotationInProgress
	}
	dstGeneration := dst.policy.KeyGeneration
	if conflict, err := dst.findConflictingEntry(dstCanonical, dstGeneration); err != nil {
		return err
	} else if conflict != nil {
		return ErrExists
	}

	newRec, err := dst.seal(dstGeneration, newName, dstCanonical, sourceRec.Inode)
	if err != nil {
		return err
	}

	if d.store == dst.store {
		// Same backing store: publish destination and remove source atomically.
		tx, err := d.store.Begin()
		if err != nil {
			return err
		}
		tx.Put(dst.lookupKey(dstCanonical, dstGeneration), newRec)
		tx.Delete(d.lookupKey(srcCanonical, sourceRec.KeyGeneration), sourceRec)
		if err := tx.Commit(); err != nil {
			tx.Abort()
			return err
		}
		return nil
	}

	// Different backing stores: commit the destination first. If anything fails
	// the source record is never removed.
	dstTx, err := dst.store.Begin()
	if err != nil {
		return err
	}
	dstTx.Put(dst.lookupKey(dstCanonical, dstGeneration), newRec)
	if err := dstTx.Commit(); err != nil {
		dstTx.Abort()
		return err
	}
	srcTx, err := d.store.Begin()
	if err != nil {
		return err
	}
	srcTx.Delete(d.lookupKey(srcCanonical, sourceRec.KeyGeneration), sourceRec)
	if err := srcTx.Commit(); err != nil {
		srcTx.Abort()
		// Destination is live but source removal failed; report without
		// deleting the source so no entry is silently lost.
		return ErrBadRecord
	}
	return nil
}

func (d *Directory) resolveSource(raw, canonical string) (*Record, error) {
	var matched []*Record
	for _, generation := range d.scanGenerations() {
		bucket := d.lookupKey(canonical, generation)
		candidates, err := d.store.GetBucket(bucket)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if candidate.Canonical == canonical {
				matched = append(matched, candidate)
			}
		}
	}
	if len(matched) == 0 {
		return nil, ErrNotFound
	}
	for _, candidate := range matched {
		decrypted, err := d.open(candidate)
		if err != nil {
			continue
		}
		if d.policy.CaseMode == CaseFoldASCII || decrypted == raw {
			return candidate, nil
		}
	}
	return nil, ErrNotFound
}

// RotateKey advances the directory to newGeneration and migrates every entry
// one at a time, each in its own committed transaction. If RotationHook stops
// the run, call ResumeRotation to continue; until then both generations serve
// lookups and writes are refused.
func (d *Directory) RotateKey(newGeneration uint64) error {
	d.mu.Lock()
	if newGeneration <= d.policy.KeyGeneration {
		d.mu.Unlock()
		return ErrInvalidPolicy
	}
	if d.rotating {
		d.mu.Unlock()
		return ErrRotationInProgress
	}
	d.rotating = true
	d.targetGen = newGeneration
	d.mu.Unlock()

	return d.resumeLocked()
}

// ResumeRotation continues an interrupted rotation.
func (d *Directory) ResumeRotation() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.rotating {
		return ErrInvalidPolicy
	}
	return d.resumeLocked()
}

func (d *Directory) resumeLocked() error {
	oldGeneration := d.policy.KeyGeneration
	newGeneration := d.targetGen

	all, err := d.snapshotRecords()
	if err != nil {
		return err
	}
	for _, rec := range all {
		if rec.KeyGeneration != oldGeneration {
			continue
		}
		raw, err := d.open(rec)
		if err != nil {
			return err
		}
		migrated, err := d.seal(newGeneration, raw, rec.Canonical, rec.Inode)
		if err != nil {
			return err
		}
		if d.RotationHook != nil {
			if err := d.RotationHook(rec.Canonical, oldGeneration, newGeneration); err != nil {
				return err
			}
		}
		tx, err := d.store.Begin()
		if err != nil {
			return err
		}
		tx.Put(d.lookupKey(rec.Canonical, newGeneration), migrated)
		tx.Delete(d.lookupKey(rec.Canonical, oldGeneration), rec)
		if err := tx.Commit(); err != nil {
			tx.Abort()
			return err
		}
	}

	d.policy.KeyGeneration = newGeneration
	d.rotating = false
	d.targetGen = 0
	return nil
}

// RecordEnumerator is implemented by stores that can list every live record.
// Key rotation needs a full enumeration to migrate entries one by one.
type RecordEnumerator interface {
	AllRecords() []*Record
}

func (d *Directory) snapshotRecords() ([]*Record, error) {
	enum, ok := d.store.(RecordEnumerator)
	if !ok {
		return nil, ErrBadRecord
	}
	return enum.AllRecords(), nil
}
