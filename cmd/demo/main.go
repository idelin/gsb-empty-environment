// Command demo runs a scripted discrete-event scenario against the tree
// rate limiter: sends, cancels, rational refills, a rate change, a parent
// bottleneck, and a runtime tenant re-attachment, auditing the ledger
// after every event.
package main

import (
	"fmt"
	"log"

	"treelimiter"
)

func main() {
	root := limiter.NewNode("root", limiter.Rat(100, 1), limiter.Rat(2, 1))
	video := limiter.NewNode("video", limiter.Rat(20, 1), limiter.Rat(1, 1))
	bulk := limiter.NewNode("bulk", limiter.Rat(40, 1), limiter.Rat(1, 1))
	alice := limiter.NewNode("alice", limiter.Rat(10, 1), limiter.Rat(1, 3)) // 1/3 token/tick
	bob := limiter.NewNode("bob", limiter.Rat(30, 1), limiter.Rat(0, 1))

	l := limiter.NewLimiter(root)
	l.Attach(root, video)
	l.Attach(root, bulk)
	l.Attach(video, alice)
	l.Attach(video, bob)

	step := 0
	audit := func(format string, args ...interface{}) {
		step++
		fmt.Printf("[%02d] %s\n", step, fmt.Sprintf(format, args...))
		if err := l.CheckLedger(); err != nil {
			log.Fatalf("ledger violated after step %d: %v", step, err)
		}
	}
	state := func() {
		fmt.Printf("     tokens: root=%s video=%s bulk=%s alice=%s bob=%s | queued: alice=%d bob=%d\n",
			limiter.Fmt(root.Tokens), limiter.Fmt(video.Tokens), limiter.Fmt(bulk.Tokens),
			limiter.Fmt(alice.Tokens), limiter.Fmt(bob.Tokens),
			l.QueueLen(alice), l.QueueLen(bob))
	}
	send := func(leaf *limiter.Node, name string) {
		if l.QueueLen(leaf) == 0 {
			fmt.Printf("     %s queue empty\n", name)
			return
		}
		if ev, ok := l.SendNext(leaf); ok {
			fmt.Printf("     sent pkt %d @tick %d, path evidence:\n", ev.PacketID, ev.Tick)
			for _, d := range ev.Deductions {
				fmt.Printf("       %-5s -%s -> %s\n", d.Node, limiter.Fmt(d.Amount), limiter.Fmt(d.Remaining))
			}
		} else {
			fmt.Printf("     %s head packet blocked (some ancestor lacks tokens); nothing deducted\n", name)
		}
	}

	audit("tick 0: enqueue alice#1(4), alice#2(5), bob#3(6)")
	must(l.Enqueue(alice, limiter.Packet{ID: 1, Size: 4}))
	must(l.Enqueue(alice, limiter.Packet{ID: 2, Size: 5}))
	must(l.Enqueue(bob, limiter.Packet{ID: 3, Size: 6}))
	state()

	audit("send alice#1: atomic deduction along root->video->alice")
	send(alice, "alice")
	state()

	audit("cancel alice#2: queued packet consumes no tokens")
	l.Cancel(alice, 2)
	state()

	audit("oversize packet alice#9(11) explicitly rejected (alice capacity 10)")
	if err := l.Enqueue(alice, limiter.Packet{ID: 9, Size: 11}); err != nil {
		fmt.Printf("     rejected: %v\n", err)
	}

	audit("advance to tick 3, applied twice: duplicate tick does not refill again")
	l.AdvanceTo(3)
	l.AdvanceTo(3)
	fmt.Printf("     alice refilled 3 * 1/3 = exactly 1\n")
	state()

	audit("rate change alice 1/3 -> 2 per tick, then advance to tick 4")
	l.SetRate(alice, limiter.Rat(2, 1))
	l.AdvanceTo(4)
	fmt.Printf("     alice gained exactly 2 under the new rate, old interval not re-refilled\n")
	state()

	audit("drain video via bob so the PARENT becomes the bottleneck")
	send(bob, "bob")
	must(l.Enqueue(bob, limiter.Packet{ID: 4, Size: 10}))
	send(bob, "bob")
	state()

	audit("enqueue alice#5(6): child has quota but parent video does not")
	must(l.Enqueue(alice, limiter.Packet{ID: 5, Size: 6}))
	send(alice, "alice")
	state()

	audit("reattach alice video -> bulk: queue and reserved tokens migrate atomically")
	l.Reattach(alice, bulk)
	state()

	audit("send alice#5 under the new parent: evidence path goes through bulk")
	send(alice, "alice")
	state()

	fmt.Printf("\nall %d events audited OK; %d packets sent with full path evidence\n", step, len(l.Sent))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
