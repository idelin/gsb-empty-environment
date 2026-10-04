// Command ledger is a small CLI over the ledger package.
//
//	ledger transfer <file> <req-hex> <from> <to> <cents>
//	ledger reverse  <file> <req-hex> <orig-seq>
//	ledger balance  <file> <acct>
//	ledger snapshot <file> <seq>
//	ledger export   <file> <out>
//	ledger verify   <file>
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"

	"example.com/ledger/ledger"
)

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func parseReq(s string) ledger.RequestID {
	var id ledger.RequestID
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		die(fmt.Errorf("request ID must be 16 bytes hex (32 chars), got %q", s))
	}
	copy(id[:], b)
	return id
}

func atou(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		die(fmt.Errorf("bad number %q: %w", s, err))
	}
	return n
}

func atoi(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		die(fmt.Errorf("bad number %q: %w", s, err))
	}
	return n
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ledger <transfer|reverse|balance|snapshot|export|verify> <file> [args]")
		os.Exit(2)
	}
	cmd, file := os.Args[1], os.Args[2]
	args := os.Args[3:]

	if cmd == "verify" {
		bal, n, err := ledger.VerifyFile(file)
		if err != nil {
			die(err)
		}
		fmt.Printf("OK: %d records, hash chain intact\n", n)
		accts := make([]uint64, 0, len(bal))
		for a := range bal {
			accts = append(accts, a)
		}
		sort.Slice(accts, func(i, j int) bool { return accts[i] < accts[j] })
		for _, a := range accts {
			fmt.Printf("  account %-6d %12d cents\n", a, bal[a])
		}
		return
	}

	l, err := ledger.Open(file)
	if err != nil {
		die(err)
	}
	defer l.Close()

	switch cmd {
	case "transfer":
		rec, err := l.Transfer(parseReq(args[0]), atou(args[1]), atou(args[2]), atoi(args[3]))
		if err != nil {
			die(err)
		}
		fmt.Printf("committed seq=%d %d -> %d: %d cents\n", rec.Seq, rec.From, rec.To, rec.Amount)
	case "reverse":
		rec, err := l.Reverse(parseReq(args[0]), atou(args[1]))
		if err != nil {
			die(err)
		}
		fmt.Printf("committed reversal seq=%d of seq=%d: %d cents back\n", rec.Seq, rec.ReversalOf, rec.Amount)
	case "balance":
		fmt.Printf("%d cents\n", l.Balance(atou(args[0])))
	case "snapshot":
		bal, err := l.Snapshot(atou(args[0]))
		if err != nil {
			die(err)
		}
		accts := make([]uint64, 0, len(bal))
		for a := range bal {
			accts = append(accts, a)
		}
		sort.Slice(accts, func(i, j int) bool { return accts[i] < accts[j] })
		for _, a := range accts {
			fmt.Printf("  account %-6d %12d cents\n", a, bal[a])
		}
	case "export":
		out, err := os.Create(args[0])
		if err != nil {
			die(err)
		}
		defer out.Close()
		if err := l.Export(out); err != nil {
			die(err)
		}
		fmt.Println("exported to", args[0])
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		os.Exit(2)
	}
}
