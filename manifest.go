package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	manifestPrefix    = "SHA256 ("
	manifestSeparator = ") = "
	sha256HexSize     = sha256.Size * 2
)

// Manifest is an authenticated, name-bound set of SHA256 checksums.
type Manifest struct {
	digests map[string][sha256.Size]byte
}

// Digest returns the expected digest for name. The bool reports whether the
// name is present.
func (manifest *Manifest) Digest(name string) ([sha256.Size]byte, bool) {
	var zero [sha256.Size]byte
	if manifest == nil {
		return zero, false
	}
	digest, ok := manifest.digests[name]
	return digest, ok
}

// Entry is an alias for Digest.
func (manifest *Manifest) Entry(name string) ([sha256.Size]byte, bool) {
	return manifest.Digest(name)
}

// Len returns the number of distinct names in the manifest.
func (manifest *Manifest) Len() int {
	if manifest == nil {
		return 0
	}
	return len(manifest.digests)
}

// Names returns the listed names in no particular order.
func (manifest *Manifest) Names() []string {
	if manifest == nil {
		return nil
	}
	names := make([]string, 0, len(manifest.digests))
	for name := range manifest.digests {
		names = append(names, name)
	}
	return names
}

// Entries returns a copy of all name-to-digest entries.
func (manifest *Manifest) Entries() map[string][sha256.Size]byte {
	if manifest == nil {
		return nil
	}
	entries := make(map[string][sha256.Size]byte, len(manifest.digests))
	for name, digest := range manifest.digests {
		entries[name] = digest
	}
	return entries
}

// Allows reports whether name is a safely formed, listed download name.
func (manifest *Manifest) Allows(name string) bool {
	_, ok := manifest.Digest(name)
	return ok && validateFileName(name) == nil
}

// VerifyFile checks downloaded content against the digest listed for name.
// The name is used only as an exact lookup key and must be a safe basename.
func (manifest *Manifest) VerifyFile(name string, content []byte) error {
	if err := validateFileName(name); err != nil {
		return err
	}
	expected, ok := manifest.Digest(name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrFileNotListed, name)
	}

	actual := sha256.Sum256(content)
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
		return fmt.Errorf("%w: %q", ErrChecksumMismatch, name)
	}
	return nil
}

// VerifyReader checks a downloaded file from a stream against the digest
// listed for name.
func (manifest *Manifest) VerifyReader(name string, reader io.Reader) error {
	if err := validateFileName(name); err != nil {
		return err
	}
	expected, ok := manifest.Digest(name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrFileNotListed, name)
	}
	if reader == nil {
		return fmt.Errorf("%w: %q: nil reader", ErrChecksumMismatch, name)
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return fmt.Errorf("read download %q: %w", name, err)
	}
	actual := hasher.Sum(nil)
	if subtle.ConstantTimeCompare(actual, expected[:]) != 1 {
		return fmt.Errorf("%w: %q", ErrChecksumMismatch, name)
	}
	return nil
}

// VerifyDownload is an alias for VerifyFile for use from a download
// completion callback.
func (manifest *Manifest) VerifyDownload(name string, content []byte) error {
	return manifest.VerifyFile(name, content)
}

// VerifyDownloadReader checks a download from a stream. The caller supplies
// the logical name; the filename of a local path is not substituted for it.
func (manifest *Manifest) VerifyDownloadReader(name string, reader io.Reader) error {
	return manifest.VerifyReader(name, reader)
}

// VerifyDownloadFile checks a completed download. The path is used only to
// read bytes; lookup and checksum matching use name exactly, so a local path
// cannot make an unlisted file acceptable.
func (manifest *Manifest) VerifyDownloadFile(name, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return manifest.VerifyReader(name, file)
}

func parseManifest(body []byte) (*Manifest, error) {
	entries := make(map[string][sha256.Size]byte)
	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte(manifestPrefix)) {
			return nil, fmt.Errorf("%w: invalid line %q", ErrMalformedManifest, rawLine)
		}

		rest := string(line[len(manifestPrefix):])
		separator := strings.LastIndex(rest, manifestSeparator)
		if separator < 0 {
			return nil, fmt.Errorf("%w: invalid line %q", ErrMalformedManifest, rawLine)
		}
		name := rest[:separator]
		encodedDigest := rest[separator+len(manifestSeparator):]
		if err := validateFileName(name); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedManifest, err)
		}
		if len(encodedDigest) != sha256HexSize {
			return nil, fmt.Errorf("%w: digest for %q has %d characters, want %d", ErrMalformedManifest, name, len(encodedDigest), sha256HexSize)
		}
		decoded, err := hex.DecodeString(encodedDigest)
		if err != nil {
			return nil, fmt.Errorf("%w: digest for %q is not hexadecimal: %v", ErrMalformedManifest, name, err)
		}

		var digest [sha256.Size]byte
		copy(digest[:], decoded)
		if previous, exists := entries[name]; exists && previous != digest {
			return nil, fmt.Errorf("%w: %w: %q", ErrMalformedManifest, ErrConflictingEntry, name)
		}
		entries[name] = digest
	}

	return &Manifest{digests: entries}, nil
}

func validateFileName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrUnsafeFileName)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: %q", ErrUnsafeFileName, name)
	}
	if strings.ContainsAny(name, "/\\") || strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("%w: %q", ErrUnsafeFileName, name)
	}
	return nil
}
