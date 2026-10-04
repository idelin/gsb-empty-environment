package leasearb

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Script DSL, one command per line ('#' starts a comment):
//
//	lease 10s                       set lease TTL (fresh arbiter state)
//	tick 5s                         advance the manual clock (clock jump)
//	acquire RES HOLDER              acquire/transfer; prints new generation
//	renew RES HOLDER GEN            renew; fails on superseded generations
//	write RES HOLDER GEN REQID PAY  arbitrated write (PAY may contain spaces)
//	expect STATUS [substr]          assert the last op's outcome
//	snapshot RES                    print holder/generation/expiry/writes
//
// Scripts are deterministic: time only moves via tick, so replaying a script
// always produces the same trace.

type lastOutcome struct {
	status Status
	detail string
	err    error
}

// Runner executes event scripts against an Arbiter with a ManualClock.
type Runner struct {
	clock   *ManualClock
	arbiter *Arbiter
	start   time.Time
	out     io.Writer
	last    lastOutcome
}

func NewRunner(out io.Writer) *Runner {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	return &Runner{
		clock:   clock,
		arbiter: NewArbiter(clock, 10*time.Second),
		start:   start,
		out:     out,
	}
}

// Run replays a script, printing a trace line per command. It returns an
// error on the first failed expect or malformed command.
func (r *Runner) Run(name, script string) error {
	fmt.Fprintf(r.out, "=== replay %s ===\n", name)
	for lineno, raw := range strings.Split(script, "\n") {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := r.step(line); err != nil {
			return fmt.Errorf("%s:%d: %q: %w", name, lineno+1, line, err)
		}
	}
	fmt.Fprintf(r.out, "--- %s OK ---\n\n", name)
	return nil
}

func (r *Runner) tracef(format string, args ...interface{}) {
	elapsed := r.clock.Now().Sub(r.start)
	fmt.Fprintf(r.out, "[t=%-6s] "+format+"\n", append([]interface{}{elapsed.Truncate(time.Millisecond)}, args...)...)
}

func (r *Runner) step(line string) error {
	f := strings.Fields(line)
	cmd := f[0]
	switch cmd {
	case "lease":
		ttl, err := time.ParseDuration(f[1])
		if err != nil {
			return err
		}
		r.arbiter = NewArbiter(r.clock, ttl)
		r.tracef("lease ttl = %s (state reset)", ttl)
	case "tick":
		d, err := time.ParseDuration(f[1])
		if err != nil {
			return err
		}
		r.clock.Advance(d)
		r.tracef("tick +%s (clock jump)", d)
	case "acquire":
		gen, exp := r.arbiter.Acquire(f[1], f[2])
		r.last = lastOutcome{status: StatusApplied, detail: "acquired"}
		r.tracef("acquire %s %s -> gen=%d expires=+%s", f[1], f[2], gen, exp.Sub(r.clock.Now()))
	case "renew":
		gen, err := strconv.ParseUint(f[3], 10, 64)
		if err != nil {
			return err
		}
		exp, rerr := r.arbiter.Renew(f[1], f[2], gen)
		r.last = lastOutcome{status: StatusApplied, detail: "renewed", err: rerr}
		if rerr != nil {
			r.tracef("renew %s %s gen=%d -> ERROR: %v", f[1], f[2], gen, rerr)
		} else {
			r.tracef("renew %s %s gen=%d -> OK expires=+%s", f[1], f[2], gen, exp.Sub(r.clock.Now()))
		}
	case "write":
		gen, err := strconv.ParseUint(f[3], 10, 64)
		if err != nil {
			return err
		}
		payload := ""
		if len(f) > 5 {
			payload = strings.Join(f[5:], " ")
		}
		res := r.arbiter.Write(f[1], f[2], gen, f[4], payload)
		r.last = lastOutcome{status: res.Status, detail: res.Detail}
		r.tracef("write %s %s gen=%d req=%s -> %s", f[1], f[2], gen, f[4], res)
	case "expect":
		want := Status(strings.ToUpper(f[1]))
		if r.last.err != nil {
			if want != StatusStale {
				return fmt.Errorf("expected %s, got error %v", want, r.last.err)
			}
		} else if r.last.status != want {
			return fmt.Errorf("expected %s, got %s (%s)", want, r.last.status, r.last.detail)
		}
		if len(f) > 2 {
			sub := strings.Join(f[2:], " ")
			hay := r.last.detail
			if r.last.err != nil {
				hay = r.last.err.Error()
			}
			if !strings.Contains(hay, sub) {
				return fmt.Errorf("expected detail containing %q, got %q", sub, hay)
			}
		}
		r.tracef("expect %s -> satisfied", strings.Join(f[1:], " "))
	case "snapshot":
		snap, ok := r.arbiter.Snapshot(f[1])
		if !ok {
			r.tracef("snapshot %s -> <absent>", f[1])
			return nil
		}
		r.tracef("snapshot %s -> holder=%s gen=%d expired=%v writes=%q",
			f[1], snap.Holder, snap.Generation, snap.Expired, snap.Writes)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}
