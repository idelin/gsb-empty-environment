package cowfs_test

import (
	"fmt"

	"prescreen.local/ps-2b33a47220/cowfs"
)

// ExampleEngine walks through the full lifecycle: CoW writes, snapshot
// retention through a deadlist, exact unique_used accounting on deletion, and
// ARC-backed reads.
func ExampleEngine() {
	engine := cowfs.NewEngine(cowfs.WithARCCapacity(16))

	_ = engine.Write("ledger", []byte("v1-block"))
	if _, err := engine.Snapshot("snap-a"); err != nil {
		panic(err)
	}

	// The overwrite allocates a new block; the old one stays live on snap-a's
	// deadlist boundary instead of being freed immediately.
	_ = engine.Write("ledger", []byte("v2-block"))

	info := engine.ListSnapshots()[0]
	freed, err := engine.DeleteSnapshot("snap-a")
	if err != nil {
		panic(err)
	}

	fmt.Printf("reported unique_used=%d\n", info.UniqueUsed)
	fmt.Printf("bytes freed on delete=%d\n", freed)

	data, err := engine.Read("ledger")
	if err != nil {
		panic(err)
	}
	fmt.Printf("live data=%s\n", string(data))

	if err := engine.Validate(); err != nil {
		panic(err)
	}

	// Output:
	// reported unique_used=8
	// bytes freed on delete=8
	// live data=v2-block
}
