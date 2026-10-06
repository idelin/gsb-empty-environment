package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

// Event is one explicit input event: "t=<time> <op> [args...]".
// Ops: send N | ack SEQ | dupack SEQ | drop SEQ | timeout SEQ | tick
type Event struct {
	Time int
	Op   string
	Args []int
}

func parseEvents(text string) ([]Event, error) {
	var events []Event
	lastTime := 0
	for ln, raw := range strings.Split(text, "\n") {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if !strings.HasPrefix(f[0], "t=") {
			return nil, fmt.Errorf("line %d: missing t=<time> prefix: %q", ln+1, raw)
		}
		t, err := strconv.Atoi(f[0][2:])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad time: %v", ln+1, err)
		}
		if t < lastTime {
			return nil, fmt.Errorf("line %d: time goes backwards (%d < %d)", ln+1, t, lastTime)
		}
		lastTime = t
		if len(f) < 2 {
			return nil, fmt.Errorf("line %d: missing op", ln+1)
		}
		ev := Event{Time: t, Op: f[1]}
		for _, a := range f[2:] {
			n, err := strconv.Atoi(a)
			if err != nil {
				return nil, fmt.Errorf("line %d: bad arg %q", ln+1, a)
			}
			ev.Args = append(ev.Args, n)
		}
		events = append(events, ev)
	}
	return events, nil
}

// run applies events to sim, checking conservation after each one, and
// returns the rendered trace lines.
func run(sim *Sim, events []Event) ([]string, error) {
	var out []string
	for _, ev := range events {
		sim.Now = ev.Time
		before := len(sim.Actions)
		head := fmt.Sprintf("t=%-4d %-8s", ev.Time, ev.Op+" "+strings.Trim(strings.Join(strings.Fields(fmt.Sprint(ev.Args)), " "), "[]"))
		switch ev.Op {
		case "send":
			sim.Send(ev.Args[0])
		case "ack", "dupack":
			sim.OnAck(ev.Args[0])
		case "drop":
			sim.Drop(ev.Args[0])
		case "timeout":
			sim.Timeout(ev.Args[0])
		case "tick":
			// pure time advance
		default:
			return out, fmt.Errorf("unknown op %q", ev.Op)
		}
		for _, a := range sim.Actions[before:] {
			kind := a.Kind
			if a.Seq >= 0 {
				kind = fmt.Sprintf("%s#%d", a.Kind, a.Seq)
			}
			out = append(out, fmt.Sprintf("%s  %-12s %s", head, kind, a.Detail))
			head = strings.Repeat(" ", len(head))
		}
		if err := sim.Check(); err != nil {
			out = append(out, fmt.Sprintf("%s  CONSERVATION-VIOLATION: %v", head, err))
			return out, fmt.Errorf("conservation violated after %+v: %v", ev, err)
		}
		out = append(out, fmt.Sprintf("%s  state: %s", head, sim.State()))
	}
	return out, nil
}

func main() {
	var input []byte
	var err error
	if len(os.Args) > 1 {
		input, err = ioutil.ReadFile(os.Args[1])
	} else {
		input, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(1)
	}
	events, err := parseEvents(string(input))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}

	sim := NewSim()
	trace, err := run(sim, events)
	for _, l := range trace {
		fmt.Println(l)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}

	fmt.Println("\n== conservation ==")
	fmt.Println("OK: sent == acked + inflight, credit released exactly once per packet")

	fmt.Println("\n== loss replay audit ==")
	violations, notes := sim.Audit()
	if len(violations) == 0 {
		fmt.Println("OK: every loss repaired by retransmission before ACK; no RTT sample after retx")
	}
	for _, v := range violations {
		fmt.Println("VIOLATION:", v)
	}
	for _, n := range notes {
		fmt.Println("note:", n)
	}

	fmt.Println("\n== determinism replay ==")
	sim2 := NewSim()
	trace2, err2 := run(sim2, events)
	if err2 != nil || strings.Join(trace, "\n") != strings.Join(trace2, "\n") {
		fmt.Println("VIOLATION: replay diverged")
		os.Exit(1)
	}
	fmt.Println("OK: replaying the event log reproduces the identical trace")

	if len(violations) > 0 {
		os.Exit(1)
	}
}
