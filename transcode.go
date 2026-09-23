package blkconv

import (
	"crypto/sha256"
	"io"
	"os"
)

// pipelineDepth bounds how many decoded blocks may be in flight between
// the decode and encode stages. Together with BlockSize this caps
// transcoding memory at roughly (pipelineDepth+2)*BlockSize, providing
// backpressure: a slow or failing writer stops the reader.
const pipelineDepth = 2

// Transcode streams src (any supported block encoding) into dst in
// dstFmt, without materializing the plaintext. The source header's
// metadata, declared length and digest are carried into the new stream
// and re-verified against the actual decoded bytes; any corruption,
// truncation or length/digest lie aborts the transcode before the commit
// marker is written. maxBytes bounds decoded output (0 = default).
func Transcode(src io.Reader, dstFmt Format, dst io.Writer, maxBytes uint64) error {
	dec, err := NewDecoder(src, maxBytes)
	if err != nil {
		return err
	}
	h := dec.Header()
	enc, err := NewEncoder(dst, dstFmt, h.Meta, h.OrigLen, h.Digest)
	if err != nil {
		return err
	}

	chunks := make(chan []byte, pipelineDepth)
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(chunks)
		buf := make([]byte, BlockSize)
		for {
			n, rerr := dec.Read(buf)
			if n > 0 {
				select {
				case chunks <- buf[:n]:
					buf = make([]byte, BlockSize)
				case <-done:
					return
				}
			}
			if rerr == io.EOF {
				return
			}
			if rerr != nil {
				errs <- rerr
				return
			}
		}
	}()

	var werr error
	for c := range chunks {
		if werr != nil {
			continue
		}
		if _, err := enc.Write(c); err != nil {
			werr = err
			close(done)
		}
	}
	if werr != nil {
		return werr
	}
	select {
	case err := <-errs:
		return err
	default:
	}
	// Close verifies declared length + digest and writes the commit
	// marker only if the whole stream checked out.
	return enc.Close()
}

// CommitTarget is a transactional sink: bytes written to W stay
// uncommitted until Commit is called; Abort discards them.
type CommitTarget struct {
	W      io.Writer
	Commit func() error
	Abort  func()
}

// NewFileTarget stages output in finalPath+".part" and atomically renames
// it into place on Commit. Abort removes the uncommitted tail.
func NewFileTarget(finalPath string) (*CommitTarget, error) {
	tmp := finalPath + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &CommitTarget{
		W: f,
		Commit: func() error {
			if err := f.Sync(); err != nil {
				f.Close()
				os.Remove(tmp)
				return err
			}
			if err := f.Close(); err != nil {
				os.Remove(tmp)
				return err
			}
			return os.Rename(tmp, finalPath)
		},
		Abort: func() {
			f.Close()
			os.Remove(tmp)
		},
	}, nil
}

// TranscodeToTarget transcodes into a CommitTarget: on any failure the
// uncommitted tail is aborted; on success it is committed.
func TranscodeToTarget(src io.Reader, dstFmt Format, t *CommitTarget, maxBytes uint64) error {
	if err := Transcode(src, dstFmt, t.W, maxBytes); err != nil {
		t.Abort()
		return err
	}
	return t.Commit()
}

// TranscodeFile transcodes srcPath into dstPath in dstFmt. The
// destination appears only on full success.
func TranscodeFile(srcPath, dstPath string, dstFmt Format, maxBytes uint64) (*Header, error) {
	h, err := ReadHeaderFile(srcPath)
	if err != nil {
		return nil, err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	t, err := NewFileTarget(dstPath)
	if err != nil {
		return nil, err
	}
	if err := TranscodeToTarget(src, dstFmt, t, maxBytes); err != nil {
		return nil, err
	}
	return h, nil
}

// PackFile encodes srcPath into dstPath in the given format. The source
// is streamed twice (hash pass, encode pass); memory stays bounded.
func PackFile(srcPath, dstPath string, f Format, meta map[string]string) (*Header, error) {
	digest, n, err := HashFile(srcPath)
	if err != nil {
		return nil, err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	t, err := NewFileTarget(dstPath)
	if err != nil {
		return nil, err
	}
	enc, err := NewEncoder(t.W, f, meta, n, digest)
	if err != nil {
		t.Abort()
		return nil, err
	}
	if _, err := io.CopyBuffer(enc, src, make([]byte, BlockSize)); err != nil {
		t.Abort()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		t.Abort()
		return nil, err
	}
	if err := t.Commit(); err != nil {
		return nil, err
	}
	h := &Header{Format: f, Meta: meta, OrigLen: n, Digest: digest}
	return h, nil
}

// UnpackFile decodes srcPath into dstPath, enforcing the decode budget
// and verifying the commit marker, length and digest.
func UnpackFile(srcPath, dstPath string, maxBytes uint64) (*Header, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	dec, err := NewDecoder(src, maxBytes)
	if err != nil {
		return nil, err
	}
	t, err := NewFileTarget(dstPath)
	if err != nil {
		return nil, err
	}
	if _, err := io.CopyBuffer(t.W, dec, make([]byte, BlockSize)); err != nil {
		t.Abort()
		return nil, err
	}
	if err := t.Commit(); err != nil {
		return nil, err
	}
	h := dec.Header()
	return &h, nil
}

// HashFile returns the SHA-256 digest and length of a file.
func HashFile(path string) ([32]byte, uint64, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return sum, 0, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, uint64(n), nil
}

// ReadHeaderFile reads just the header of an encoded file.
func ReadHeaderFile(path string) (*Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec, err := NewDecoder(f, 0)
	if err != nil {
		return nil, err
	}
	h := dec.Header()
	return &h, nil
}
