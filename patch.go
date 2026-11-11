package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// Op identifies the instruction opcode of a patch script.
type Op uint8

const (
	// OpCopy appends Len bytes read from the original read-only image at
	// offset Off. Source ranges always refer to the original image, never
	// to output produced by the current run.
	OpCopy Op = iota
	// OpInsert appends Data verbatim.
	OpInsert
	// OpZero appends Len zero bytes.
	OpZero
)

// Instruction is a single step of a patch script. Output is produced by
// executing instructions in order and appending their results.
type Instruction struct {
	Op   Op
	Off  uint64 // source offset in the original image (OpCopy only)
	Len  uint64 // byte count (OpCopy, OpZero)
	Data []byte // literal payload (OpInsert)
}

// Manifest binds the expected output length and final SHA-256 digest of a
// patch run. The host MUST verify the manifest signature before calling
// Apply; Apply enforces that the produced output matches the manifest.
type Manifest struct {
	OutputLen uint64
	SHA256    [32]byte
}

// Limits caps the resources an untrusted script may consume. Validation of
// every range and of the total budget completes before any output is
// written.
type Limits struct {
	MaxInstructions int    // maximum number of instructions per script
	MaxOutputLen    uint64 // maximum total output bytes
	MaxInsertBytes  uint64 // maximum cumulative OpInsert payload bytes
}

var (
	ErrUnknownOp      = errors.New("netsec: unknown opcode")
	ErrRange          = errors.New("netsec: copy range outside original image")
	ErrBudget         = errors.New("netsec: script exceeds budget")
	ErrOutputLen      = errors.New("netsec: output length does not match manifest")
	ErrDigestMismatch = errors.New("netsec: output digest does not match manifest")
	ErrImageTooLarge  = errors.New("netsec: original image too large to address")
)

// Validate checks every instruction range against the original image length
// and the total budget, and confirms the script's implied output length
// matches the manifest. It performs no I/O and writes nothing.
func Validate(script []Instruction, oldLen uint64, m Manifest, lim Limits) error {
	if oldLen > math.MaxInt64 {
		return ErrImageTooLarge
	}
	if len(script) > lim.MaxInstructions {
		return fmt.Errorf("%w: %d instructions > limit %d", ErrBudget, len(script), lim.MaxInstructions)
	}
	if m.OutputLen > lim.MaxOutputLen {
		return fmt.Errorf("%w: manifest output %d > limit %d", ErrBudget, m.OutputLen, lim.MaxOutputLen)
	}
	var total, inserts uint64
	for i, ins := range script {
		var n uint64
		switch ins.Op {
		case OpCopy:
			// Overflow-safe bounds check: Off <= oldLen and Len <= oldLen-Off.
			if ins.Off > oldLen || ins.Len > oldLen-ins.Off {
				return fmt.Errorf("%w: instruction %d copies [%d,%d) from image of %d bytes",
					ErrRange, i, ins.Off, ins.Off+ins.Len, oldLen)
			}
			n = ins.Len
		case OpInsert:
			n = uint64(len(ins.Data))
			if n > lim.MaxInsertBytes-inserts {
				return fmt.Errorf("%w: instruction %d exceeds insert budget %d", ErrBudget, i, lim.MaxInsertBytes)
			}
			inserts += n
		case OpZero:
			n = ins.Len
		default:
			return fmt.Errorf("%w: instruction %d has opcode %d", ErrUnknownOp, i, ins.Op)
		}
		if n > lim.MaxOutputLen-total {
			return fmt.Errorf("%w: instruction %d exceeds output budget %d", ErrBudget, i, lim.MaxOutputLen)
		}
		total += n
	}
	if total != m.OutputLen {
		return fmt.Errorf("%w: script produces %d bytes, manifest expects %d", ErrOutputLen, total, m.OutputLen)
	}
	return nil
}

// Apply validates the script, writes the patched image to an isolated
// temporary file in the target directory, and publishes it to target with
// an atomic rename only if the output length and SHA-256 match the
// manifest. The original image is opened read-only by the caller and is
// never modified; COPY instructions read exclusively from it. On any
// failure the temporary file is removed and target is never created.
func Apply(old io.ReaderAt, oldLen uint64, script []Instruction, m Manifest, lim Limits, target string) (err error) {
	if err := Validate(script, oldLen, m, lim); err != nil {
		return err
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".patch-inflight-*")
	if err != nil {
		return fmt.Errorf("netsec: create isolated target: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	w := io.MultiWriter(tmp, h)
	var written uint64
	for i, ins := range script {
		var n int64
		var werr error
		switch ins.Op {
		case OpCopy:
			// Validated above: Off+Len <= oldLen <= MaxInt64.
			src := io.NewSectionReader(old, int64(ins.Off), int64(ins.Len))
			n, werr = io.Copy(w, src)
		case OpInsert:
			var nw int
			nw, werr = w.Write(ins.Data)
			n = int64(nw)
		case OpZero:
			n, werr = io.CopyN(w, zeroReader{}, int64(ins.Len))
		}
		if werr != nil {
			err = fmt.Errorf("netsec: execute instruction %d: %w", i, werr)
			return err
		}
		written += uint64(n)
	}

	if written != m.OutputLen {
		err = fmt.Errorf("%w: wrote %d bytes, manifest expects %d", ErrOutputLen, written, m.OutputLen)
		return err
	}
	sum := h.Sum(nil)
	if subtle.ConstantTimeCompare(sum, m.SHA256[:]) != 1 {
		err = ErrDigestMismatch
		return err
	}
	if err = tmp.Sync(); err != nil {
		err = fmt.Errorf("netsec: sync isolated target: %w", err)
		return err
	}
	if err = tmp.Close(); err != nil {
		err = fmt.Errorf("netsec: close isolated target: %w", err)
		return err
	}
	if err = os.Rename(tmpName, target); err != nil {
		err = fmt.Errorf("netsec: publish target: %w", err)
		return err
	}
	return nil
}

var zeroBlock [32 << 10]byte

// zeroReader is an infinite source of zero bytes, avoiding a Len-sized
// allocation for OpZero.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		n += copy(p[n:], zeroBlock[:])
	}
	return n, nil
}
