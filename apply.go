package binpatch

import (
	"crypto/sha256"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

// ApplyFile applies the patch read from patchR to the baseline at
// baselinePath and publishes the result at dstPath.
//
// The result is written to a temporary file in the destination directory and
// only becomes visible at dstPath via an atomic rename after the baseline
// hash, every copy range, the target length, and the target hash have all
// been verified. On any error the temporary file is removed and dstPath is
// left untouched, so a failed apply never publishes a partial result.
func ApplyFile(baselinePath string, patchR io.Reader, dstPath string) error {
	base, err := os.Open(baselinePath)
	if err != nil {
		return err
	}
	defer base.Close()
	st, err := base.Stat()
	if err != nil {
		return err
	}
	return apply(base, st.Size(), patchR, dstPath)
}

// apply runs the patch against baseline (random access, size bytes) and
// atomically publishes the result at dstPath.
func apply(baseline io.ReaderAt, baselineSize int64, patchR io.Reader, dstPath string) error {
	pr, err := NewReader(patchR)
	if err != nil {
		return err
	}

	// Verify the baseline before writing anything.
	sum := sha256.New()
	if _, err := io.Copy(sum, io.NewSectionReader(baseline, 0, baselineSize)); err != nil {
		return err
	}
	var baseHash [32]byte
	copy(baseHash[:], sum.Sum(nil))
	if baseHash != pr.Header.BaselineHash {
		return ErrBaselineHash
	}

	// Stage the output next to the destination so the final rename stays on
	// the same filesystem and is atomic.
	dir := filepath.Dir(dstPath)
	tmp, err := ioutil.TempFile(dir, "."+filepath.Base(dstPath)+".tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// From here on, any failure must not leave the staged file behind.
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpName)
		}
	}()

	out := &SumWriter{W: tmp, H: sha256.New()}
	for {
		op, payload, err := pr.NextOp()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if op.Insert {
			if _, err := io.Copy(out, payload); err != nil {
				return err
			}
			continue
		}
		// Copies read only from the original baseline, never from the
		// output produced so far.
		if op.Offset > uint64(baselineSize) || op.Length > uint64(baselineSize)-op.Offset {
			return ErrOutOfBounds
		}
		src := io.NewSectionReader(baseline, int64(op.Offset), int64(op.Length))
		if _, err := io.Copy(out, src); err != nil {
			return err
		}
	}

	if out.N != pr.Header.TargetLength {
		return ErrTargetLength
	}
	var targetHash [32]byte
	copy(targetHash[:], out.H.Sum(nil))
	if targetHash != pr.Header.TargetHash {
		return ErrTargetHash
	}

	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dstPath); err != nil {
		return err
	}
	ok = true
	return nil
}
