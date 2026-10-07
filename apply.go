package binpatch

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

// ErrBaselineHash is returned when the baseline does not match the patch.
var ErrBaselineHash = errors.New("binpatch: baseline hash mismatch")

// ErrChecksum is returned when the reconstructed target fails verification.
var ErrChecksum = errors.New("binpatch: target hash mismatch")

// ErrLength is returned when the reconstructed target has the wrong size.
var ErrLength = errors.New("binpatch: target length mismatch")

// ErrOutOfBounds is returned when a copy op reads outside the baseline.
var ErrOutOfBounds = errors.New("binpatch: copy op out of baseline bounds")

// OpError describes a failure while streaming the op at Index.
type OpError struct {
	Index int
	Kind  OpKind
	Err   error
}

func (e *OpError) Error() string {
	return fmt.Sprintf("binpatch: op %d (kind %d): %v", e.Index, e.Kind, e.Err)
}

// ApplyFile decodes a patch from patchR and applies it to the baseline at
// baselinePath, publishing the reconstructed target at targetPath.
//
// The target is written to a temporary file in the same directory and only
// renamed into place after its length and SHA-256 match the patch header,
// so a failed apply never leaves a partial file at targetPath.
func ApplyFile(baselinePath, targetPath string, patchR io.Reader) error {
	baseline, err := os.Open(baselinePath)
	if err != nil {
		return err
	}
	defer baseline.Close()
	return Apply(baseline, targetPath, patchR)
}

// Apply decodes a patch from patchR and applies it to baseline, publishing
// the reconstructed target at targetPath atomically. baseline is the only
// source for copy operations; the partially written output is never read.
func Apply(baseline io.Reader, targetPath string, patchR io.Reader) error {
	patch, err := decodePatch(patchR)
	if err != nil {
		return err
	}
	return ApplyPatch(baseline, targetPath, patch)
}

// ApplyPatch applies an already-decoded patch, publishing the result at
// targetPath atomically (write to temp file, verify, rename).
func ApplyPatch(baseline io.Reader, targetPath string, patch *Patch) error {
	src, err := hashVerifiedSource(baseline, patch.BaselineHash)
	if err != nil {
		return err
	}

	dir := filepath.Dir(targetPath)
	tmp, err := ioutil.TempFile(dir, ".binpatch-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Any exit before the rename must not publish a partial target.
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if err := streamTarget(tmp, src, patch); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, targetPath)
}

// verifiedSource is a baseline snapshot whose hash has been checked against
// the patch. Copy ops read from it via ReadAt, never from the output.
type verifiedSource struct {
	r    io.ReaderAt
	size int64
}

// hashVerifiedSource buffers the baseline while hashing it, and returns a
// random-access view only if the hash matches want.
func hashVerifiedSource(baseline io.Reader, want [sha256.Size]byte) (*verifiedSource, error) {
	h := sha256.New()
	buf, err := ioutil.ReadAll(io.TeeReader(baseline, h))
	if err != nil {
		return nil, err
	}
	var got [sha256.Size]byte
	h.Sum(got[:0])
	if got != want {
		return nil, ErrBaselineHash
	}
	return &verifiedSource{r: bytes.NewReader(buf), size: int64(len(buf))}, nil
}

// streamTarget writes the reconstructed target to out, verifying length and
// hash against the patch header.
func streamTarget(out io.Writer, src *verifiedSource, patch *Patch) error {
	h := sha256.New()
	w := io.MultiWriter(out, h)
	var written uint64
	for i, op := range patch.Ops {
		var n uint64
		var err error
		switch op.Kind {
		case OpCopy:
			n, err = streamCopy(w, src, op)
		case OpInsert:
			n, err = streamInsert(w, op)
		default:
			err = fmt.Errorf("unknown op kind %d", op.Kind)
		}
		if err != nil {
			return &OpError{Index: i, Kind: op.Kind, Err: err}
		}
		written += n
		if written > patch.TargetLength {
			return ErrLength
		}
	}
	if written != patch.TargetLength {
		return ErrLength
	}
	var got [sha256.Size]byte
	h.Sum(got[:0])
	if got != patch.TargetHash {
		return ErrChecksum
	}
	return nil
}

// streamCopy copies op.Length baseline bytes starting at op.Offset. It reads
// exclusively from the verified baseline snapshot, never from the output.
func streamCopy(w io.Writer, src *verifiedSource, op Op) (uint64, error) {
	if op.Offset > uint64(src.size) || op.Length > uint64(src.size)-op.Offset {
		return 0, ErrOutOfBounds
	}
	reader := io.NewSectionReader(src.r, int64(op.Offset), int64(op.Length))
	if _, err := io.Copy(w, reader); err != nil {
		return 0, err
	}
	return op.Length, nil
}

func streamInsert(w io.Writer, op Op) (uint64, error) {
	if uint64(len(op.Data)) != op.Length {
		return 0, ErrFormat
	}
	if _, err := w.Write(op.Data); err != nil {
		return 0, err
	}
	return op.Length, nil
}
