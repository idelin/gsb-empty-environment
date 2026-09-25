package kvdb

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
)

const (
	manifestFile = "MANIFEST"
	manifestTmp  = manifestFile + ".tmp"
)

// manifest is the committed set of live segments. It is written to a temp
// file, fsynced, then atomically renamed over MANIFEST, followed by an
// fsync of the directory so the rename itself is durable.
type manifest struct {
	Segments []uint64 `json:"segments"`
}

func readManifest(dir string) (*manifest, error) {
	data, err := ioutil.ReadFile(filepath.Join(dir, manifestFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// writeManifestLocked persists the current db.order as the committed
// segment set. Callers must hold db.mu.
func (db *DB) writeManifestLocked() error {
	if err := db.inject(InjectBeforeManifestWrite); err != nil {
		return err
	}
	m := manifest{Segments: append([]uint64(nil), db.order...)}
	data, err := json.Marshal(&m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(db.dir, manifestTmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
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
	if err := os.Rename(tmp, filepath.Join(db.dir, manifestFile)); err != nil {
		return err
	}
	return syncDir(db.dir)
}

// syncDir fsyncs a directory so file creations and renames inside it are
// durable across a power failure.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
