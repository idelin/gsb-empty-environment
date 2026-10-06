package main

import "fmt"

// The simulator drives every clock tick explicitly; nothing in the
// system reads wall-clock time.
func main() {
	clock := NewClock()
	store := NewStore()
	manager := NewLeaseManager(clock, store)
	resource := NewResource()

	write := func(who string, lease Lease, value string) {
		err := resource.Write(lease.Fence, value)
		if err != nil {
			fmt.Printf("t=%-3d %-4s WRITE %-14q fence=%d -> REJECTED: %v\n", clock.Now(), who, value, lease.Fence, err)
			return
		}
		fmt.Printf("t=%-3d %-4s WRITE %-14q fence=%d -> accepted\n", clock.Now(), who, value, lease.Fence)
	}

	fmt.Println("== two competitors, interleaved ==")

	leaseA, err := manager.Grant("A", 10)
	must(err)
	fmt.Printf("t=%-3d A    GRANT  %s\n", clock.Now(), leaseA)

	write("A", leaseA, "A:init")

	clock.Advance(4)
	leaseA, err = manager.Renew("A", leaseA.Fence, 10)
	must(err)
	fmt.Printf("t=%-3d A    RENEW  %s (fence unchanged)\n", clock.Now(), leaseA)
	write("A", leaseA, "A:update")

	if _, err := manager.Grant("B", 10); err != nil {
		fmt.Printf("t=%-3d B    GRANT  -> REJECTED: %v\n", clock.Now(), err)
	}

	// A stalls (e.g. GC pause); the simulator lets its lease expire.
	clock.Advance(11)
	fmt.Printf("t=%-3d A    (stalled, lease expired)\n", clock.Now())

	leaseB, err := manager.Grant("B", 10)
	must(err)
	fmt.Printf("t=%-3d B    GRANT  %s\n", clock.Now(), leaseB)
	write("B", leaseB, "B:takes-over")

	// A wakes up and tries a late write with its old fence.
	write("A", leaseA, "A:stale")

	fmt.Println("== persistence failure and recovery ==")

	clock.Advance(11) // B's lease expires
	store.SetFailing(true)
	if _, err := manager.Grant("C", 10); err != nil {
		fmt.Printf("t=%-3d C    GRANT  -> REJECTED: %v (no fence consumed)\n", clock.Now(), err)
	}
	store.SetFailing(false)

	// Crash: the manager's in-memory state is lost. Recovery reloads
	// the fence counter from the durable store, so it can never issue
	// a fence lower than one granted before the crash.
	manager = NewLeaseManager(clock, store)
	fmt.Printf("t=%-3d      (manager crashed and recovered, durable fence=%d)\n", clock.Now(), store.LoadFence())

	leaseC, err := manager.Grant("C", 10)
	must(err)
	fmt.Printf("t=%-3d C    GRANT  %s (fence still increasing after recovery)\n", clock.Now(), leaseC)
	write("C", leaseC, "C:recovered")

	// The recovered manager must also reject the pre-crash master.
	write("B", leaseB, "B:stale")

	fmt.Println("== accepted write history ==")
	for i, rec := range resource.History() {
		fmt.Printf("%d. fence=%d value=%q\n", i+1, rec.Fence, rec.Value)
	}
	fmt.Printf("final data: %q\n", resource.Data())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
