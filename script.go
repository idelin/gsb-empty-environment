package paxos

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// RunScript executes a text script of simulation events against s, writing
// the event trace and any requested output to w. One command per line;
// blank lines and lines starting with '#' are ignored.
//
//	tick <n>            set the integer logical clock
//	leader <node>       node starts phase 1 with a fresh ballot
//	propose <node> <v>  submit client value v to node
//	deliver <id>        deliver pending message id
//	deliverto <node>    deliver all pending messages addressed to node
//	deliverall          deliver every pending message (FIFO, fair)
//	drop <id>           drop pending message id
//	dropto <node>       drop all pending messages addressed to node
//	dropall             drop every pending message
//	dup <id>            duplicate pending message id
//	crash <node>        crash node (volatile state lost)
//	restart <node>      restart node (durable state reloaded)
//	state               print chosen evidence, applied prefixes, node states
//	check               run the independent safety checker
func RunScript(s *Sim, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	printed := 0
	flush := func() {
		for ; printed < len(s.Trace); printed++ {
			fmt.Fprintln(w, s.Trace[printed])
		}
	}
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := runCommand(s, line, w); err != nil {
			flush()
			return fmt.Errorf("line %d (%q): %v", lineNo, line, err)
		}
		flush()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func runCommand(s *Sim, line string, w io.Writer) error {
	fields := strings.Fields(line)
	cmd := fields[0]
	atoi := func(i int) (int, error) {
		if len(fields) <= i {
			return 0, fmt.Errorf("missing argument")
		}
		return strconv.Atoi(fields[i])
	}
	switch cmd {
	case "tick":
		n, err := atoi(1)
		if err != nil {
			return err
		}
		s.Tick = n
	case "leader":
		i, err := atoi(1)
		if err != nil {
			return err
		}
		s.StartLeader(i)
	case "propose":
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			return fmt.Errorf("usage: propose <node> <value>")
		}
		i, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return err
		}
		s.Propose(i, Value(strings.TrimSpace(parts[2])))
	case "deliver":
		id, err := atoi(1)
		if err != nil {
			return err
		}
		if !s.Deliver(id) {
			return fmt.Errorf("no pending message #%d", id)
		}
	case "deliverto":
		i, err := atoi(1)
		if err != nil {
			return err
		}
		s.DeliverWhere(func(e Envelope) bool { return e.To == i })
	case "deliverall":
		s.DeliverAll()
	case "drop":
		id, err := atoi(1)
		if err != nil {
			return err
		}
		if !s.Drop(id) {
			return fmt.Errorf("no pending message #%d", id)
		}
	case "dropto":
		i, err := atoi(1)
		if err != nil {
			return err
		}
		for _, e := range s.Pending() {
			if e.To == i {
				s.Drop(e.ID)
			}
		}
	case "dropall":
		s.DropAll()
	case "dup":
		id, err := atoi(1)
		if err != nil {
			return err
		}
		if _, ok := s.Dup(id); !ok {
			return fmt.Errorf("no pending message #%d", id)
		}
	case "crash":
		i, err := atoi(1)
		if err != nil {
			return err
		}
		s.Crash(i)
	case "restart":
		i, err := atoi(1)
		if err != nil {
			return err
		}
		s.Restart(i)
	case "state":
		PrintState(s, w)
	case "check":
		if err := s.Checker.Check(s.Nodes); err != nil {
			fmt.Fprintf(w, "check FAILED: %v\n", err)
			return err
		}
		fmt.Fprintln(w, "check OK: no slot chosen with two values; all applied values chosen")
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}

// PrintState writes chosen evidence, applied prefixes and node states.
func PrintState(s *Sim, w io.Writer) {
	chosen := s.Checker.Chosen()
	slots := make([]int, 0, len(chosen))
	for slot := range chosen {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	fmt.Fprintln(w, "== chosen evidence ==")
	if len(slots) == 0 {
		fmt.Fprintln(w, "  (nothing chosen yet)")
	}
	for _, slot := range slots {
		ev := chosen[slot]
		fmt.Fprintf(w, "  slot %d: value=%s ballot=%s acceptors=%v\n", slot, formatValue(ev.Value), ev.Ballot, ev.Acceptors)
	}
	fmt.Fprintln(w, "== node states ==")
	for _, nd := range s.Nodes {
		fmt.Fprintf(w, "  %s\n", nd.StateString())
	}
}
