package pagebackup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// ExportFull writes a full backup of the database at its current
// version into backupDir. backupDir must not exist yet; the backup
// appears atomically (staging dir + rename) or not at all.
func ExportFull(db *DB, backupDir string) error {
	ids, err := db.PageIDs()
	if err != nil {
		return err
	}
	m := &Manifest{Type: ManifestFull, Base: 0, Target: db.Version(), Pages: map[uint32]string{}}
	return exportPages(db, backupDir, ids, m)
}

// ExportIncremental writes an incremental backup containing the pages
// changed since base, taking the database from base to its current
// version. base must be older than the current version.
func ExportIncremental(db *DB, backupDir string, base uint64) error {
	if base >= db.Version() {
		return fmt.Errorf("base version %d must be older than current version %d", base, db.Version())
	}
	ids, err := db.ChangedSince(base)
	if err != nil {
		return err
	}
	m := &Manifest{Type: ManifestIncr, Base: base, Target: db.Version(), Pages: map[uint32]string{}}
	return exportPages(db, backupDir, ids, m)
}

func exportPages(db *DB, backupDir string, ids []uint32, m *Manifest) error {
	if _, err := os.Stat(backupDir); err == nil {
		return fmt.Errorf("backup dir %s already exists", backupDir)
	}
	staging, err := stagingPath(backupDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pagesDir(staging), 0755); err != nil {
		return err
	}
	fail := func(err error) error {
		os.RemoveAll(staging)
		return err
	}

	for _, id := range ids {
		data, err := db.ReadPage(id)
		if err != nil {
			return fail(err)
		}
		sum := sha256.Sum256(data)
		m.Pages[id] = hex.EncodeToString(sum[:])
		if err := writeFileAtomic(pagePath(staging, id), data); err != nil {
			return fail(err)
		}
	}
	if err := inject(PointExportPagesWritten); err != nil {
		return fail(err)
	}

	digest, err := db.Digest()
	if err != nil {
		return fail(err)
	}
	m.Digest = digest
	if err := writeManifest(staging, m); err != nil {
		return fail(err)
	}
	if err := inject(PointExportManifestCommit); err != nil {
		return fail(err)
	}

	if err := fsyncDir(pagesDir(staging)); err != nil {
		return fail(err)
	}
	if err := os.Rename(staging, backupDir); err != nil {
		return fail(err)
	}
	return fsyncDir(filepath.Dir(backupDir))
}
