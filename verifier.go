package netsec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

const readChunkSize = 32 * 1024

// Limits bounds the bytes read from each entry and from the whole package.
// Limits are inclusive: a zero value permits zero-byte files but no content.
type Limits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func (limits Limits) validate() error {
	if limits.MaxFileBytes < 0 || limits.MaxTotalBytes < 0 {
		return fmt.Errorf("%w: limits must be non-negative", ErrInvalidLimits)
	}
	return nil
}

// Entry is one package item. Content must return only that item's bytes and
// io.EOF at its exact end. DeclaredSHA256 is untrusted metadata and is checked
// against both Content and the signed manifest.
type Entry struct {
	Path           string
	DeclaredSHA256 [sha256.Size]byte
	Content        io.Reader
}

// EntrySource yields ordered package entries. Next returns io.EOF after the
// final entry. Content readers may return data in arbitrary-sized chunks.
type EntrySource interface {
	Next() (Entry, error)
}

// InstallFunc receives the complete, verified installation mapping. It is
// invoked at most once, after all entries and the signed manifest agree.
type InstallFunc func(files map[string][]byte) error

// Verifier validates and installs signed packages.
type Verifier struct {
	PublicKey ed25519.PublicKey
	Limits    Limits
}

// NewVerifier creates a verifier with a trusted Ed25519 public key.
func NewVerifier(publicKey ed25519.PublicKey, limits Limits) (*Verifier, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: Ed25519 public key has length %d", ErrInvalidSignature, len(publicKey))
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &Verifier{
		PublicKey: append(ed25519.PublicKey(nil), publicKey...),
		Limits:    limits,
	}, nil
}

// Verify checks the signed manifest and every streamed entry. It returns only
// after the stream has ended and the manifest and entry set match exactly.
func (verifier *Verifier) Verify(ctx context.Context, manifest, signature []byte, source EntrySource) (map[string][]byte, error) {
	if verifier == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidSignature)
	}
	if err := verifier.Limits.validate(); err != nil {
		return nil, err
	}
	if len(verifier.PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: Ed25519 public key has length %d", ErrInvalidSignature, len(verifier.PublicKey))
	}
	if source == nil {
		return nil, fmt.Errorf("%w: nil entry source", ErrStream)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	manifestEntries, err := VerifySignedManifest(verifier.PublicKey, manifest, signature)
	if err != nil {
		return nil, err
	}
	expected := make(map[string][sha256.Size]byte, len(manifestEntries))
	for _, entry := range manifestEntries {
		expected[entry.Path] = entry.SHA256
	}

	files := make(map[string][]byte, len(manifestEntries))
	var totalBytes int64

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		entry, err := source.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStream, err)
		}

		path, err := NormalizePath(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", entry.Path, err)
		}
		if _, exists := files[path]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateEntry, path)
		}
		if _, exists := expected[path]; !exists {
			return nil, fmt.Errorf("%w: %q", ErrExtraEntry, path)
		}

		content, err := readEntry(ctx, entry.Content, path, verifier.Limits, &totalBytes)
		if err != nil {
			return nil, err
		}
		computed := sha256.Sum256(content)
		if computed != entry.DeclaredSHA256 {
			return nil, fmt.Errorf("%w: %q does not match entry declaration", ErrDigestMismatch, path)
		}
		if computed != expected[path] {
			return nil, fmt.Errorf("%w: %q does not match signed manifest", ErrDigestMismatch, path)
		}
		files[path] = content
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for _, entry := range manifestEntries {
		if _, exists := files[entry.Path]; !exists {
			return nil, fmt.Errorf("%w: %q", ErrMissingEntry, entry.Path)
		}
	}
	if len(files) != len(manifestEntries) {
		return nil, fmt.Errorf("%w: verified %d entries, manifest has %d", ErrExtraEntry, len(files), len(manifestEntries))
	}
	return files, nil
}

// VerifyAndInstall verifies the entire package before invoking install. A
// callback error is returned wrapped with ErrInstall.
func (verifier *Verifier) VerifyAndInstall(ctx context.Context, manifest, signature []byte, source EntrySource, install InstallFunc) error {
	if install == nil {
		return fmt.Errorf("%w: nil install callback", ErrInstall)
	}
	files, err := verifier.Verify(ctx, manifest, signature, source)
	if err != nil {
		return err
	}
	if err := install(files); err != nil {
		return fmt.Errorf("%w: %w", ErrInstall, err)
	}
	return nil
}

// Verify validates a package using a caller-provided trusted key and limits.
func Verify(ctx context.Context, publicKey ed25519.PublicKey, manifest, signature []byte, source EntrySource, limits Limits) (map[string][]byte, error) {
	verifier, err := NewVerifier(publicKey, limits)
	if err != nil {
		return nil, err
	}
	return verifier.Verify(ctx, manifest, signature, source)
}

// VerifyAndInstall validates a package and then invokes install once.
func VerifyAndInstall(ctx context.Context, publicKey ed25519.PublicKey, manifest, signature []byte, source EntrySource, limits Limits, install InstallFunc) error {
	verifier, err := NewVerifier(publicKey, limits)
	if err != nil {
		return err
	}
	return verifier.VerifyAndInstall(ctx, manifest, signature, source, install)
}

func readEntry(ctx context.Context, reader io.Reader, path string, limits Limits, totalBytes *int64) ([]byte, error) {
	if reader == nil {
		reader = bytes.NewReader(nil)
	}

	var content bytes.Buffer
	chunk := make([]byte, readChunkSize)
	var fileBytes int64
	noProgress := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		readSize := int64(len(chunk))
		if remaining := limits.MaxFileBytes - fileBytes; remaining < readSize {
			readSize = remaining + 1
		}
		if remaining := limits.MaxTotalBytes - *totalBytes; remaining < readSize {
			readSize = remaining + 1
		}
		if readSize <= 0 {
			readSize = 1
		}

		n, readErr := reader.Read(chunk[:readSize])
		if n < 0 || n > int(readSize) {
			return nil, fmt.Errorf("%w: %q: reader returned invalid byte count %d", ErrStream, path, n)
		}
		if n > 0 {
			noProgress = 0
			if int64(n) > limits.MaxFileBytes-fileBytes {
				return nil, fmt.Errorf("%w: %q", ErrFileTooLarge, path)
			}
			if int64(n) > limits.MaxTotalBytes-*totalBytes {
				return nil, fmt.Errorf("%w: %q", ErrTotalTooLarge, path)
			}
			fileBytes += int64(n)
			*totalBytes += int64(n)
			if _, err := content.Write(chunk[:n]); err != nil {
				return nil, fmt.Errorf("%w: %q: %v", ErrStream, path, err)
			}
		} else if readErr == nil {
			noProgress++
			if noProgress >= 100 {
				return nil, fmt.Errorf("%w: %q: reader made no progress", ErrStream, path)
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("%w: %q: %w", ErrStream, path, readErr)
		}
	}
	return content.Bytes(), nil
}
