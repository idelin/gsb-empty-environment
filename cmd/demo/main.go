// Command demo walks through the declared consistency behaviour of
// the L1/L2 shared-version cache using only in-process fakes.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"llcache"
)

func main() {
	ctx := context.Background()

	// Injectable data source, clock and failure hook: no remote cache.
	source := map[string]llcache.Value{"user:1": {Data: "alice@v1", Version: 1}}
	loader := func(_ context.Context, key string) (llcache.Value, error) {
		if v, ok := source[key]; ok {
			return v, nil
		}
		return llcache.Value{}, fmt.Errorf("no such key %q", key)
	}
	l2Down := false
	failHook := func(op string) error {
		if l2Down {
			return errors.New("L2 unreachable (" + op + ")")
		}
		return nil
	}

	l2 := llcache.NewL2(
		llcache.WithNotifyDelay(300*time.Millisecond), // slow invalidation link
		llcache.WithL2Clock(time.Now),
		llcache.WithL2FailHook(failHook),
	)
	node := llcache.NewNode(l2, loader)
	defer func() { node.Close(); l2.Close() }()

	fmt.Println("== 1. prime L1 from the data source ==")
	r, _ := node.Get(ctx, "user:1", llcache.Eventual)
	fmt.Printf("eventual read: %q v%d (source=%s)\n", r.Value.Data, r.Value.Version, r.Source)
	r, _ = node.Get(ctx, "user:1", llcache.Eventual)
	fmt.Printf("eventual read: %q v%d (source=%s)\n", r.Value.Data, r.Value.Version, r.Source)

	fmt.Println("\n== 2. L2 updated; notification delayed 300ms ==")
	source["user:1"] = llcache.Value{Data: "alice@v2", Version: 2}
	must(l2.Update("user:1", llcache.Value{Data: "alice@v2", Version: 2}))
	r, _ = node.Get(ctx, "user:1", llcache.Eventual)
	fmt.Printf("eventual read (declared stale ok): %q v%d (source=%s)\n", r.Value.Data, r.Value.Version, r.Source)
	r, _ = node.Get(ctx, "user:1", llcache.Strong)
	fmt.Printf("strong read (revalidates now):     %q v%d (source=%s)\n", r.Value.Data, r.Value.Version, r.Source)

	fmt.Println("\n== 3. wait out the notification delay ==")
	time.Sleep(350 * time.Millisecond)
	r, _ = node.Get(ctx, "user:1", llcache.Eventual)
	fmt.Printf("eventual read (converged): %q v%d (source=%s)\n", r.Value.Data, r.Value.Version, r.Source)

	fmt.Println("\n== 4. version rollback rejected ==")
	err := l2.Update("user:1", llcache.Value{Data: "alice@v1-again", Version: 1})
	fmt.Printf("update with older version: %v\n", err)

	fmt.Println("\n== 5. L2 outage: explicit degradation, no fake strong consistency ==")
	l2Down = true
	delete(source, "user:1") // data source unreachable too
	r, err = node.Get(ctx, "user:1", llcache.Eventual)
	fmt.Printf("eventual read: %q v%d (source=%s stale=%v degraded=%v)\n",
		r.Value.Data, r.Value.Version, r.Source, r.Stale, r.Degraded)
	_, err = node.Get(ctx, "user:1", llcache.Strong)
	fmt.Printf("strong read:   error=%v\n", err)

	fmt.Println("\n== 6. Close releases resources ==")
	node.Close()
	l2.Close()
	fmt.Println("node.Close() and l2.Close() returned: all goroutines stopped")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
