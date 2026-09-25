package pagebackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Restore rebuilds a database from a backup chain (one full backup
// followed by zero or more incrementals in version order) into
// targetDir, which must not exist yet.
//
// Guarantees:
//   - Every manifest checksum, every page payload hash, the version
//     sequence (base/target links) and the per-step baseline digest
//     are verified. Any corruption, gap or reordering aborts the
//     restore.
//   - The chain is applied in a staging directory next to targetDir
//     and only atomically renamed into place after full verification,
//     so a failed or crashed restore never leaves a seemingly usable
//     partial database at targetDir.
//   - Re-applying an incremental whose Target equals the current
//     version is an idempotent no-op.
func Restore(chain []string, targetDir string) error {
	if len(chain) == 0 {
		return fmt.Errorf("empty backup chain")
	}
	// Crash recovery: drop staging areas left behind by interrupted
	// runs. They are never valid databases.
	stale, _ := filepath.Glob(targetDir + ".staging.*")
	for _, s := range stale {
		os.RemoveAll(s)
	}

	if _, err := os.Stat(targetDir); err == nil {
		return fmt.Errorf("target dir %s already exists", targetDir)
	}
	staging, err := stagingPath(targetDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pagesDir(staging), 0755); err != nil {
		return err
	}
	// Note: cleanup happens only on the error path. A panic from
	// CrashInject deliberately leaves the staging dir behind to
	// simulate a crash; the next Restore call reclaims it above.
	fail := func(err error) error {
		os.RemoveAll(staging)
		return err
	}

	var cur uint64
	started := false
	mod := map[uint32]uint64{}

	for i, bdir := range chain {
		m, err := loadManifest(bdir)
		if err != nil {
			return fail(fmt.Errorf("backup %d (%s): %v", i, bdir, err))
		}

		// Verify every page payload against the manifest before use.
		for id, want := range m.Pages {
			data, err := ioutil.ReadFile(pagePath(bdir, id))
			if err != nil {
				return fail(fmt.Errorf("backup %d: %v", i, err))
			}
			sum := sha256.Sum256(data)
			if got := hex.EncodeToString(sum[:]); got != want {
				return fail(fmt.Errorf("backup %d: page %d corrupt: manifest hash %s, got %s", i, id, want, got))
			}
		}

		switch {
		case !started && m.Type == ManifestFull:
			started = true
			cur = m.Target
			if err := applyPages(staging, bdir, m, mod); err != nil {
				return fail(err)
			}
		case started && m.Type == ManifestIncr && m.Target == cur:
			// Already applied: idempotent no-op.
		case started && m.Type == ManifestIncr && m.Base == cur:
			cur = m.Target
			if err := applyPages(staging, bdir, m, mod); err != nil {
				return fail(err)
			}
		case !started:
			return fail(fmt.Errorf("backup %d: chain must start with a full backup, got %s (base=%d target=%d)", i, m.Type, m.Base, m.Target))
		default:
			return fail(fmt.Errorf("backup %d: broken chain: %s base=%d target=%d cannot apply on top of version %d (missing or out-of-order link)", i, m.Type, m.Base, m.Target, cur))
		}

		// Per-step baseline digest verification: the reconstructed
		// database must match the digest recorded in the manifest.
		ids, err := listPageIDs(pagesDir(staging))
		if err != nil {
			return fail(err)
		}
		digest, err := digestPages(pagesDir(staging), ids)
		if err != nil {
			return fail(err)
		}
		if digest != m.Digest {
			return fail(fmt.Errorf("backup %d: digest mismatch after apply: manifest %s, reconstructed %s", i, m.Digest, digest))
		}
		if err := inject(PointRestoreBackupApplied); err != nil {
			return fail(err)
		}
	}
	if !started {
		return fail(fmt.Errorf("chain contains no full backup"))
	}
	if err := inject(PointRestoreBeforeCommit); err != nil {
		return fail(err)
	}

	state := dbState{Version: cur, Mod: mod}
	data, err := json.Marshal(state)
	if err != nil {
		return fail(err)
	}
	if err := writeFileAtomic(statePath(staging), data); err != nil {
		return fail(err)
	}
	if err := fsyncDir(pagesDir(staging)); err != nil {
		return fail(err)
	}
	if err := fsyncDir(staging); err != nil {
		return fail(err)
	}
	if err := os.Rename(staging, targetDir); err != nil {
		return fail(err)
	}
	return fsyncDir(filepath.Dir(targetDir))
}

func applyPages(staging, bdir string, m *Manifest, mod map[uint32]uint64) error {
	for id := range m.Pages {
		data, err := ioutil.ReadFile(pagePath(bdir, id))
		if err != nil {
			return err
		}
		if err := writeFileAtomic(pagePath(staging, id), data); err != nil {
			return err
		}
		mod[id] = m.Target
	}
	return nil
}
