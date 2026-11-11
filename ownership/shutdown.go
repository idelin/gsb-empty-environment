package ownership

import (
	"context"
	"errors"
	"time"
)

// Killer terminates individual processes. The host supplies the platform
// implementation; tests use an in-memory fake so the cleanup protocol can be
// exercised without real processes.
type Killer interface {
	// Terminate requests graceful termination (SIGTERM).
	Terminate(pid int) error
	// ForceKill demands immediate termination (SIGKILL).
	ForceKill(pid int) error
}

// ShutdownConfig tunes a tenant cleanup.
type ShutdownConfig struct {
	// GracePeriod is the delay between SIGTERM and SIGKILL escalation. Zero
	// means escalate immediately; negative values are treated as zero.
	GracePeriod time.Duration
	// Killer delivers signals. Required.
	Killer Killer
}

// ErrShutdownTimeout is returned by Shutdown when the context expires while
// members of the generation are still alive.
var ErrShutdownTimeout = errors.New("ownership: shutdown timed out with surviving processes")

// ShutdownError carries details about an incomplete cleanup.
type ShutdownError struct {
	// Survivors are the PIDs still alive when cleanup gave up.
	Survivors []int
	// SignalErrs collects non-fatal signal delivery errors.
	SignalErrs []error
}

func (e *ShutdownError) Error() string {
	return "ownership: shutdown incomplete, surviving pids: " + joinPIDs(e.Survivors)
}

func (e *ShutdownError) Unwrap() error { return ErrShutdownTimeout }

// Shutdown runs the tenant cleanup protocol against generation:
//
//  1. Seal the generation so no further Spawned event can be accepted for it
//     (the new-spawn lock). Spawns racing the seal fail with
//     ErrGenerationSealed and therefore never join the kill set.
//  2. Snapshot the whole reachable descendant set and send Terminate to each
//     member, including orphaned children whose parent already exited.
//  3. Wait for Exited events. After GracePeriod, escalate any survivor with
//     ForceKill and keep waiting, re-escalating periodically.
//  4. Return nil once every member is dead, or a ShutdownError if ctx
//     expires first.
//
// Process-group changes between steps 2 and 3 do not affect the kill set:
// membership is keyed by the immutable generation, not the current PGID.
func Shutdown(ctx context.Context, t *Tracker, g Gen, cfg ShutdownConfig) error {
	if cfg.Killer == nil {
		return errors.New("ownership: shutdown requires a Killer")
	}
	if !g.Valid() {
		return ErrInvalidGeneration
	}

	// Step 1: block new derivation before touching any live process.
	if err := t.Apply(Sealed{Generation: g}); err != nil {
		return err
	}

	grace := cfg.GracePeriod
	if grace < 0 {
		grace = 0
	}

	// Step 2: terminate the descendant set captured behind the seal.
	signalErrs := t.signalAll(g, cfg.Killer, false)

	// Step 3: wait for confirmation, escalating after the grace period.
	graceTimer := time.NewTimer(grace)
	defer graceTimer.Stop()

	for {
		survivors := t.LiveMembers(g)
		if len(survivors) == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return &ShutdownError{
				Survivors:  append([]int(nil), survivors...),
				SignalErrs: signalErrs,
			}
		case <-graceTimer.C:
			// First firing escalates SIGTERM -> SIGKILL; later firings
			// re-kill anything still alive. Repeated SIGKILL is harmless and
			// covers processes that were uninterruptible earlier.
			signalErrs = append(signalErrs,
				t.signalAll(g, cfg.Killer, true)...)
			graceTimer.Reset(grace)
		case <-t.waitChan():
			// An Exited (or other) event landed; loop and re-query.
		}
	}
}

func (t *Tracker) signalAll(g Gen, killer Killer, force bool) []error {
	pids := t.LiveMembers(g)
	var errs []error
	for _, pid := range pids {
		var err error
		if force {
			err = killer.ForceKill(pid)
		} else {
			err = killer.Terminate(pid)
		}
		if err != nil {
			errs = append(errs, &SignalError{PID: pid, Force: force, Err: err})
		}
	}
	return errs
}

// SignalError wraps a signal delivery failure. ESRCH-style "already gone"
// errors should be swallowed by the Killer implementation rather than wrapped.
type SignalError struct {
	PID   int
	Force bool
	Err   error
}

func (e *SignalError) Error() string {
	verb := "terminate"
	if e.Force {
		verb = "force-kill"
	}
	return "ownership: failed to " + verb + " pid " + itoa(e.PID) + ": " + e.Err.Error()
}

func (e *SignalError) Unwrap() error { return e.Err }

// waitChan exposes the coalesced event channel for select-based waiting.
func (t *Tracker) waitChan() <-chan struct{} {
	t.lock()
	ch := t.wake
	t.unlock()
	return ch
}

func joinPIDs(pids []int) string {
	if len(pids) == 0 {
		return "<none>"
	}
	out := ""
	for i, pid := range pids {
		if i > 0 {
			out += ","
		}
		out += itoa(pid)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
