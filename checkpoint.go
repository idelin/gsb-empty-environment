package parity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// CheckpointVersion is the current serialization format identifier.
const CheckpointVersion = "ps-parity-checkpoint/v1"

// RatRange is a JSON-friendly exact rational interval endpoint pair. Each
// endpoint is a canonical big.Rat string ("a/b" or "a").
type RatRange struct {
	Lo string `json:"lo"`
	Hi string `json:"hi"`
}

// Checkpoint is the serializable, resumable recovery state.
//
// It is bound to N, e, c and KeyVersion and carries the COMPLETE ordered query
// history plus its SHA-256 digest. Resume replays that history to rebuild exact
// state rather than trusting only the serialized interval snapshot.
type Checkpoint struct {
	Version    string `json:"version"`
	N          string `json:"n"`
	E          string `json:"e"`
	C          string `json:"c"`
	KeyVersion string `json:"key_version,omitempty"`

	BindingDigest string `json:"binding_digest"`

	// History is the complete ordered observable query history.
	History       []QueryRecord `json:"history"`
	HistoryDigest string        `json:"history_digest"`

	Step      int        `json:"step"`
	Queries   int        `json:"queries"`
	Q         string     `json:"q"`
	T         string     `json:"t"`
	Intervals []RatRange `json:"intervals"`
	Lower     string     `json:"lower"`
	Upper     string     `json:"upper"`

	// Base-blinding state (classic persistent r). C above remains the OUTER
	// bound ciphertext; WorkingC is C*r^e mod N.
	BaseBlindR string `json:"base_blind_r,omitempty"`
	WorkingC   string `json:"working_c,omitempty"`

	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

func (r *runner) buildCheckpoint(st Status, msg string) *Checkpoint {
	cp := &Checkpoint{
		Version:       CheckpointVersion,
		N:             bigHex(r.N),
		E:             bigHex(r.E),
		C:             bigHex(r.bind.C),
		KeyVersion:    r.bind.KeyVersion,
		History:       append([]QueryRecord(nil), r.history...),
		HistoryDigest: DigestHistory(r.history),
		Step:          r.step,
		Queries:       len(r.history),
		Q:             bigHex(r.q),
		T:             bigHex(r.T),
		Status:        st,
		Message:       msg,
	}
	if r.baseR != nil {
		cp.BaseBlindR = bigHex(r.baseR)
		cp.WorkingC = bigHex(r.C)
	}
	cp.BindingDigest = DigestBinding(r.bind)
	for _, iv := range r.intervals {
		cp.Intervals = append(cp.Intervals, RatRange{Lo: iv.Lo.RatString(), Hi: iv.Hi.RatString()})
	}
	if lo, hi, ok := hull(r.intervals); ok {
		cp.Lower = lo.RatString()
		cp.Upper = hi.RatString()
	}
	return cp
}

// Binding returns the public binding embedded in the checkpoint.
func (cp *Checkpoint) Binding() Binding {
	return Binding{N: hexBig(cp.N), E: hexBig(cp.E), C: hexBig(cp.C), KeyVersion: cp.KeyVersion}
}

// Verify checks the version, the supplied binding, the binding digest and the
// complete-history digest. expectedHistoryDigest may be empty to only check the
// digest stored in the checkpoint.
func (cp *Checkpoint) Verify(bind Binding, expectedHistoryDigest string) error {
	if cp == nil {
		return fmt.Errorf("%w: nil checkpoint", ErrInvalidInput)
	}
	if cp.Version != CheckpointVersion {
		return fmt.Errorf("%w: unsupported checkpoint version %q", ErrInvalidInput, cp.Version)
	}
	cb := cp.Binding()
	if cb.N == nil || bind.N == nil || cb.N.Cmp(bind.N) != 0 ||
		cb.E == nil || bind.E == nil || cb.E.Cmp(bind.E) != 0 ||
		cb.C == nil || bind.C == nil || cb.C.Cmp(bind.C) != 0 ||
		cb.KeyVersion != bind.KeyVersion {
		return fmt.Errorf("%w: checkpoint is bound to different N/e/c/keyVersion", ErrBindingMismatch)
	}
	if got := DigestBinding(bind); cp.BindingDigest != "" && got != cp.BindingDigest {
		return fmt.Errorf("%w: binding digest", ErrDigestMismatch)
	}
	gotHist := DigestHistory(cp.History)
	if cp.HistoryDigest == "" || gotHist != cp.HistoryDigest {
		return fmt.Errorf("%w: stored history digest", ErrDigestMismatch)
	}
	if expectedHistoryDigest != "" && expectedHistoryDigest != gotHist {
		return fmt.Errorf("%w: caller-pinned history digest", ErrDigestMismatch)
	}
	return nil
}

// Marshal serializes the checkpoint to indented JSON.
func (cp *Checkpoint) Marshal() ([]byte, error) {
	return json.MarshalIndent(cp, "", "  ")
}

// Save writes the checkpoint as JSON with 0600 permissions.
func (cp *Checkpoint) Save(path string) error {
	buf, err := cp.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o600)
}

// ParseCheckpoint decodes JSON checkpoint bytes.
func ParseCheckpoint(buf []byte) (*Checkpoint, error) {
	var cp Checkpoint
	if err := json.Unmarshal(buf, &cp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return &cp, nil
}

// LoadCheckpoint reads and decodes a JSON checkpoint file.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseCheckpoint(buf)
}

// IsBudget reports whether err is the budget sentinel.
func IsBudget(err error) bool { return errors.Is(err, ErrBudgetExhausted) }

// IsVersionSwitch reports whether err indicates a key version switch.
func IsVersionSwitch(err error) bool { return errors.Is(err, ErrVersionMismatch) }

// IsNoOracle reports whether err indicates no usable parity oracle.
func IsNoOracle(err error) bool { return errors.Is(err, ErrNoOracle) }
