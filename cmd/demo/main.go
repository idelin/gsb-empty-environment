// Demo of the tagcache store: multi-tag entries, tag-version invalidation,
// reverse-index cleanup, generation-checked loads and injectable
// source/clock/failure handling.
package main

import (
	"context"
	"fmt"
	"time"

	"tagcache"
)

func main() {
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

	// Injectable data source: a fake "database".
	db := map[string]string{"article:1": "Hello", "article:2": "World"}
	store := tagcache.New(tagcache.Options{
		Source: func(_ context.Context, key string) (any, error) {
			fmt.Printf("  [source] loading %s\n", key)
			return db[key], nil
		},
		Clock:           func() time.Time { return now }, // injectable clock
		OnFailure:       func(key string, err error) { fmt.Printf("  [failure] %s: %v\n", key, err) },
		TTL:             time.Hour,
		MaxLoadAttempts: 3,
	})

	fmt.Println("1) Load article:1 with two tags (billed once):")
	v, _ := store.Get(ctx, "article:1", "topic:go", "author:delin")
	fmt.Printf("   value=%v stats=%+v\n", v, store.Stats())

	fmt.Println("2) Cache hit via either tag (no reload):")
	v, _ = store.Get(ctx, "article:1", "topic:go")
	fmt.Printf("   value=%v billed=%d\n", v, store.Stats().BilledLoads)

	fmt.Println("3) Tag version change invalidates related entries:")
	store.InvalidateTag("topic:go")
	v, _ = store.Get(ctx, "article:1", "topic:go", "author:delin")
	fmt.Printf("   value=%v billed=%d\n", v, store.Stats().BilledLoads)

	fmt.Println("4) DeleteTag detaches one tag, keeps other relations:")
	store.DeleteTag("author:delin")
	v, _ = store.Get(ctx, "article:1", "topic:go")
	fmt.Printf("   value=%v billed=%d (still a hit)\n", v, store.Stats().BilledLoads)

	fmt.Println("5) Delete cleans the reverse index and releases resources:")
	_, _ = store.Get(ctx, "article:2", "topic:go")
	store.Delete("article:1")
	store.Delete("article:2")
	fmt.Printf("   stats=%+v\n", store.Stats())

	fmt.Println("6) Injected clock drives expiry:")
	_, _ = store.Get(ctx, "article:1", "topic:go")
	now = now.Add(2 * time.Hour)
	_, _ = store.Get(ctx, "article:1", "topic:go")
	fmt.Printf("   billed=%d (expired entry reloaded)\n", store.Stats().BilledLoads)
}
