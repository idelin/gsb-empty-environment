package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
)

// Hooks are optional observation points used to inject faults and to verify
// transactional behavior. A nil hook is skipped. Hooks must not block on the
// Volume or they will deadlock.
type Hooks struct {
	// AfterRead fires once a block has been read and its reader pin acquired.
	AfterRead func(blockID string, rec Record)
	// AfterMigrateRead fires inside MigrateBlock immediately after reading the
	// current record, before any re-encryption. It marks the read checkpoint of
	// a migration transaction for restart/fault tests.
	AfterMigrateRead func(blockID string, rec Record)
	// BeforeCommit fires after sealing but before the compare-and-swap commit
	// of a foreground write. Returning an error aborts the write with no
	// on-disk change.
	BeforeWriteCommit func(blockID string, next Record) error
	// BeforeMigrateCommit fires after re-encryption but before a migration
	// commit. Returning an error aborts this migration attempt; the on-disk
	// record is untouched and MigrateBlock will retry after re-reading.
	BeforeMigrateCommit func(blockID string, old, next Record) error
	// OnMigrateConflict fires whenever a migration commit loses the
	// compare-and-swap to a concurrent writer and must re-read.
	OnMigrateConflict func(blockID string, current Record)
}

// Volume is the transactional block host. It coordinates a Store, a KeyRing
// and an Encryptor so that concurrent foreground writes and background
// re-encryption can never clobber one another.
type Volume struct {
	store Store
	ring  *KeyRing
	enc   Encryptor
	hooks Hooks
}

// NewVolume assembles a Volume and reconciles the ring with any blocks
// already present in store (important after a restart).
func NewVolume(ctx context.Context, store Store, ring *KeyRing, enc Encryptor, hooks Hooks) (*Volume, error) {
	if err := ring.Reconcile(ctx, store); err != nil {
		return nil, err
	}
	return &Volume{store: store, ring: ring, enc: enc, hooks: hooks}, nil
}

// CurrentGen returns the generation under which new writes are sealed.
func (v *Volume) CurrentGen() KeyGen { return v.ring.Current() }

// Ring exposes the key ring for lifecycle operations such as Rotate.
func (v *Volume) Ring() *KeyRing { return v.ring }

// Store exposes the backing store.
func (v *Volume) Store() Store { return v.store }

// digest returns the SHA-256 content digest of plaintext.
func digest(plaintext []byte) Digest {
	return sha256.Sum256(plaintext)
}

// Read opens a block. The generation that sealed it is pinned for the
// duration of the read so its key cannot be destroyed while the ciphertext is
// being opened. The returned plaintext is verified against the version digest.
func (v *Volume) Read(ctx context.Context, blockID string) ([]byte, error) {
	rec, err := v.store.Get(ctx, blockID)
	if err != nil {
		return nil, err
	}
	gen := rec.Version.Gen
	if err := v.ring.PinReader(gen); err != nil {
		return nil, err
	}
	defer v.ring.ReleaseReader(gen)

	if v.hooks.AfterRead != nil {
		v.hooks.AfterRead(blockID, rec)
	}

	plaintext, err := v.enc.Decrypt(ctx, gen, rec.Ciphertext)
	if err != nil {
		return nil, err
	}
	if digest(plaintext) != rec.Version.Sum {
		return nil, errors.New("netsec: content digest mismatch on read")
	}
	return plaintext, nil
}

// Write seals plaintext under the current generation and commits it with a
// compare-and-swap. It re-reads and retries if a concurrent writer (or a
// migration) changed the block in the meantime, so the committed version is
// always derived from the latest on-disk sequence.
func (v *Volume) Write(ctx context.Context, blockID string, plaintext []byte) (BlockVersion, error) {
	gen := v.ring.Current()
	sum := digest(plaintext)

	var expected BlockVersion
	rec, err := v.store.Get(ctx, blockID)
	switch {
	case err == nil:
		expected = rec.Version
	case errors.Is(err, ErrNotFound):
		expected = BlockVersion{}
	default:
		return BlockVersion{}, err
	}

	for {
		if err := ctx.Err(); err != nil {
			return BlockVersion{}, err
		}
		ciphertext, err := v.enc.Encrypt(ctx, gen, plaintext)
		if err != nil {
			return BlockVersion{}, err
		}
		next := Record{
			BlockID: blockID,
			Version: BlockVersion{
				Gen: gen,
				Seq: expected.Seq + 1,
				Sum: sum,
			},
			Ciphertext: ciphertext,
		}
		if v.hooks.BeforeWriteCommit != nil {
			if err := v.hooks.BeforeWriteCommit(blockID, next); err != nil {
				return BlockVersion{}, err
			}
		}
		current, cerr := v.store.CompareAndSwap(ctx, blockID, expected, next)
		if cerr == nil {
			if expected == (BlockVersion{}) {
				v.ring.AddBlock(gen)
			} else {
				v.ring.MoveBlock(expected.Gen, gen)
			}
			return next.Version, nil
		}
		if !errors.Is(cerr, ErrConflict) {
			return BlockVersion{}, cerr
		}
		// Lost the race: re-read the current version and rebuild against it.
		expected = current.Version
	}
}

