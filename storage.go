package roaring

import (
	"io"
	"os"
)

// Storage persists a Bitmap to disk with crash-safe semantics:
//
//   - the serialized image is written to "<path>.tmp" and fsynced;
//   - the previous image is copied to "<path>.bak";
//   - the temp file is atomically renamed over "<path>" (the commit point).
//
// A crash before the rename leaves the old image untouched; a torn or
// corrupted main file is detected via CRC32 on Load and transparently
// restored from the backup (self-healing). A disk-full error rolls back
// by removing the temp file, keeping the last committed state intact.
type Storage struct {
	Path   string
	Faults *FaultInjector
}

// NewStorage creates a Storage rooted at path. A nil injector disables faults.
func NewStorage(path string, fi *FaultInjector) *Storage {
	if fi == nil {
		fi = NewFaultInjector()
	}
	return &Storage{Path: path, Faults: fi}
}

func (s *Storage) tmpPath() string { return s.Path + ".tmp" }
func (s *Storage) bakPath() string { return s.Path + ".bak" }

// faultWriter wraps a file and enforces the injector's write budget.
type faultWriter struct {
	fi      *FaultInjector
	w       io.Writer
	written int
}

func (fw *faultWriter) Write(p []byte) (int, error) {
	budget, fault := fw.fi.writeBudget(fw.written)
	if budget >= 0 && len(p) > budget {
		n := budget
		if n < 0 {
			n = 0
		}
		if n > 0 {
			if _, err := fw.w.Write(p[:n]); err != nil {
				return 0, err
			}
			fw.written += n
		}
		return n, fault
	}
	n, err := fw.w.Write(p)
	fw.written += n
	return n, err
}

// Save atomically persists b. On ErrDiskFull the temp file is rolled back;
// on ErrPowerLoss the partial temp file is left behind (as after a real
// crash) but never committed. In both cases the previously committed
// image remains valid.
func (s *Storage) Save(b *Bitmap) error {
	data := b.Marshal()
	f, err := os.OpenFile(s.tmpPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	fw := &faultWriter{fi: s.Faults, w: f}
	if _, werr := fw.Write(data); werr != nil {
		f.Close()
		if werr == ErrPowerLoss {
			// Simulated sudden death: partial temp file stays on disk,
			// commit never happens.
			return ErrPowerLoss
		}
		// Disk full (or any other write error): roll back the temp file.
		os.Remove(s.tmpPath())
		return werr
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(s.tmpPath())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(s.tmpPath())
		return err
	}
	// Keep the previous committed image as a backup before committing.
	if _, err := os.Stat(s.Path); err == nil {
		if err := copyFile(s.Path, s.bakPath()); err != nil {
			os.Remove(s.tmpPath())
			return err
		}
	}
	// Atomic commit point.
	if err := os.Rename(s.tmpPath(), s.Path); err != nil {
		return err
	}
	if s.Faults.corruptMainOnCommit() {
		corruptFile(s.Path)
	}
	return nil
}

// Load reads the committed bitmap, self-healing from the backup when the
// main image fails integrity checks. Stray temp files from crashed commits
// are cleaned up. When nothing was ever committed, an empty bitmap is
// returned. ErrCorrupt is returned only when every copy is unrecoverable.
func (s *Storage) Load() (*Bitmap, error) {
	defer os.Remove(s.tmpPath()) // a leftover temp file is never committed

	if data, err := os.ReadFile(s.Path); err == nil {
		if bm, err := Unmarshal(data); err == nil {
			return bm, nil
		}
		// Main image is corrupt: try to self-heal from the backup.
		if bak, err := os.ReadFile(s.bakPath()); err == nil {
			if bm, err := Unmarshal(bak); err == nil {
				_ = os.WriteFile(s.Path, bak, 0o644) // restore main from backup
				return bm, nil
			}
		}
		return nil, ErrCorrupt
	}
	// No main image (e.g. crash before the first commit): fall back to bak.
	if bak, err := os.ReadFile(s.bakPath()); err == nil {
		if bm, err := Unmarshal(bak); err == nil {
			_ = os.WriteFile(s.Path, bak, 0o644)
			return bm, nil
		}
	}
	return New(), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// corruptFile flips bits across the file to emulate torn writes / bit-rot.
func corruptFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	for i := 0; i < len(data); i += 7 {
		data[i] ^= 0xFF
	}
	_ = os.WriteFile(path, data, 0o644)
}
