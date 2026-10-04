package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	k := flag.Int("k", 0, "number of groups / palette entries (required)")
	levels := flag.Int("levels", 256, "number of gray levels")
	verify := flag.Bool("verify", false, "cross-check with brute-force enumeration (small inputs only)")
	flag.Parse()

	if *k < 1 {
		fmt.Fprintln(os.Stderr, "error: -k must be >= 1")
		os.Exit(2)
	}

	var r io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}

	w, err := readHistogram(r, *levels)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	sol := Solve(w, *k)
	if len(sol.Groups) == 0 {
		fmt.Println("empty histogram: nothing to quantize")
		return
	}

	nonEmpty := 0
	for _, c := range w {
		if c > 0 {
			nonEmpty++
		}
	}
	fmt.Printf("levels: 0..%d  non-empty: %d  groups: %d\n", *levels-1, nonEmpty, len(sol.Groups))
	fmt.Printf("max abs error: %d\n", sol.MaxErr)
	fmt.Printf("weighted SSE:  %d\n", sol.SSE)
	fmt.Println("palette / groups:")
	for i, g := range sol.Groups {
		fmt.Printf("  group %2d  levels [%3d..%3d]  rep = %3d  pixels = %d  maxerr = %d  sse = %d\n",
			i, g.From, g.To, g.Rep, g.Pixels, g.MaxErr, g.SSE)
	}

	if *verify {
		be, bs := BruteForce(w, *k)
		if be != sol.MaxErr || bs != sol.SSE {
			fmt.Printf("VERIFY FAILED: dp=(%d,%d) brute=(%d,%d)\n", sol.MaxErr, sol.SSE, be, bs)
			os.Exit(1)
		}
		fmt.Printf("verify: brute-force agrees (maxerr=%d sse=%d)\n", be, bs)
	}
}

// readHistogram parses whitespace-separated lines, ignoring blanks and
// '#' comments. A line with one integer is the count of the next
// sequential level; a line with two integers is "level count".
func readHistogram(r io.Reader, levels int) ([]int64, error) {
	w := make([]int64, levels)
	sc := bufio.NewScanner(r)
	sc.Split(bufio.ScanLines)
	seq := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch len(fields) {
		case 1:
			c, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad count %q: %v", fields[0], err)
			}
			if seq >= levels {
				return nil, fmt.Errorf("more than %d counts given", levels)
			}
			w[seq] = c
			seq++
		case 2:
			lv, err := strconv.Atoi(fields[0])
			if err != nil {
				return nil, fmt.Errorf("bad level %q: %v", fields[0], err)
			}
			c, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad count %q: %v", fields[1], err)
			}
			if lv < 0 || lv >= levels {
				return nil, fmt.Errorf("level %d out of range [0,%d)", lv, levels)
			}
			w[lv] = c
		default:
			return nil, fmt.Errorf("cannot parse line %q", line)
		}
	}
	return w, sc.Err()
}
