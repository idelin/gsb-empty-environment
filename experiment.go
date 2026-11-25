package parity

import (
	"context"
	"errors"
)

// ExperimentReport summarizes a recovery experiment for the regression host.
//
// OracleUsable is false when no parity observation could be obtained (for
// example against the fixed pre-authentication callback).
type ExperimentReport struct {
	// OracleUsable reports whether at least one valid parity was observed.
	OracleUsable bool `json:"oracle_usable"`
	// Recovered reports whether m was recovered and c-verified.
	Recovered bool `json:"recovered"`
	// Status is the underlying recovery status.
	Status Status `json:"status"`
	// Queries is the cumulative query count.
	Queries int `json:"queries"`
	// NoOracle is true when the experiment explicitly reports no usable oracle.
	NoOracle bool `json:"no_oracle"`
	// ErrorKind is a compact classification: "", "budget", "version",
	// "no_oracle", "invalid", "other".
	ErrorKind string `json:"error_kind,omitempty"`
	// Result is the full recovery result (checkpoint/history included).
	Result *Result `json:"-"`
}

// RunExperiment executes a recovery and normalizes the outcome into a report.
// It never panics on a non-success termination; callers can inspect error via
// Result or the returned error.
func RunExperiment(ctx context.Context, p Params, cb Callback, opts Options) (ExperimentReport, error) {
	res, err := RecoverContext(ctx, p, cb, opts)
	rep := ExperimentReport{Result: res}
	if res != nil {
		rep.Status = res.Status
		rep.Queries = res.Queries
		rep.Recovered = res.Status == StatusRecovered && res.Plaintext != nil
	}
	switch {
	case err == nil:
		rep.OracleUsable = true
	case errors.Is(err, ErrNoOracle):
		rep.NoOracle = true
		rep.OracleUsable = false
		rep.ErrorKind = "no_oracle"
		if rep.Status == "" {
			rep.Status = StatusNoOracle
		}
	case errors.Is(err, ErrVersionMismatch):
		rep.OracleUsable = true
		rep.ErrorKind = "version"
		if rep.Status == "" {
			rep.Status = StatusVersionInterrupted
		}
	case errors.Is(err, ErrBudgetExhausted):
		rep.OracleUsable = true
		rep.ErrorKind = "budget"
		if rep.Status == "" {
			rep.Status = StatusBudgetExhausted
		}
	case errors.Is(err, ErrInvalidInput):
		rep.ErrorKind = "invalid"
		if rep.Status == "" {
			rep.Status = StatusInvalidInput
		}
	default:
		// Any callback/parity recorded means an oracle existed.
		if res != nil && countOutcome(res.History, OutcomeEven)+countOutcome(res.History, OutcomeOdd) > 0 {
			rep.OracleUsable = true
		}
		rep.ErrorKind = "other"
	}
	if rep.Recovered {
		rep.OracleUsable = true
	}
	return rep, err
}
