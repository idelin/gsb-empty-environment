// Command demo runs a small hierarchical rate-limiter scenario on a
// discrete-event, integer-tick clock. No real network traffic occurs.
package main

import (
	"fmt"
	"strings"

	"htblimit/htb"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func balances(tr *htb.Tree, ids ...string) string {
	var parts []string
	for _, id := range ids {
		b, err := tr.Balance(id)
		must(err)
		parts = append(parts, id+"="+b.RatString())
	}
	return strings.Join(parts, "  ")
}

func showSend(tr *htb.Tree, tenant string) {
	res := tr.SendNext(tenant)
	if !res.Sent {
		fmt.Printf("  send(%s) REJECTED: %s\n", tenant, res.Reason)
		return
	}
	fmt.Printf("  send(%s) packet %s size %s — deduction evidence (root->leaf):\n",
		tenant, res.Packet.ID, res.Packet.Size.RatString())
	for _, d := range res.Path {
		fmt.Printf("    bucket %-6s -%s   %s -> %s\n", d.NodeID, d.Amount.RatString(), d.Before.RatString(), d.After.RatString())
	}
}

func main() {
	fmt.Println("== hierarchical token bucket, discrete ticks, rational arithmetic ==")
	tr := htb.NewTree(htb.RejectOversize)
	must(tr.AddNode("root", "", htb.Rat(100, 1), htb.Rat(5, 1), true))
	must(tr.AddNode("eng", "root", htb.Rat(40, 1), htb.Rat(2, 1), true))
	must(tr.AddNode("sales", "root", htb.Rat(40, 1), htb.Rat(2, 1), true))
	must(tr.AddNode("alice", "eng", htb.Rat(20, 1), htb.Rat(1, 1), true))
	must(tr.AddNode("bob", "sales", htb.Rat(20, 1), htb.Rat(1, 3), true)) // 1/3 token per tick

	ids := []string{"root", "eng", "sales", "alice", "bob"}
	fmt.Printf("tick %d  start: %s\n", tr.Now(), balances(tr, ids...))

	fmt.Println("-- enqueue alice:p1(6), alice:p2(30 oversize), bob:p3(4)")
	must(tr.Enqueue("alice", htb.Packet{ID: "p1", Size: htb.Rat(6, 1)}))
	must(tr.Enqueue("alice", htb.Packet{ID: "p2", Size: htb.Rat(30, 1)}))
	must(tr.Enqueue("bob", htb.Packet{ID: "p3", Size: htb.Rat(4, 1)}))

	fmt.Println("-- tick 0: send alice p1")
	showSend(tr, "alice")

	fmt.Println("-- tick 0: alice p2 (30 > capacity 20) explicitly rejected")
	showSend(tr, "alice")

	fmt.Println("-- tick 0: cancel alice p2 — queued packets hold no tokens")
	tr.Cancel("alice", "p2")
	fmt.Printf("  after cancel: %s\n", balances(tr, ids...))

	fmt.Println("-- advance to tick 10 (rational refill: bob earns 1/3/tick)")
	tr.Advance(10)
	fmt.Printf("  tick %d: %s\n", tr.Now(), balances(tr, ids...))

	fmt.Println("-- duplicate tick 10 and past tick 3: no double refill")
	tr.Advance(10)
	tr.Advance(3)
	fmt.Printf("  tick %d: %s\n", tr.Now(), balances(tr, ids...))

	fmt.Println("-- tick 10: rate change alice 1 -> 3/tick (old rate cannot refill twice)")
	must(tr.SetRate("alice", htb.Rat(3, 1)))
	tr.Advance(15)
	fmt.Printf("  tick %d: %s\n", tr.Now(), balances(tr, ids...))

	fmt.Println("-- tick 15: reattach alice eng -> sales (queue + reserved tokens migrate)")
	must(tr.Enqueue("alice", htb.Packet{ID: "p4", Size: htb.Rat(3, 1)}))
	must(tr.Reattach("alice", "sales"))
	fmt.Printf("  after reattach: %s\n", balances(tr, ids...))
	showSend(tr, "alice")

	fmt.Println("-- tick 15: send bob p3")
	showSend(tr, "bob")
	fmt.Printf("  tick %d final: %s\n", tr.Now(), balances(tr, ids...))

	fmt.Println("== debt policy demo (AllowDebt) ==")
	d := htb.NewTree(htb.AllowDebt)
	must(d.AddNode("root", "", htb.Rat(1000, 1), htb.Rat(0, 1), true))
	must(d.AddNode("leaf", "root", htb.Rat(10, 1), htb.Rat(1, 1), true))
	must(d.Enqueue("leaf", htb.Packet{ID: "huge", Size: htb.Rat(25, 1)}))
	showSend(d, "leaf")
	fmt.Printf("  leaf balance now -15 (debt); next send blocked until refills repay it\n")
	must(d.Enqueue("leaf", htb.Packet{ID: "small", Size: htb.Rat(3, 1)}))
	showSend(d, "leaf")
	d.Advance(20)
	fmt.Printf("  tick %d after refill: leaf=%s\n", d.Now(), func() string { b, _ := d.Balance("leaf"); return b.RatString() }())
	showSend(d, "leaf")
}
