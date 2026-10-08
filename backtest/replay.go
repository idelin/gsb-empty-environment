package backtest

import "fmt"

// Replay re-runs the exact same input through a fresh engine and strategy
// and verifies that the produced action sequence is identical to the
// recorded one. It is the determinism check: same events in, same
// decisions out, regardless of wall-clock or map iteration order.
func Replay(events []Event, recorded []Action, strategy Strategy) (*Result, error) {
	res, err := NewEngine(strategy).Run(events)
	if err != nil {
		return nil, err
	}
	if !EqualActions(recorded, res.Audit.Actions) {
		return res, fmt.Errorf("backtest: replay mismatch: recorded %d actions, replayed %d, sequences differ",
			len(recorded), len(res.Audit.Actions))
	}
	return res, nil
}

// VerifyDeterministic runs the backtest runs times with independent
// engines and confirms every run yields the identical action sequence.
func VerifyDeterministic(events []Event, newStrategy func() Strategy, runs int) error {
	if runs < 2 {
		runs = 2
	}
	first, err := NewEngine(newStrategy()).Run(events)
	if err != nil {
		return err
	}
	for i := 1; i < runs; i++ {
		res, err := NewEngine(newStrategy()).Run(events)
		if err != nil {
			return err
		}
		if !EqualActions(first.Audit.Actions, res.Audit.Actions) {
			return fmt.Errorf("backtest: run %d diverged from run 0", i)
		}
	}
	return nil
}
