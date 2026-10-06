// Package fir implements an FIR filter with exact rational arithmetic.
//
// All samples and coefficients are exact rationals (math/big.Rat), so the
// output is deterministic and bit-exact regardless of how the input is
// partitioned into blocks, or whether the filter is checkpointed and
// restored mid-stream.
package fir

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// CheckpointVersion is the version written into exported checkpoints.
// Restoring a checkpoint with a different version fails with
// ErrCheckpointVersion.
const CheckpointVersion = 1

// ErrCheckpointVersion is returned when a checkpoint's version does not
// match CheckpointVersion.
var ErrCheckpointVersion = errors.New("fir: unsupported checkpoint version")

// Coeff is a rational filter coefficient Num/Den. Den must be non-zero.
type Coeff struct {
	Num int64
	Den int64
}

func (c Coeff) rat() *big.Rat {
	return big.NewRat(c.Num, c.Den)
}

func coeffsToRats(coeffs []Coeff) ([]*big.Rat, error) {
	if len(coeffs) == 0 {
		return nil, errors.New("fir: coefficient list must not be empty")
	}
	rats := make([]*big.Rat, len(coeffs))
	for i, c := range coeffs {
		if c.Den == 0 {
			return nil, fmt.Errorf("fir: coefficient %d has zero denominator", i)
		}
		rats[i] = c.rat()
	}
	return rats, nil
}

type pendingSwitch struct {
	at     uint64
	coeffs []*big.Rat
}

// Filter is an FIR filter over an exact rational sample stream.
//
// The state consists of the absolute position of the next sample and the
// history of preceding samples; both are fully captured by Export.
type Filter struct {
	coeffs  []*big.Rat
	pending *pendingSwitch
	hist    []*big.Rat // previous samples, most recent first
	pos     uint64     // absolute index of the next sample to process
}

// NewFilter returns a Filter using the given coefficients. The filter
// starts at position 0 with an empty (all-zero) history.
func NewFilter(coeffs []Coeff) (*Filter, error) {
	rats, err := coeffsToRats(coeffs)
	if err != nil {
		return nil, err
	}
	return &Filter{coeffs: rats}, nil
}

// Pos returns the absolute index of the next sample to be processed,
// i.e. the number of samples processed so far.
func (f *Filter) Pos() uint64 {
	return f.pos
}

// histCap is the number of preceding samples that must be retained: enough
// for the active coefficients and for any scheduled future coefficients,
// so that old history still participates after a switch.
func (f *Filter) histCap() int {
	n := len(f.coeffs)
	if f.pending != nil && len(f.pending.coeffs) > n {
		n = len(f.pending.coeffs)
	}
	return n - 1
}

// SetCoeffsAt schedules a coefficient switch to take effect at the sample
// boundary at: the first sample processed with the new coefficients is the
// one with absolute index at. Samples before at keep using the current
// coefficients. The retained history of preceding samples is shared: after
// the switch, old samples still participate in the new convolution.
//
// at must not be in the past. If at equals the current position the switch
// applies immediately. A previously scheduled switch is replaced.
func (f *Filter) SetCoeffsAt(coeffs []Coeff, at uint64) error {
	rats, err := coeffsToRats(coeffs)
	if err != nil {
		return err
	}
	if at < f.pos {
		return fmt.Errorf("fir: switch position %d is in the past (pos %d)", at, f.pos)
	}
	f.pending = &pendingSwitch{at: at, coeffs: rats}
	f.trimHist()
	return nil
}

// applyPending activates a scheduled switch whose boundary has been reached.
func (f *Filter) applyPending() {
	if f.pending != nil && f.pos >= f.pending.at {
		f.coeffs = f.pending.coeffs
		f.pending = nil
		f.trimHist()
	}
}

func (f *Filter) trimHist() {
	if c := f.histCap(); len(f.hist) > c {
		f.hist = f.hist[:c]
	}
}

// Process filters one sample and returns the exact rational output.
func (f *Filter) Process(x *big.Rat) *big.Rat {
	f.applyPending()
	y := new(big.Rat)
	term := new(big.Rat)
	for i, c := range f.coeffs {
		var s *big.Rat
		if i == 0 {
			s = x
		} else if i-1 < len(f.hist) {
			s = f.hist[i-1]
		} else {
			break // beyond available history: implicit zeros
		}
		y.Add(y, term.Mul(c, s))
	}
	f.hist = append([]*big.Rat{new(big.Rat).Set(x)}, f.hist...)
	f.trimHist()
	f.pos++
	return y
}

