// Command bigsqrt reads a non-negative integer as whitespace-separated
// decimal chunks from stdin and prints its integer square root and
// remainder, satisfying r*r <= n < (r+1)*(r+1).
//
// Flags:
//   -max-digits int   maximum decimal digits accepted (default 1000000)
//   -budget     int   big-integer operation budget, 0 = unlimited
//
// Example:
//   echo "1234567890 1234567890" | bigsqrt
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"

	"bigsqrt"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bigsqrt:", err)
	os.Exit(1)
}

func main() {
	maxDigits := flag.Int("max-digits", 1000000, "maximum decimal digits accepted")
	ops := flag.Int64("budget", 0, "big-integer operation budget (0 = unlimited)")
	flag.Parse()

	var budget *bigsqrt.Budget
	if *ops > 0 {
		budget = bigsqrt.NewBudget(*ops)
	}

	b, err := bigsqrt.NewBuilder(*maxDigits)
	if err != nil {
		fatal(err)
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Split(bufio.ScanWords)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if err := b.Feed(sc.Text(), budget); err != nil {
			fatal(err)
		}
	}
	if err := sc.Err(); err != nil {
		fatal(err)
	}

	st, err := bigsqrt.NewState(b.Num())
	if err != nil {
		fatal(err)
	}
	if err := st.Run(budget); err != nil {
		fatal(err)
	}
	r, rem, err := st.Result()
	if err != nil {
		fatal(err)
	}
	fmt.Println("r =", r)
	fmt.Println("rem =", rem)
}
