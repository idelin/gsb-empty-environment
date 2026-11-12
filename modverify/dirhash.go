// Package modverify verifies Go module zips against independently recorded
// hashes, protecting a build proxy from a compromised module proxy that
// serves source archives differing from the recorded hashes.
package modverify

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// hash1 implements the dirhash h1 algorithm: files are sorted by name, then
// for each file a line "<sha256-hex><space><space><name>\n" is fed into a
// SHA-256 hasher, and the result is returned as "h1:" plus base64.
func hash1(files []string, open func(string) (io.ReadCloser, error)) (string, error) {
	h := sha256.New()
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, file := range sorted {
		if strings.ContainsAny(file, "\n\r") {
			return "", fmt.Errorf("modverify: file name %q contains a newline", file)
		}
		r, err := open(file)
		if err != nil {
			return "", err
		}
		hf := sha256.New()
		_, copyErr := io.Copy(hf, r)
		closeErr := r.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		fmt.Fprintf(h, "%x  %s\n", hf.Sum(nil), file)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// HashZip computes the h1 digest of a module zip. Every entry name must
// carry the "<module>@<version>/" prefix and must not contain a newline;
// otherwise an error is returned and no hash is produced.
func HashZip(zipPath, module, version string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	prefix := module + "@" + version + "/"
	files := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		if strings.ContainsAny(f.Name, "\n\r") {
			return "", fmt.Errorf("modverify: zip entry %q contains a newline", f.Name)
		}
		if !strings.HasPrefix(f.Name, prefix) {
			return "", fmt.Errorf("modverify: zip entry %q lacks required prefix %q", f.Name, prefix)
		}
		files = append(files, f.Name)
	}

	return hash1(files, func(name string) (io.ReadCloser, error) {
		for _, f := range zr.File {
			if f.Name == name {
				return f.Open()
			}
		}
		return nil, fmt.Errorf("modverify: zip entry %q vanished", name)
	})
}

// HashMod computes the h1 digest of a go.mod file, hashed under the fixed
// synthetic name "go.mod" as go.sum records expect.
func HashMod(modPath string) (string, error) {
	if _, err := os.Stat(modPath); err != nil {
		return "", err
	}
	return hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return os.Open(modPath)
	})
}
