// Command ledger is a small CLI for the append-only ledger.
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"

	"ledger"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: ledger <command> [args]

  fund     <file> <reqID> <acct> <cents>        credit an account
  transfer <file> <reqID> <from> <to> <cents>   move cents between accounts
  reverse  <file> <reqID> <seq>                 reverse a committed transfer
  balance  <file> <acct>                        current balance
  snapshot <file> <seq>                         balances as of record seq
  export   <file>                               dump ledger as JSON lines
  verify   <file>                               independently re-verify file
`)
	os.Exit(2)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func parseCents(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		die(fmt.Errorf("bad amount %q: %w", s, err))
	}
	return n
}

func parseSeq(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		die(fmt.Errorf("bad seq %q: %w", s, err))
	}
	return n
}

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	cmd, file := os.Args[1], os.Args[2]
	args := os.Args[3:]

	switch cmd {
	case "fund", "transfer", "reverse":
		l, err := ledger.Open(file)
		if err != nil {
			die(err)
		}
		defer l.Close()
		var seq uint64
		switch cmd {
		case "fund":
			if len(args) != 3 {
				usage()
			}
			seq, err = l.Fund(args[0], args[1], parseCents(args[2]))
		case "transfer":
			if len(args) != 4 {
				usage()
			}
			seq, err = l.Transfer(args[0], args[1], args[2], parseCents(args[3]))
		case "reverse":
			if len(args) != 2 {
				usage()
			}
			seq, err = l.Reverse(args[0], parseSeq(args[1]))
		}
		if errors.Is(err, ledger.ErrConflict) {
			die(fmt.Errorf("request ID already used with different content: %w", err))
		}
		if err != nil {
			die(err)
		}
		fmt.Printf("committed seq=%d\n", seq)

	case "balance":
		if len(args) != 1 {
			usage()
		}
		l, err := ledger.Open(file)
		if err != nil {
			die(err)
		}
		defer l.Close()
		fmt.Printf("%s %d\n", args[0], l.Balance(args[0]))

	case "snapshot":
		if len(args) != 1 {
			usage()
		}
		l, err := ledger.Open(file)
		if err != nil {
			die(err)
		}
		defer l.Close()
		bal, err := l.BalancesAt(parseSeq(args[0]))
		if err != nil {
			die(err)
		}
		printBalances(bal)

	case "export":
		l, err := ledger.Open(file)
		if err != nil {
			die(err)
		}
		defer l.Close()
		if err := l.Export(os.Stdout); err != nil {
			die(err)
		}

	case "verify":
		bal, err := ledger.Verify(file)
		if err != nil {
			die(err)
		}
		fmt.Println("chain OK")
		printBalances(bal)

	default:
		usage()
	}
}

func printBalances(bal map[string]int64) {
	accts := make([]string, 0, len(bal))
	for a := range bal {
		accts = append(accts, a)
	}
	sort.Strings(accts)
	for _, a := range accts {
		fmt.Printf("%s %d\n", a, bal[a])
	}
}
