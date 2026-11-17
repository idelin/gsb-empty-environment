package netsec

import (
	"context"
	"time"
)

// Killer delivers signals to process instances. The host implements it
// against real PIDs; because targets are Instances, a signal never
// reaches a reused PID's new incarnation by mistake.
type Killer interface {
	// Terminate asks the process to exit (SIGTERM semantics).
	Terminate(inst Instance) error
	// Kill forces the process to exit (SIGKILL semantics).
	Kill(inst Instance) error
}

// Terminate runs the full cleanup protocol for tok's generation:
//
//  1. Freeze the generation so Admit/Spawn refuse new forks.
//  2. Snapshot the reachable descendant set and SIGTERM every member.
//  3. Wait up to grace for confirmed exits, then SIGKILL survivors.
//  4. Repeat until the set is empty (catching anything that raced the
//     freeze) or ctx is cancelled.
//
// It returns nil only when every member's exit has been confirmed via
// Exit events. A non-nil error from Killer is fatal; ctx cancellation
// returns ctx.Err().
func (t *Tracker) Terminate(ctx context.Context, tok Token, k Killer, grace time.Duration) error {
	t.Freeze(tok)

	signaled := make(map[Instance]bool)
	for {
		set := t.Snapshot(tok)
		if len(set) == 0 {
			return nil
		}
		for _, inst := range set {
			if signaled[inst] {
				continue
			}
			signaled[inst] = true
			if err := k.Terminate(inst); err != nil {
				return err
			}
		}
		if err := t.awaitSet(ctx, tok, grace); err != nil {
			return err
		}

		survivors := t.Snapshot(tok)
		if len(survivors) == 0 {
			continue // new members may still race in; re-check at top
		}
		for _, inst := range survivors {
			if err := k.Kill(inst); err != nil {
				return err
			}
		}
		if err := t.awaitSet(ctx, tok, 0); err != nil {
			return err
		}
	}
}

// awaitSet blocks until tok's reachable set is empty, the timeout
// (if > 0) elapses, or ctx is cancelled. A timeout is not an error:
// the caller escalates. Only ctx cancellation returns an error.
func (t *Tracker) awaitSet(ctx context.Context, tok Token, timeout time.Duration) error {
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	for {
		if len(t.Snapshot(tok)) == 0 {
			return nil
		}
		wake := t.wakeCh()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer:
			return nil
		case <-wake:
		}
	}
}
