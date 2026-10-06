package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

// RunLog replays an explicit event log against a fresh simulator.
// Grammar (one event per line, '#' starts a comment):
//   tick <ms>     advance the clock; fire timeouts as they come due
//   send <n>      application offers n packets (bounded by cwnd)
//   ack <seq>     ACK arrives for seq (duplicate ACKs release no credit)
//   drop <seq>    the network loses the outstanding copy of seq
//   timeout       force a retransmission timeout on the oldest unacked packet
func RunLog(r io.Reader, cfg Config) (*Sim, error) {
	s := NewSim(cfg)
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		arg := func() (int, error) {
			if len(f) < 2 {
				return 0, fmt.Errorf("line %d: %q needs an argument", lineNo, f[0])
			}
			n, err := strconv.Atoi(f[1])
			if err != nil {
				return 0, fmt.Errorf("line %d: bad argument %q", lineNo, f[1])
			}
			return n, nil
		}
		switch f[0] {
		case "tick":
			n, err := arg()
			if err != nil {
				return nil, err
			}
			s.Tick(n)
		case "send":
			n, err := arg()
			if err != nil {
				return nil, err
			}
			s.Send(n)
		case "ack":
			n, err := arg()
			if err != nil {
				return nil, err
			}
			s.Ack(n)
		case "drop":
			n, err := arg()
			if err != nil {
				return nil, err
			}
			s.Drop(n)
		case "timeout":
			s.Timeout()
		default:
			return nil, fmt.Errorf("line %d: unknown event %q", lineNo, f[0])
		}
	}
	return s, sc.Err()
}

func main() {
	var (
		file    = flag.String("f", "", "event log file (default: stdin)")
		cwnd    = flag.Int("cwnd", 4, "initial congestion window (packets)")
		minrto  = flag.Int("minrto", 200, "minimum RTO in ms")
		rto     = flag.Int("rto", 1000, "default RTO in ms before the first RTT sample")
		maxrto  = flag.Int("maxrto", 60000, "RTO backoff cap in ms")
		noreply = flag.Bool("no-replay-check", false, "skip the deterministic replay check")
	)
	flag.Parse()

	cfg := Config{InitCwnd: *cwnd, MinRTO: *minrto, DefaultRTO: *rto, MaxRTO: *maxrto}

	var data io.Reader = os.Stdin
	if *file != "" {
		fh, err := os.Open(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer fh.Close()
		data = fh
	}
	buf, err := ioutil.ReadAll(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	sim, err := RunLog(strings.NewReader(string(buf)), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	fmt.Println("== state trace ==")
	for _, line := range sim.Trace() {
		fmt.Println(line)
	}

	fmt.Println("\n== conservation check ==")
	if v := sim.Violations(); len(v) > 0 {
		for _, line := range v {
			fmt.Println("VIOLATION: " + line)
		}
	} else {
		fmt.Printf("OK: acked(%d) + inflight(%d) == sent(%d); cwnd=%d >= 1 after every event\n",
			sim.acked(), sim.InFlight(), sim.sent(), sim.Cwnd)
	}

	fmt.Println("\n== loss replay audit ==")
	for _, line := range sim.LossAudit() {
		fmt.Println(line)
	}

	if !*noreply {
		fmt.Println("\n== determinism replay ==")
		again, err := RunLog(strings.NewReader(string(buf)), cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		a, b := sim.Trace(), again.Trace()
		same := len(a) == len(b)
		if same {
			for i := range a {
				if a[i] != b[i] {
					same = false
					break
				}
			}
		}
		if same {
			fmt.Printf("OK: replaying the log reproduces the identical %d-line trace\n", len(a))
		} else {
			fmt.Println("VIOLATION: replay diverged")
		}
	}

	fmt.Printf("\nsummary: sent=%d acked=%d inflight=%d dupAcks=%d retransmits=%d cwnd=%d rto=%dms\n",
		sim.sent(), sim.acked(), sim.InFlight(), sim.DupAcks, sim.Retransmits, sim.Cwnd, sim.RTO)

	if len(sim.Violations()) > 0 {
		os.Exit(1)
	}
}
