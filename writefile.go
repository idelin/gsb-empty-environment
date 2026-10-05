package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to name via a temp file + rename so the
// manifest is never observed half-written.
func writeFileAtomic(name string, data []byte) error {
	tmp, err := ioutil.TempFile(filepath.Dir(name), ".archmerge-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}
