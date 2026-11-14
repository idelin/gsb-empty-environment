package depverify

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// Hash1 computes the "h1:" module hash over an already rendered, newline
// separated "name  hash" list. It is equivalent to dirhash.Hash1 from
// golang.org/x/mod/sumdb/dirhash, so produced values match go.sum entries.
func Hash1(files []string, open func(string) (io.ReadCloser, error)) (string, error) {
	h := sha256.New()
	for _, file := range files {
		if strings.Contains(file, "\n") {
			return "", fmt.Errorf("depverify: filenames with newlines are not supported")
		}
		r, err := open(file)
		if err != nil {
			return "", err
		}
		hf := sha256.New()
		if _, err := io.Copy(hf, r); err != nil {
			r.Close()
			return "", err
		}
		r.Close()
		fmt.Fprintf(h, "%s  %x\n", file, hf.Sum(nil))
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// HashDir hashes an on-disk module directory. prefix is stripped from each
// file name, and module is the module path used together with version to form
// the canonical "<module>@<version>/" prefix expected inside a module zip.
func HashDir(dir, module, version string) (string, error) {
	prefix := module + "@" + version + "/"
	files, err := dirFiles(dir, prefix)
	if err != nil {
		return "", err
	}
	return Hash1(files, func(name string) (io.ReadCloser, error) {
		rel := strings.TrimPrefix(name, prefix)
		return os.Open(path.Join(dir, rel))
	})
}

func dirFiles(dir, prefix string) ([]string, error) {
	var files []string
	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		cleaned := path.Clean(p)
		if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
			return fmt.Errorf("depverify: invalid local module file %q", p)
		}
		files = append(files, prefix+cleaned)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// HashZip hashes a module zip archive. The zip must contain files under the
// canonical "<module>@<version>/" prefix, exactly like a module proxy download.
func HashZip(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("depverify: reading module zip: %w", err)
	}
	var names []string
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Clean(f.Name)
		if !strings.HasPrefix(name, "../") && name != ".." {
			names = append(names, name)
			byName[name] = f
		}
	}
	sort.Strings(names)
	return Hash1(names, func(name string) (io.ReadCloser, error) {
		f, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("depverify: missing zip entry %q", name)
		}
		return f.Open()
	})
}

// HashBytes hashes raw bytes as a single in-memory file, used for go.mod
// contents whose "<path> <version>/go.mod" checksum is present in go.sum.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s  %x", "go.mod", sum[:])
	outer := sha256.Sum256(buf.Bytes())
	return "h1:" + base64.StdEncoding.EncodeToString(outer[:])
}
