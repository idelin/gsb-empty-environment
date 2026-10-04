package paxos

import (
	"fmt"
	"strconv"
	"strings"
)

// RunScript executes a replay script against s. One event per line:
//
//	tick N                  advance the clock
//	campaign NODE           node starts phase 1 with a fresh ballot
//	propose NODE VALUE...   client proposal (VALUE may be "no-op")
//	deliver ID              deliver pending message ID
//	drop ID                 drop pending message ID
//	dup ID                  duplicate pending message ID
//	deliver-all             drain the queue FIFO (fair delivery)
//	drop-all                drop every pending message
//	crash NODE              crash node (durable state survives)
//	restart NODE            restart node from its disk
//	diskfail NODE           injected persistence failure (Save fails)
//	diskheal NODE           clear the injected persistence failure
//	expect-chosen SLOT VALUE...   assert chosen value ("no-op" allowed)
//	expect-not-chosen SLOT        assert slot is not chosen
//	expect-applied NODE LEN       assert node's applied prefix length >= LEN
//
// Blank lines and lines starting with # are ignored. The run fails if any
// expectation fails or if the safety checker recorded a violation.
func RunScript(s *Sim, text string) error {
	for ln, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := execLine(s, line); err != nil {
			return fmt.Errorf("line %d (%q): %v", ln+1, line, err)
		}
	}
	if vs := s.checker.Violations(); len(vs) > 0 {
		return fmt.Errorf("safety violations: %s", strings.Join(vs, "; "))
	}
	return nil
}

func execLine(s *Sim, line string) error {
	fields := strings.Fields(line)
	op := fields[0]
	need := func(n int) error {
		if len(fields) < n+1 {
			return fmt.Errorf("%s needs %d argument(s)", op, n)
		}
		return nil
	}
	num := func(i int) (int, error) {
		v, err := strconv.Atoi(fields[i])
		if err != nil {
			return 0, fmt.Errorf("bad integer %q", fields[i])
		}
		return v, nil
	}
	rest := func(i int) string {
		if len(fields) <= i {
			return ""
		}
		return strings.Join(fields[i:], " ")
	}

	switch op {
	case "tick":
		if err := need(1); err != nil {
			return err
		}
		t, err := num(1)
		if err != nil {
			return err
		}
		s.Tick(t)
	case "campaign":
		if err := need(1); err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		s.Campaign(n)
	case "propose":
		if err := need(2); err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		s.Propose(n, parseValue(rest(2)))
	case "deliver", "drop", "dup":
		if err := need(1); err != nil {
			return err
		}
		id, err := num(1)
		if err != nil {
			return err
		}
		ok := false
		switch op {
		case "deliver":
			ok = s.Deliver(id)
		case "drop":
			ok = s.Drop(id)
		case "dup":
			ok = s.Duplicate(id) >= 0
		}
		if !ok {
			return fmt.Errorf("no pending message #%d", id)
		}
	case "deliver-all":
		s.DeliverAll()
	case "drop-all":
		s.DropAll()
	case "crash":
		if err := need(1); err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		s.Crash(n)
	case "restart":
		if err := need(1); err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		s.Restart(n)
	case "diskfail", "diskheal":
		if err := need(1); err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		if op == "diskfail" {
			s.DiskFail(n)
		} else {
			s.DiskHeal(n)
		}
	case "expect-chosen":
		if err := need(2); err != nil {
			return err
		}
		slot, err := num(1)
		if err != nil {
			return err
		}
		want := parseValue(rest(2))
		got, ok := s.checker.Chosen(slot)
		if !ok {
			return fmt.Errorf("slot %d is not chosen, want %s", slot, printable(want))
		}
		if got != want {
			return fmt.Errorf("slot %d chosen as %s, want %s", slot, printable(got), printable(want))
		}
	case "expect-not-chosen":
		if err := need(1); err != nil {
			return err
		}
		slot, err := num(1)
		if err != nil {
			return err
		}
		if v, ok := s.checker.Chosen(slot); ok {
			return fmt.Errorf("slot %d unexpectedly chosen as %s", slot, printable(v))
		}
	case "expect-applied":
		if err := need(2); err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		k, err := num(2)
		if err != nil {
			return err
		}
		if got := len(s.Node(n).Applied()); got < k {
			return fmt.Errorf("node %d applied %d slots, want >= %d", n, got, k)
		}
	default:
		return fmt.Errorf("unknown op %q", op)
	}
	return nil
}

func parseValue(s string) string {
	if s == "no-op" {
		return NoOp
	}
	return s
}
