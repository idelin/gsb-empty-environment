package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
)

var (
	ErrTooManyEntries = errors.New("netsec: too many entries")
	ErrFileTooLarge   = errors.New("netsec: file exceeds the per-file byte limit")
	ErrTotalTooLarge  = errors.New("netsec: package exceeds the total byte limit")
	ErrDuplicatePath  = errors.New("netsec: duplicate canonical path in entries")
	ErrDigestMismatch = errors.New("netsec: entry digest mismatch")
	ErrExtraEntry     = errors.New("netsec: entry not present in the manifest")
	ErrMissingEntry   = errors.New("netsec: manifest entry missing from the package")
)

// Limits bounds resource consumption while reading a package. A value <= 0
// means the corresponding limit is not enforced.
type Limits struct {
	// MaxFileBytes bounds the size of any single file.
	MaxFileBytes int64
	// MaxTotalBytes bounds the sum of all file sizes.
	MaxTotalBytes int64
	// MaxEntries bounds the number of entries in the package.
	MaxEntries int
}

// Entry is one package entry: a raw (unnormalized) UTF-8 path, the declared
// SHA-256 of the file bytes, and the file content stream. Content is read in
// chunks and may be any io.Reader, including one that yields truncated data.
type Entry struct {
	Path           string
	DeclaredSHA256 [sha256.Size]byte
	Content        io.Reader
}

// InstallFunc receives one verified file. It is only ever invoked after the
// entire package has been verified; a failed package triggers no calls.
type InstallFunc func(path string, content io.Reader) error

const copyBufSize = 32 * 1024

// VerifyAndInstall verifies a signed package and installs it.
//
// Phase 1 reads every entry in chunks, canonicalizes its path, enforces the
// caller-provided limits, hashes the content, and rejects duplicate canonical
// paths, digest mismatches, and read failures. It then verifies the manifest
// signature and requires the set of canonical paths and digests to match the
// manifest exactly: extra or missing entries are rejected.
//
// Phase 2 runs only after every check has passed: install is invoked once per
// file, in canonical path byte order. Any conflict, extra or missing entry,
// or bad signature is therefore rejected before any install callback.
func VerifyAndInstall(entries []Entry, manifest, signature []byte, key ed25519.PublicKey, limits Limits, install InstallFunc) error {
	if limits.MaxEntries > 0 && len(entries) > limits.MaxEntries {
		return fmt.Errorf("%w: got %d, limit %d", ErrTooManyEntries, len(entries), limits.MaxEntries)
	}
	if err := verifyManifestSignature(key, manifest, signature); err != nil {
		return err
	}
	manifestEntries, err := parseManifest(manifest)
	if err != nil {
		return err
	}

	type verifiedFile struct {
		data []byte
	}
	byPath := make(map[string]verifiedFile, len(entries))
	var total int64
	buf := make([]byte, copyBufSize)

	for _, e := range entries {
		canon, err := canonicalizePath(e.Path)
		if err != nil {
			return fmt.Errorf("entry %q: %w", e.Path, err)
		}
		if _, dup := byPath[canon]; dup {
			return fmt.Errorf("%w: %s", ErrDuplicatePath, canon)
		}

		h := sha256.New()
		var body bytes.Buffer
		allowance := limits.MaxFileBytes
		if allowance <= 0 {
			allowance = math.MaxInt64
		}
		if limits.MaxTotalBytes > 0 {
			if remaining := limits.MaxTotalBytes - total; remaining < allowance {
				allowance = remaining
			}
		}
		// Read in chunks, at most allowance+1 bytes, so oversized files are
		// detected without unbounded reads.
		readCap := allowance
		if readCap < math.MaxInt64 {
			readCap++
		}
		n, err := io.CopyBuffer(io.MultiWriter(h, &body), io.LimitReader(e.Content, readCap), buf)
		if err != nil {
			return fmt.Errorf("netsec: reading entry %q: %w", canon, err)
		}
		if limits.MaxFileBytes > 0 && n > limits.MaxFileBytes {
			return fmt.Errorf("%w: %s is %d bytes, limit %d", ErrFileTooLarge, canon, n, limits.MaxFileBytes)
		}
		total += n
		if limits.MaxTotalBytes > 0 && total > limits.MaxTotalBytes {
			return fmt.Errorf("%w: limit %d", ErrTotalTooLarge, limits.MaxTotalBytes)
		}

		var got [sha256.Size]byte
		copy(got[:], h.Sum(nil))
		if got != e.DeclaredSHA256 {
			return fmt.Errorf("%w: %s content does not match its declared SHA-256", ErrDigestMismatch, canon)
		}
		byPath[canon] = verifiedFile{data: body.Bytes()}
	}

	if len(byPath) != len(manifestEntries) {
		return fmt.Errorf("%w: package has %d files, manifest lists %d", ErrExtraEntry, len(byPath), len(manifestEntries))
	}
	for _, m := range manifestEntries {
		v, ok := byPath[m.path]
		if !ok {
			return fmt.Errorf("%w: %s", ErrMissingEntry, m.path)
		}
		if sha256.Sum256(v.data) != m.digest {
			return fmt.Errorf("%w: %s content does not match the manifest", ErrDigestMismatch, m.path)
		}
	}

	// All checks passed: invoke install callbacks in canonical byte order.
	for _, m := range manifestEntries {
		if err := install(m.path, bytes.NewReader(byPath[m.path].data)); err != nil {
			return fmt.Errorf("netsec: install %q: %w", m.path, err)
		}
	}
	return nil
}
