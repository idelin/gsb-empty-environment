// Command tsxform scales a packet trace by rational time and byte rates in
// a discrete event model (integer ticks, no real network traffic), then
// verifies the result against an independent exact reference timeline.
//
// Input on stdin (unless -demo): one packet per line, "stream tick bytes".
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"tsxform"
)

func demoTrace() []tsxform.Packet {
	var pkts []tsxform.Packet
	for i := 0; i < 12; i++ {
		pkts = append(pkts, tsxform.Packet{Stream: 0, Time: int64(i), Size: 60})
	}
	for i := 0; i < 6; i++ {
		pkts = append(pkts, tsxform.Packet{Stream: 1, Time: int64(2 * i), Size: 400})
	}
	pkts = append(pkts, tsxform.Packet{Stream: 1, Time: 12, Size: 4000})
	pkts = append(pkts, tsxform.Packet{Stream: 2, Time: 3, Size: 1500})
	pkts = append(pkts, tsxform.Packet{Stream: 2, Time: 9, Size: 1500})
	return pkts
}

func readTrace() ([]tsxform.Packet, error) {
	var pkts []tsxform.Packet
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var p tsxform.Packet
		if _, err := fmt.Sscanf(sc.Text(), "%d %d %d", &p.Stream, &p.Time, &p.Size); err != nil {
			return nil, fmt.Errorf("bad input line %q: %v", sc.Text(), err)
		}
		pkts = append(pkts, p)
	}
	return pkts, sc.Err()
}

func main() {
	rate := flag.String("rate", "1/1", "time scaling ratio p/q (0 rejected)")
	byteRate := flag.String("bytes", "1/1", "byte scaling ratio p/q (0 rejected)")
	mtu := flag.Int64("mtu", 1500, "maximum transmission unit in bytes")
	bw := flag.Int64("bw", 100, "link bandwidth in bytes per tick")
	minGap := flag.Int64("min-gap", 0, "required minimum ideal spacing per stream in ticks")
	demo := flag.Bool("demo", false, "run the built-in demo trace")
	flag.Parse()

	tr, err := tsxform.ParseRatio(*rate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsxform:", err)
		os.Exit(2)
	}
	br, err := tsxform.ParseRatio(*byteRate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsxform:", err)
		os.Exit(2)
	}

	var pkts []tsxform.Packet
	if *demo {
		pkts = demoTrace()
	} else {
		pkts, err = readTrace()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tsxform:", err)
			os.Exit(2)
		}
	}

	cfg := tsxform.Config{MTU: *mtu, BytesPerTick: *bw, TimeRate: tr, ByteRate: br}
	out, err := tsxform.Transform(pkts, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsxform:", err)
		os.Exit(2)
	}

	inTime := make([]int64, len(pkts))
	for i, p := range pkts {
		inTime[i] = p.Time
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SEQ\tSTREAM\tIN\tIDEAL\tDEPART\tDONE\tBYTES\tFRAG")
	for _, r := range out {
		fmt.Fprintf(w, "%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d/%d\n",
			r.Seq, r.Stream, inTime[r.PacketID], r.IdealTick, r.Depart, r.Done, r.Size, r.FragIndex+1, r.FragCount)
	}
	w.Flush()

	rep := tsxform.Check(tsxform.RefSpec{
		Packets:      pkts,
		TimeSegs:     []tsxform.RateSeg{{At: 0, Rate: tr}},
		ByteRate:     br,
		MTU:          *mtu,
		BytesPerTick: *bw,
		MinSpacing:   *minGap,
	}, out)
	fmt.Printf("check: violations=%d unsatisfiable-spacing=%d max-drift=%s tick\n",
		len(rep.Violations), len(rep.Unsatisfiable), rep.MaxDrift.RatString())
	for _, v := range rep.Violations {
		fmt.Println("VIOLATION:", v)
	}
	for _, u := range rep.Unsatisfiable {
		fmt.Println("UNSATISFIABLE:", u)
	}
	if !rep.OK() {
		os.Exit(1)
	}
}
