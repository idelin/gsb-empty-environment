package cleaner

import (
	"os"
	"sort"
	"syscall"
)

// OSFS implements FS against the real operating-system filesystem.
type OSFS struct{}

func (OSFS) Stat(path string) (FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileInfo{}, err
	}
	info := FileInfo{
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		info.ID = FileID{Dev: uint64(st.Dev), Ino: st.Ino}
	}
	return info, nil
}

func (OSFS) ReadDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (OSFS) RemoveFile(path string) error { return os.Remove(path) }

// RemoveDir uses rmdir semantics: it fails on non-empty directories.
func (OSFS) RemoveDir(path string) error { return syscall.Rmdir(path) }
