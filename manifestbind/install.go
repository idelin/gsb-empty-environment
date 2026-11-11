package manifestbind

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
)

// Entry is one decompressed archive member. Open must return a fresh
// reader over the member's decompressed bytes; the installer computes
// the digest over exactly those bytes as they are written to disk.
type Entry struct {
	Path string
	Open func() (io.ReadCloser, error)
}

// BytesEntry builds an Entry from an in-memory byte slice.
func BytesEntry(path string, data []byte) Entry {
	return Entry{
		Path: path,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		},
	}
}

// Install extracts entries into destDir on fsys, but only publishes the
// directory once every entry has been verified against the signed
// manifest. Entries are staged in a sibling directory and moved into
// place atomically; on any failure the staging directory is removed and
// destDir is left untouched.
//
// The digest of each entry is computed over the decompressed bytes
// actually written, never over the raw archive encoding.
func Install(
	fsys FS,
	destDir string,
	entries iter.Seq2[Entry, error],
	manifest *Manifest,
	sig []byte,
	key ed25519.PublicKey,
) (err error) {
	if err := manifest.Verify(key, sig); err != nil {
		return err
	}
	if err := ValidatePath(destDir); err != nil {
		return fmt.Errorf("manifestbind: invalid destination: %w", err)
	}
	if _, statErr := fsys.Stat(destDir); statErr == nil {
		return fmt.Errorf("manifestbind: destination %q already exists", destDir)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}

	expected := make(map[string]ManifestEntry, len(manifest.Entries))
	for _, e := range manifest.Entries {
		expected[e.Path] = e
	}

	staging := destDir + ".staging"
	if err := fsys.RemoveAll(staging); err != nil {
		return err
	}
	if err := fsys.MkdirAll(staging); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Roll back: nothing is published until every entry verifies.
			_ = fsys.RemoveAll(staging)
		}
	}()

	seen := newPathSet()
	for entry, iterErr := range entries {
		if iterErr != nil {
			return iterErr
		}
		if err = installOne(fsys, staging, entry, expected, seen); err != nil {
			return err
		}
	}

	for _, e := range manifest.Entries {
		if !seen.Has(e.Path) {
			return fmt.Errorf("%w: %q", ErrMissingEntry, e.Path)
		}
	}

	if err = fsys.Rename(staging, destDir); err != nil {
		return err
	}
	return nil
}

func installOne(fsys FS, staging string, entry Entry, expected map[string]ManifestEntry, seen *pathSet) error {
	if err := seen.Add(entry.Path); err != nil {
		return err
	}
	want, ok := expected[entry.Path]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnlistedEntry, entry.Path)
	}

	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	dst := staging + "/" + entry.Path
	if err := mkdirParent(fsys, dst); err != nil {
		return err
	}
	w, err := fsys.Create(dst)
	if err != nil {
		return err
	}

	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(w, hash), rc)
	closeErr := w.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}

	if size != want.Size {
		return fmt.Errorf("%w: %q: size %d, manifest says %d",
			ErrDigestMismatch, entry.Path, size, want.Size)
	}
	var got [sha256.Size]byte
	copy(got[:], hash.Sum(nil))
	if got != want.SHA256 {
		return fmt.Errorf("%w: %q", ErrDigestMismatch, entry.Path)
	}
	return nil
}

func mkdirParent(fsys FS, name string) error {
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			if err := fsys.MkdirAll(name[:i]); err != nil {
				return err
			}
		}
	}
	return nil
}
