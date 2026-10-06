package fir

import (
	"bytes"
	"encoding/gob"
	"errors"
)

// CheckpointVersion is the version written by Export and required by Restore.
const CheckpointVersion = 1

// ErrVersionMismatch is returned by Restore when the checkpoint was written
// by an incompatible version of this library.
var ErrVersionMismatch = errors.New("fir: checkpoint version mismatch")

// ErrInvalidCheckpoint is returned by Restore when the checkpoint data is
// malformed or inconsistent.
var ErrInvalidCheckpoint = errors.New("fir: invalid checkpoint")

type switchState struct {
	At     uint64
	Coeffs []float64
}

type checkpoint struct {
	Version int
	Coeffs  []float64
	Hist    []float64
	Count   uint64
	Pending []switchState
}

// Export serializes the complete filter state: active coefficients, the
// history ring buffer, the stream position, and any scheduled coefficient
// switches. Restoring the returned bytes yields a filter that continues
// exactly where this one left off.
func (f *Filter) Export() ([]byte, error) {
	cp := checkpoint{
		Version: CheckpointVersion,
		Coeffs:  f.Coeffs(),
		Hist:    make([]float64, len(f.hist)),
		Count:   f.count,
	}
	copy(cp.Hist, f.hist)
	for _, s := range f.pending {
		c := make([]float64, len(s.coeffs))
		copy(c, s.coeffs)
		cp.Pending = append(cp.Pending, switchState{At: s.at, Coeffs: c})
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cp); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Restore rebuilds a filter from bytes produced by Export. It returns
// ErrVersionMismatch if the checkpoint version is not supported and
// ErrInvalidCheckpoint if the data cannot be decoded or is inconsistent.
func Restore(data []byte) (*Filter, error) {
	var cp checkpoint
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cp); err != nil {
		return nil, ErrInvalidCheckpoint
	}
	if cp.Version != CheckpointVersion {
		return nil, ErrVersionMismatch
	}
	if len(cp.Coeffs) == 0 || len(cp.Hist) < len(cp.Coeffs) {
		return nil, ErrInvalidCheckpoint
	}
	f := &Filter{
		coeffs: cp.Coeffs,
		hist:   cp.Hist,
		count:  cp.Count,
	}
	for _, s := range cp.Pending {
		if len(s.Coeffs) == 0 {
			return nil, ErrInvalidCheckpoint
		}
		f.pending = append(f.pending, coeffSwitch{at: s.At, coeffs: s.Coeffs})
	}
	return f, nil
}