// ProcessBlock filters a block of samples and returns the outputs, one per
// input sample. An empty (or nil) block is a no-op and returns an empty
// slice. Processing samples one block at a time yields exactly the same
// outputs as processing them one at a time, for any block partitioning.
func (f *Filter) ProcessBlock(in []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(in))
	for i, x := range in {
		out[i] = f.Process(x)
	}
	return out
}

// PendingCheckpoint is the exported form of a scheduled coefficient switch.
type PendingCheckpoint struct {
	At     uint64   `json:"at"`
	Coeffs []string `json:"coeffs"`
}

// Checkpoint is the exported, serializable state of a Filter. Restoring a
// checkpoint and continuing to process yields exactly the same outputs as
// an uninterrupted run.
type Checkpoint struct {
	Version int                `json:"version"`
	Pos     uint64             `json:"pos"`
	Coeffs  []string           `json:"coeffs"`
	Pending *PendingCheckpoint `json:"pending,omitempty"`
	History []string           `json:"history"`
}

func ratsToStrings(rats []*big.Rat) []string {
	s := make([]string, len(rats))
	for i, r := range rats {
		s[i] = r.RatString()
	}
	return s
}

func stringsToRats(s []string, what string) ([]*big.Rat, error) {
	rats := make([]*big.Rat, len(s))
	for i, str := range s {
		r, ok := new(big.Rat).SetString(str)
		if !ok {
			return nil, fmt.Errorf("fir: invalid %s value %q", what, str)
		}
		rats[i] = r
	}
	return rats, nil
}

// Export captures the complete filter state as a Checkpoint.
func (f *Filter) Export() *Checkpoint {
	cp := &Checkpoint{
		Version: CheckpointVersion,
		Pos:     f.pos,
		Coeffs:  ratsToStrings(f.coeffs),
		History: ratsToStrings(f.hist),
	}
	if f.pending != nil {
		cp.Pending = &PendingCheckpoint{
			At:     f.pending.at,
			Coeffs: ratsToStrings(f.pending.coeffs),
		}
	}
	return cp
}

// ExportBytes captures the filter state and serializes it as JSON.
func (f *Filter) ExportBytes() ([]byte, error) {
	return json.Marshal(f.Export())
}

// Restore rebuilds a Filter from a Checkpoint. It returns
// ErrCheckpointVersion if the checkpoint version is unsupported, and an
// error if the checkpoint is otherwise malformed.
func Restore(cp *Checkpoint) (*Filter, error) {
	if cp == nil {
		return nil, errors.New("fir: nil checkpoint")
	}
	if cp.Version != CheckpointVersion {
		return nil, fmt.Errorf("%v: got %d, want %d", ErrCheckpointVersion, cp.Version, CheckpointVersion)
	}
	coeffs, err := stringsToRats(cp.Coeffs, "coefficient")
	if err != nil {
		return nil, err
	}
	if len(coeffs) == 0 {
		return nil, errors.New("fir: checkpoint has no coefficients")
	}
	hist, err := stringsToRats(cp.History, "history")
	if err != nil {
		return nil, err
	}
	f := &Filter{coeffs: coeffs, hist: hist, pos: cp.Pos}
	if cp.Pending != nil {
		pending, err := stringsToRats(cp.Pending.Coeffs, "pending coefficient")
		if err != nil {
			return nil, err
		}
		if len(pending) == 0 {
			return nil, errors.New("fir: checkpoint pending switch has no coefficients")
		}
		if cp.Pending.At < cp.Pos {
			return nil, fmt.Errorf("fir: checkpoint pending switch at %d precedes pos %d", cp.Pending.At, cp.Pos)
		}
		f.pending = &pendingSwitch{at: cp.Pending.At, coeffs: pending}
	}
	if len(f.hist) > f.histCap() {
		return nil, fmt.Errorf("fir: checkpoint history too long: %d > %d", len(f.hist), f.histCap())
	}
	return f, nil
}

// RestoreBytes deserializes a checkpoint produced by ExportBytes and
// rebuilds the Filter.
func RestoreBytes(data []byte) (*Filter, error) {
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("fir: decoding checkpoint: %v", err)
	}
	return Restore(&cp)
}
