package versionedcache_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	vc "versionedcache"
)

func ExampleStore() {
	var sourceCalls atomic.Int32
	now := time.Unix(100, 0)
	store, err := vc.New(vc.Options[string]{
		DataSource: func(_ context.Context, request vc.SourceRequest[string]) error {
			sourceCalls.Add(1)
			fmt.Printf("source write: version=%d value=%q retry=%q\n",
				request.Version, request.Value, request.RetryID)
			return nil
		},
		Publish: func(_ context.Context, entry vc.Entry[string]) error {
			if entry.Version == 2 {
				return errors.New("cache unavailable")
			}
			fmt.Printf("cache publish: version=%d value=%q\n", entry.Version, entry.Value)
			return nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ctx := context.Background()

	first, err := store.Write(ctx, "ready", "request-1")
	fmt.Printf("first status=%s error=%v\n", first.Status, err)

	entry, ok, err := store.Read(ctx)
	if err != nil || !ok {
		panic(err)
	}
	fmt.Printf("read value=%q\n", entry.Value)

	_, err = store.Write(ctx, "will-not-cache", "")
	fmt.Printf("partial error=%v\n", err)

	retry, err := store.Write(ctx, "ready", "request-1")
	fmt.Printf("retry status=%s version=%d error=%v source-calls=%d\n",
		retry.Status, retry.Version, err, sourceCalls.Load())

	// Output:
	// source write: version=1 value="ready" retry="request-1"
	// cache publish: version=1 value="ready"
	// first status=committed error=<nil>
	// read value="ready"
	// source write: version=2 value="will-not-cache" retry=""
	// partial error=versionedcache: cache publish failed: version 2: cache unavailable
	// retry status=committed version=1 error=<nil> source-calls=2
}
