// Command demo exercises the write-through cache coordinator end to end
// using only in-process fakes: no remote cache is involved.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"example.com/writecache/store"
)

type memDS struct {
	mu    sync.Mutex
	data  map[string][]byte
	slow  map[string]time.Duration
	calls int
}

func (m *memDS) write(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	d := m.slow[string(value)]
	m.mu.Unlock()
	if d > 0 {
		time.Sleep(d) // simulate a slow data source write
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memDS) Close() error { fmt.Println("  [ds] closed (resource released)"); return nil }

type memCache struct {
	mu   sync.Mutex
	data map[string]string
}

func (m *memCache) publish(_ context.Context, key string, value []byte, version int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = fmt.Sprintf("%s@v%d", value, version)
	return nil
}

func main() {
	ctx := context.Background()
	ds := &memDS{data: map[string][]byte{}, slow: map[string]time.Duration{}}
	cache := &memCache{data: map[string]string{}}

	var failPublish atomic.Bool
	s := store.New(store.Options{
		DataSource: ds.write,
		Cache:      cache.publish,
		Clock:      time.Now,
		Fail: func(op string) error {
			if op == store.OpCachePublish && failPublish.Load() {
				return errors.New("injected cache outage")
			}
			return nil
		},
		Closer: ds,
	})
	defer s.Close()

	fmt.Println("== 1. successful write publishes to cache ==")
	res, _ := s.Write(ctx, "req-1", "user:1", []byte("alice"))
	fmt.Printf("  write req-1 -> %+v\n", res)
	v, ver, _ := s.Read("user:1")
	fmt.Printf("  read  user:1 -> %q (version %d), cache=%q\n", v, ver, cache.data["user:1"])

	fmt.Println("== 2. data source failure keeps old value ==")
	boom := errors.New("ds disk full")
	sFail := store.New(store.Options{
		DataSource: ds.write,
		Cache:      cache.publish,
		Fail: func(op string) error {
			if op == store.OpDataSourceWrite {
				return boom
			}
			return nil
		},
	})
	if _, err := sFail.Write(ctx, "req-2", "user:1", []byte("mallory")); err != nil {
		fmt.Printf("  write req-2 -> error: %v\n", err)
	}
	v, ver, _ = s.Read("user:1")
	fmt.Printf("  read  user:1 -> %q (version %d) old value kept\n", v, ver)

	fmt.Println("== 3. slow old write cannot overwrite newer version ==")
	ds.mu.Lock()
	ds.slow["old-value"] = 150 * time.Millisecond
	ds.mu.Unlock()
	staleErr := make(chan error, 1)
	go func() {
		_, err := s.Write(ctx, "req-3", "user:2", []byte("old-value"))
		staleErr <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the slow write start first
	res, _ = s.Write(ctx, "req-4", "user:2", []byte("new-value"))
	fmt.Printf("  fast write req-4 -> version %d\n", res.Version)
	fmt.Printf("  slow write req-3 -> %v\n", <-staleErr)
	v, ver, _ = s.Read("user:2")
	fmt.Printf("  read  user:2 -> %q (version %d), cache=%q\n", v, ver, cache.data["user:2"])

	fmt.Println("== 4. cache publish failure marks partial, retry by ID completes ==")
	failPublish.Store(true)
	res, err := s.Write(ctx, "req-5", "user:3", []byte("carol"))
	var perr *store.PartialError
	errors.As(err, &perr)
	fmt.Printf("  write req-5 -> status=%s err=%v\n", res.Status, err)
	v, _, _ = s.Read("user:3")
	fmt.Printf("  read  user:3 -> %q (data source success is not denied)\n", v)
	failPublish.Store(false)
	dsCallsBefore := ds.calls
	res, err = s.Write(ctx, "req-5", "user:3", []byte("carol"))
	fmt.Printf("  retry req-5 -> status=%s err=%v (ds re-executed: %v), cache=%q\n",
		res.Status, err, ds.calls != dsCallsBefore, cache.data["user:3"])

	fmt.Println("== 5. retry with same ID is idempotent ==")
	dsCallsBefore = ds.calls
	again, _ := s.Write(ctx, "req-4", "user:2", []byte("new-value"))
	fmt.Printf("  retry req-4 -> version %d (same as before, ds re-executed: %v)\n",
		again.Version, ds.calls != dsCallsBefore)

	fmt.Println("== 6. close releases resources ==")
	if err := s.Close(); err != nil {
		fmt.Println("  close error:", err)
	}
	if _, err := s.Write(ctx, "req-6", "user:1", []byte("x")); err != nil {
		fmt.Printf("  write after close -> %v\n", err)
	}
}
