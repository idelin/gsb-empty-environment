package npmverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"path"
	"strings"
)

// maxManifestBytes bounds the package.json read so a malicious archive cannot
// force unbounded memory use through a gzip bomb on a single entry.
const maxManifestBytes = 16 << 20

// TarballManifest extracts and decodes package.json from an npm tarball.
// npm packages store it at "package/package.json", while bare tarballs may
// place it at the archive root; both layouts are accepted.
func TarballManifest(tarball []byte) (Manifest, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return Manifest{}, ErrMalformedTarball
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err != nil {
			return Manifest{}, ErrMalformedTarball
		}

		clean := path.Clean(header.Name)
		base := path.Base(clean)
		if base != "package.json" {
			continue
		}

		// Accept "package/package.json" (npm layout) and "package.json"
		// (root layout), but nothing nested more deeply.
		dir := path.Dir(clean)
		if dir != "package" && dir != "." {
			continue
		}

		limited := bytes.NewBuffer(nil)
		if _, err := limited.ReadFrom(io.LimitReader(reader, maxManifestBytes)); err != nil {
			return Manifest{}, ErrMalformedTarball
		}

		var manifest Manifest
		if err := json.Unmarshal(limited.Bytes(), &manifest); err != nil {
			return Manifest{}, ErrMalformedTarball
		}
		if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
			return Manifest{}, ErrMalformedTarball
		}
		return manifest, nil
	}
}