// MigrateBlock re-encrypts one block to the current generation as an
// optimistic transaction:
//
//  1. Read the current record and its version.
//  2. Open it with the old generation's key and verify the content digest.
//  3. Re-seal the identical plaintext under the current generation.
//  4. Commit only if the on-disk version still equals the version read in
//     step 1. On any mismatch, discard the work and re-read.
//
// The plaintext content (and therefore the digest) is preserved; only the key
// generation, ciphertext and sequence change. It returns the new version, or
// the existing version unchanged if the block is already current.
func (v *Volume) MigrateBlock(ctx context.Context, blockID string) (BlockVersion, error) {
	for {
		if err := ctx.Err(); err != nil {
			return BlockVersion{}, err
		}
		rec, err := v.store.Get(ctx, blockID)
		if err != nil {
			return BlockVersion{}, err
		}
		oldVersion := rec.Version
		target := v.ring.Current()
		if oldVersion.Gen == target {
			return oldVersion, nil
		}
		if v.hooks.AfterMigrateRead != nil {
			v.hooks.AfterMigrateRead(blockID, rec)
		}

		// Pin the old generation while its key is needed, keeping it alive
		// even if the last reference would otherwise drain concurrently.
		if err := v.ring.PinReader(oldVersion.Gen); err != nil {
			return BlockVersion{}, err
		}
		plaintext, derr := v.enc.Decrypt(ctx, oldVersion.Gen, rec.Ciphertext)
		if derr != nil {
			v.ring.ReleaseReader(oldVersion.Gen)
			return BlockVersion{}, derr
		}
		if digest(plaintext) != oldVersion.Sum {
			v.ring.ReleaseReader(oldVersion.Gen)
			return BlockVersion{}, errors.New("netsec: content digest mismatch during migration")
		}

		ciphertext, eerr := v.enc.Encrypt(ctx, target, plaintext)
		v.ring.ReleaseReader(oldVersion.Gen)
		if eerr != nil {
			return BlockVersion{}, eerr
		}

		next := Record{
			BlockID: blockID,
			Version: BlockVersion{
				Gen: target,
				Seq: oldVersion.Seq + 1,
				Sum: oldVersion.Sum,
			},
			Ciphertext: ciphertext,
		}
		if v.hooks.BeforeMigrateCommit != nil {
			if err := v.hooks.BeforeMigrateCommit(blockID, rec, next); err != nil {
				return BlockVersion{}, err
			}
		}

		current, cerr := v.store.CompareAndSwap(ctx, blockID, oldVersion, next)
		if cerr == nil {
			v.ring.MoveBlock(oldVersion.Gen, target)
			return next.Version, nil
		}
		if !errors.Is(cerr, ErrConflict) {
			return BlockVersion{}, cerr
		}
		// A concurrent writer moved the block. Never overwrite it: re-read.
		if v.hooks.OnMigrateConflict != nil {
			v.hooks.OnMigrateConflict(blockID, current)
		}
	}
}

// MigrateAll re-encrypts every block still sealed under an old generation.
// Blocks already current are skipped. It returns the number of blocks
// migrated (excluding no-op skips).
func (v *Volume) MigrateAll(ctx context.Context) (int, error) {
	records, err := v.store.List(ctx)
	if err != nil {
		return 0, err
	}
	current := v.ring.Current()
	migrated := 0
	for _, rec := range records {
		if rec.Version.Gen == current {
			continue
		}
		if _, err := v.MigrateBlock(ctx, rec.BlockID); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

// VerifyContent re-reads every block, opens it and confirms the plaintext
// digest matches the version stamp. It is used after restart to prove no
// migration overwrote or corrupted a concurrent write.
func (v *Volume) VerifyContent(ctx context.Context) error {
	records, err := v.store.List(ctx)
	if err != nil {
		return err
	}
	for _, rec := range records {
		gen := rec.Version.Gen
		if err := v.ring.PinReader(gen); err != nil {
			return err
		}
		plaintext, err := v.enc.Decrypt(ctx, gen, rec.Ciphertext)
		ok := err == nil && digest(plaintext) == rec.Version.Sum
		v.ring.ReleaseReader(gen)
		if !ok {
			if err != nil {
				return err
			}
			return errors.New("netsec: digest verification failed for block " + rec.BlockID)
		}
	}
	return nil
}
