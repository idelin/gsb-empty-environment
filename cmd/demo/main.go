// Command demo reproduces the classic write-skew anomaly and shows how
// SSI prevents it: T1 and T2 each read accounts A and B (100 each,
// invariant A+B >= 0) and withdraw 80 from a different account. Plain
// snapshot isolation would commit both; SSI aborts T2.
package main

import (
	"errors"
	"fmt"

	ssi "example.com/ssi"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	s := ssi.NewStore()
	s.Set("A", 100)
	s.Set("B", 100)
	fmt.Println("initial: A=100 B=100 (invariant: A+B >= 0)")

	t1 := s.Begin()
	t2 := s.Begin()
	fmt.Printf("begin  %s\nbegin  %s\n", t1, t2)

	// T1 reads both accounts and withdraws 80 from A.
	a1, _, err := t1.Get("A")
	check(err)
	b1, _, err := t1.Get("B")
	check(err)
	fmt.Printf("T1 reads A=%d B=%d, withdraws 80 from A\n", a1, b1)
	check(t1.Put("A", a1-80))

	// T2 concurrently reads both accounts (same snapshot) and
	// withdraws 80 from B.
	a2, _, err := t2.Get("A")
	check(err)
	b2, _, err := t2.Get("B")
	check(err)
	fmt.Printf("T2 reads A=%d B=%d, withdraws 80 from B\n", a2, b2)
	check(t2.Put("B", b2-80))

	// T1 commits; SSI detects the dangerous structure
	// T1 -rw-> T2 -rw-> T1 and aborts the pivot T2.
	check(t1.Commit())
	fmt.Printf("commit %s\n", t1)

	err = t2.Commit()
	if errors.Is(err, ssi.ErrSerializationFailure) {
		fmt.Printf("commit T2: ABORTED (%v)\n", err)
	} else {
		panic("T2 should have been aborted by SSI")
	}

	a, _ := s.Snapshot("A")
	b, _ := s.Snapshot("B")
	fmt.Printf("final:   A=%d B=%d  A+B=%d (invariant holds: %v)\n",
		a, b, a+b, a+b >= 0)
}
