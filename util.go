package pagebackup

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

// writeFileAtomic writes data to path via a temp file + fsync + rename,
// so path never contains a partially written file.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// fsyncDir flushes a directory's metadata (dirents) to stable storage.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// stagingPath returns a unique staging directory name next to final.
func stagingPath(final string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return final + ".staging." + hex.EncodeToString(b[:]), nil
}
