package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrObjectSizeMismatch marks a response whose byte count differs from the
	// size in the validated pointer.
	ErrObjectSizeMismatch = errors.New("LFS object size mismatch")
	// ErrObjectDigestMismatch marks a response whose SHA-256 differs from the
	// digest in the validated pointer.
	ErrObjectDigestMismatch = errors.New("LFS object sha256 mismatch")
)

// PublishObject streams src into a temporary file in dest's directory while
// computing its SHA-256. It atomically renames the temporary file to dest only
// after both the expected length and digest match. On any failure, dest is not
// modified and the temporary file is removed.
func PublishObject(ctx context.Context, src io.Reader, dest string, expected Pointer) error {
	if src == nil {
		return fmt.Errorf("publish LFS object: nil response body")
	}
	if dest == "" {
		return fmt.Errorf("publish LFS object: empty destination")
	}
	if expected.Size < 0 {
		return fmt.Errorf("%w: negative size", ErrInvalidPointer)
	}
	if !validOID(expected.OID) {
		return fmt.Errorf("%w: malformed sha256 oid %q", ErrInvalidPointer, expected.OID)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	wantDigest := strings.TrimPrefix(expected.OID, sha256Prefix)
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".lfs-download-*")
	if err != nil {
		return fmt.Errorf("create LFS temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	buf := make([]byte, 64*1024)
	var written int64
	var emptyReads int

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			emptyReads = 0
			if int64(n) > expected.Size-written {
				return fmt.Errorf("%w: response exceeds expected size %d", ErrObjectSizeMismatch, expected.Size)
			}
			if _, err := tmp.Write(buf[:n]); err != nil {
				return fmt.Errorf("write LFS temporary file: %w", err)
			}
			if _, err := hasher.Write(buf[:n]); err != nil {
				return fmt.Errorf("hash LFS object: %w", err)
			}
			written += int64(n)
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return fmt.Errorf("read LFS object: %w", io.ErrNoProgress)
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read LFS object: %w", readErr)
		}
	}

	if written != expected.Size {
		return fmt.Errorf("%w: got %d bytes, expected %d", ErrObjectSizeMismatch, written, expected.Size)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actualDigest := hex.EncodeToString(hasher.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actualDigest), []byte(wantDigest)) != 1 {
		return fmt.Errorf("%w: got %s, expected %s", ErrObjectDigestMismatch, actualDigest, wantDigest)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync LFS temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close LFS temporary file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("publish LFS object: %w", err)
	}
	published = true
	return nil
}
