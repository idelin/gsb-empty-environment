package netsec

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

// SHA256Checksum returns the raw-content SHA-256 checksum in
// "sha256:<lowercase hex>" form.
func SHA256Checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// H1ChecksumZip computes the Go h1 checksum for a module zip. The algorithm
// follows the Go module dirhash convention: SHA-256 over sorted
// "<file-content-sha256-hex>  <zip-entry-name>\n" records, base64 encoded.
func H1ChecksumZip(content []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("%w: module content is not a zip: %v", ErrChecksumUnsupported, err)
	}

	files := make(map[string]*zip.File, len(reader.File))
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if strings.Contains(file.Name, "\n") {
			return "", fmt.Errorf("%w: zip entry name contains a newline", ErrInvalidArtifact)
		}
		if _, exists := files[file.Name]; exists {
			return "", fmt.Errorf("%w: duplicate zip entry %q", ErrInvalidArtifact, file.Name)
		}
		files[file.Name] = file
		names = append(names, file.Name)
	}
	sort.Strings(names)

	aggregate := sha256.New()
	for _, name := range names {
		entry, err := files[name].Open()
		if err != nil {
			return "", fmt.Errorf("%w: open zip entry %q: %v", ErrInvalidArtifact, name, err)
		}
		fileHash := sha256.New()
		_, copyErr := io.Copy(fileHash, entry)
		closeErr := entry.Close()
		if copyErr != nil {
			return "", fmt.Errorf("%w: read zip entry %q: %v", ErrInvalidArtifact, name, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("%w: close zip entry %q: %v", ErrInvalidArtifact, name, closeErr)
		}
		fmt.Fprintf(aggregate, "%x  %s\n", fileHash.Sum(nil), name)
	}

	return "h1:" + base64.StdEncoding.EncodeToString(aggregate.Sum(nil)), nil
}

// VerifyContentChecksum recomputes content and compares it with expected.
// Supported formats are "h1:<base64>" (Go module zip hash) and
// "sha256:<hex>" (raw content hash).
func VerifyContentChecksum(content []byte, expected string) error {
	algorithm, err := checksumAlgorithm(expected)
	if err != nil {
		return err
	}
	var actual string
	switch algorithm {
	case "h1":
		actual, err = H1ChecksumZip(content)
		if err != nil {
			return err
		}
	case "sha256":
		actual = SHA256Checksum(content)
	default:
		return fmt.Errorf("%w: %q", ErrChecksumUnsupported, algorithm)
	}
	if actual != expected {
		return fmt.Errorf("%w: expected %s, content produced %s", ErrChecksumMismatch, expected, actual)
	}
	return nil
}

func checksumAlgorithm(checksum string) (string, error) {
	switch {
	case strings.HasPrefix(checksum, "h1:") && len(checksum) > len("h1:"):
		return "h1", nil
	case strings.HasPrefix(checksum, "sha256:") && len(checksum) > len("sha256:"):
		return "sha256", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrChecksumUnsupported, checksum)
	}
}
