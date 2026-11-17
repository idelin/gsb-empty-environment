package netsec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gatedStore lets a test pause creation at each persistence boundary while
// independent readers probe visibility.
type gatedStore struct {
	*MemoryStore
	reached chan string
	release chan struct{}
}

func newGatedStore() *gatedStore {
	return &gatedStore{
		MemoryStore: NewMemoryStore(),
		reached:     make(chan string, 1),
		release:     make(chan struct{}),
	}
}

func (g *gatedStore) hit(point string) {
	g.reached <- point
	<-g.release
}

func (g *gatedStore) CreateObject(ctx context.Context, meta ObjectMeta) (Token, error) {
	token, err := g.MemoryStore.CreateObject(ctx, meta)
	if err != nil {
		return "", err
	}
	g.hit("object-staged")
	return token, nil
}

func (g *gatedStore) PersistLabels(ctx context.Context, token Token, labels Labels, digest LabelDigest) error {
	if err := g.MemoryStore.PersistLabels(ctx, token, labels, digest); err != nil {
		return err
	}
	g.hit("labels-persisted")
	return nil
}

func (g *gatedStore) Publish(ctx context.Context, token Token) error {
	g.hit("before-publish")
	return g.MemoryStore.Publish(ctx, token)
}

func (g *gatedStore) Abort(ctx context.Context, token Token) error {
	g.hit("before-abort")
	return g.MemoryStore.Abort(ctx, token)
}

func expectPoint(t *testing.T, store *gatedStore, want string) {
	t.Helper()
	select {
	case got := <-store.reached:
		if got != want {
			t.Fatalf("creation reached %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

func TestReadersNeverObservePartialAuthorizationState(t *testing.T) {
	ctx := context.Background()
	store := newGatedStore()
	host := newTestHost(t, store,
		WithInheritedLabels(Labels{"tenant": "acme"}),
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)
	want := Labels{
		"tenant":         "acme",
		"classification": "secret",
		"owner":          "alice",
	}

	type createResult struct {
		obj *Object
		err error
	}
	done := make(chan createResult, 1)
	go func() {
		obj, err := host.Create(ctx, CreateRequest{
			ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
		})
		done <- createResult{obj: obj, err: err}
	}()

	for _, point := range []string{"object-staged", "labels-persisted", "before-publish"} {
		expectPoint(t, store, point)
		if stats := store.Stats(); stats.Pending != 1 || stats.Committed != 0 {
			t.Fatalf("%s: Stats = %#v", point, stats)
		}
		for i := 0; i < 64; i++ {
			if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
				t.Fatalf("%s: host reader saw partial object: %v", point, err)
			}
			if _, err := store.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
				t.Fatalf("%s: store reader saw partial object: %v", point, err)
			}
		}
		store.release <- struct{}{}
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Create: %v", result.err)
		}
		if !result.obj.Labels().Equal(want) {
			t.Fatalf("created labels = %#v, want %#v", result.obj.Labels(), want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Create")
	}

	obj, err := host.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("Get after publish: %v", err)
	}
	if !obj.Labels().Equal(want) || obj.Digest() != want.Digest() {
		t.Fatalf("published object labels=%#v digest=%s", obj.Labels(), obj.Digest())
	}
	if stats := store.Stats(); stats.Pending != 0 || stats.Committed != 1 {
		t.Fatalf("Stats after publish = %#v", stats)
	}
}

func TestConcurrentReadersSeeNotFoundOrCompleteState(t *testing.T) {
	ctx := context.Background()
	store := newGatedStore()
	host := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)
	want := Labels{"classification": "secret", "owner": "alice"}

	stop := make(chan struct{})
	firstComplete := make(chan struct{})
	var firstOnce sync.Once
	var wg sync.WaitGroup
	var complete atomic.Int64
	var unexpected atomic.Int64
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				obj, err := host.Get(ctx, "doc-1")
				if err != nil {
					if !errors.Is(err, ErrObjectNotFound) {
						unexpected.Add(1)
					}
					continue
				}
				if !obj.Labels().Equal(want) || obj.Digest() != want.Digest() {
					unexpected.Add(1)
					continue
				}
				complete.Add(1)
				firstOnce.Do(func() { close(firstComplete) })
			}
		}()
	}

	done := make(chan error, 1)
	go func() {
		_, err := host.Create(ctx, CreateRequest{
			ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
		})
		done <- err
	}()

	for _, point := range []string{"object-staged", "labels-persisted", "before-publish"} {
		expectPoint(t, store, point)
		time.Sleep(5 * time.Millisecond)
		store.release <- struct{}{}
	}
	if err := <-done; err != nil {
		t.Fatalf("Create: %v", err)
	}
	select {
	case <-firstComplete:
	case <-time.After(2 * time.Second):
		t.Fatal("readers never observed the published complete object")
	}
	close(stop)
	wg.Wait()

	if got := unexpected.Load(); got != 0 {
		t.Fatalf("readers observed %d incomplete or inconsistent states", got)
	}
	if got := complete.Load(); got == 0 {
		t.Fatal("readers never observed the published complete object")
	}
}

func TestFailedCreationStaysInvisibleDuringCleanup(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected failure")
	tests := []struct {
		name   string
		faults Faults
		points []string
	}{
		{
			name:   "label persistence",
			faults: Faults{PersistLabels: boom},
			points: []string{"object-staged"},
		},
		{
			name:   "publication",
			faults: Faults{Publish: boom},
			points: []string{"object-staged", "labels-persisted", "before-publish"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newGatedStore()
			store.SetFaults(tc.faults)
			host := newTestHost(t, store,
				WithMandatoryLabels(Labels{"classification": "secret"}),
			)

			done := make(chan error, 1)
			go func() {
				_, err := host.Create(ctx, CreateRequest{
					ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
				})
				done <- err
			}()

			for _, point := range tc.points {
				expectPoint(t, store, point)
				if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
					t.Fatalf("%s: reader saw incomplete object: %v", point, err)
				}
				store.release <- struct{}{}
			}

			expectPoint(t, store, "before-abort")
			if stats := store.Stats(); stats.Pending != 1 || stats.Committed != 0 {
				t.Fatalf("before abort: Stats = %#v", stats)
			}
			for i := 0; i < 64; i++ {
				if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
					t.Fatalf("reader saw incomplete object during cleanup: %v", err)
				}
			}
			store.release <- struct{}{}

			if err := <-done; !errors.Is(err, boom) {
				t.Fatalf("Create = %v, want injected failure", err)
			}
			if stats := store.Stats(); stats.Pending != 0 || stats.Committed != 0 {
				t.Fatalf("after abort: Stats = %#v", stats)
			}
			if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
				t.Fatalf("Get after failed creation = %v", err)
			}
		})
	}
}
