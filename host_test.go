package netsec

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func newTestHost(t *testing.T, store Store, opts ...HostOption) *Host {
	t.Helper()
	host, err := NewHost(store, opts...)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return host
}

func TestCreateMergesLabelsAndPublishes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithInheritedLabels(Labels{"tenant": "acme", "env": "dev"}),
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)

	obj, err := host.Create(ctx, CreateRequest{
		ID:   "doc-1",
		Kind: "document",
		Data: []byte("hello"),
		Labels: Labels{
			"owner": "alice",
			"env":   "prod",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := Labels{
		"tenant":         "acme",
		"env":            "prod",
		"owner":          "alice",
		"classification": "secret",
	}
	if !obj.Labels().Equal(want) {
		t.Fatalf("object labels = %#v, want %#v", obj.Labels(), want)
	}
	if obj.Digest() != want.Digest() {
		t.Fatalf("object digest = %s, want %s", obj.Digest(), want.Digest())
	}
	if got := store.Stats(); got != (StoreStats{Pending: 0, Committed: 1}) {
		t.Fatalf("Stats = %#v", got)
	}

	record, err := store.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if !record.Labels.Equal(want) || record.LabelDigest != want.Digest() {
		t.Fatalf("stored record = %#v", record)
	}
	read, err := host.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("host.Get: %v", err)
	}
	if !read.Labels().Equal(want) || read.Digest() != obj.Digest() {
		t.Fatalf("read object = %#v", read)
	}
}

type recordingStore struct {
	*MemoryStore
	mu        sync.Mutex
	ops       []Operation
	lastToken Token
}

func (r *recordingStore) record(op Operation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, op)
}

func (r *recordingStore) CreateObject(ctx context.Context, meta ObjectMeta) (Token, error) {
	r.record(OpCreateObject)
	token, err := r.MemoryStore.CreateObject(ctx, meta)
	if err == nil {
		r.mu.Lock()
		r.lastToken = token
		r.mu.Unlock()
	}
	return token, err
}

func (r *recordingStore) PersistLabels(ctx context.Context, token Token, labels Labels, digest LabelDigest) error {
	r.record(OpPersistLabels)
	return r.MemoryStore.PersistLabels(ctx, token, labels, digest)
}

func (r *recordingStore) Publish(ctx context.Context, token Token) error {
	r.record(OpPublish)
	return r.MemoryStore.Publish(ctx, token)
}

func (r *recordingStore) Abort(ctx context.Context, token Token) error {
	r.record(OpAbort)
	return r.MemoryStore.Abort(ctx, token)
}

func (r *recordingStore) operations() []Operation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Operation(nil), r.ops...)
}

func (r *recordingStore) token() Token {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastToken
}

func TestCreateValidatesBeforeAllocatingObject(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		req  CreateRequest
		want error
	}{
		{
			name: "immutable label override",
			req: CreateRequest{
				ID: "doc-1", Kind: "document",
				Labels: Labels{"classification": "public"},
			},
			want: ErrImmutableLabel,
		},
		{
			name: "invalid label",
			req: CreateRequest{
				ID: "doc-1", Kind: "document",
				Labels: Labels{"bad key": "v"},
			},
			want: ErrInvalidLabel,
		},
		{
			name: "invalid object ID",
			req:  CreateRequest{Kind: "document"},
			want: ErrInvalidRequest,
		},
		{
			name: "invalid object kind",
			req:  CreateRequest{ID: "doc-1"},
			want: ErrInvalidRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingStore{MemoryStore: NewMemoryStore()}
			host := newTestHost(t, store,
				WithInheritedLabels(Labels{"tenant": "acme"}),
				WithMandatoryLabels(Labels{"classification": "secret"}),
			)
			if _, err := host.Create(ctx, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("Create = %v, want %v", err, tc.want)
			}
			if ops := store.operations(); len(ops) != 0 {
				t.Fatalf("store operations after rejected request = %v", ops)
			}
			if got := store.Stats(); got != (StoreStats{}) {
				t.Fatalf("Stats = %#v", got)
			}
		})
	}
}

func TestCreateStoreOperationOrder(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{MemoryStore: NewMemoryStore()}
	host := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)
	if _, err := host.Create(ctx, CreateRequest{
		ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := []Operation{OpCreateObject, OpPersistLabels, OpPublish}
	got := store.operations()
	if !slices.Equal(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
}

func TestCreateFailsClosedAtEveryStoreStep(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected failure")
	tests := []struct {
		name          string
		faults        Faults
		wantPending   int
		wantCommitted int
	}{
		{
			name:          "object creation",
			faults:        Faults{CreateObject: boom},
			wantPending:   0,
			wantCommitted: 0,
		},
		{
			name:          "label persistence",
			faults:        Faults{PersistLabels: boom},
			wantPending:   0,
			wantCommitted: 0,
		},
		{
			name:          "publication",
			faults:        Faults{Publish: boom},
			wantPending:   0,
			wantCommitted: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.SetFaults(tc.faults)
			host := newTestHost(t, store,
				WithMandatoryLabels(Labels{"classification": "secret"}),
			)
			_, err := host.Create(ctx, CreateRequest{
				ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
			})
			if !errors.Is(err, boom) {
				t.Fatalf("Create = %v, want injected failure", err)
			}
			if got := store.Stats(); got.Pending != tc.wantPending || got.Committed != tc.wantCommitted {
				t.Fatalf("Stats = %#v, want pending=%d committed=%d", got, tc.wantPending, tc.wantCommitted)
			}
			if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
				t.Fatalf("Get after failure = %v, want ErrObjectNotFound", err)
			}
		})
	}
}

func TestCreateReportsAbortFailureButKeepsObjectInvisible(t *testing.T) {
	ctx := context.Background()
	persistErr := errors.New("persist failed")
	abortErr := errors.New("abort failed")
	store := &recordingStore{MemoryStore: NewMemoryStore()}
	store.SetFaults(Faults{PersistLabels: persistErr, Abort: abortErr})
	host := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)

	_, err := host.Create(ctx, CreateRequest{
		ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
	})
	if !errors.Is(err, persistErr) || !errors.Is(err, abortErr) {
		t.Fatalf("Create = %v, want both persist and abort failures", err)
	}
	if got := store.Stats(); got.Pending != 1 || got.Committed != 0 {
		t.Fatalf("Stats = %#v", got)
	}
	if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Get = %v, want ErrObjectNotFound", err)
	}

	// Clean up with the owning token; even an uncleaned staged object is not
	// visible to readers.
	store.ClearFaults()
	if err := store.Abort(ctx, store.token()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if got := store.Stats(); got.Pending != 0 || got.Committed != 0 {
		t.Fatalf("Stats after cleanup = %#v", got)
	}
}

func TestCreateInheritsParentLabels(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithInheritedLabels(Labels{"tenant": "acme", "env": "staging"}),
		WithMandatoryLabels(Labels{"classification": "internal"}),
	)

	parent, err := host.Create(ctx, CreateRequest{
		ID: "parent", Kind: "folder", Labels: Labels{"owner": "alice", "env": "dev"},
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := host.Create(ctx, CreateRequest{
		ID: "child", Kind: "document", ParentID: "parent",
		Labels: Labels{"owner": "bob"},
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	want := Labels{
		"tenant":         "acme",
		"classification": "internal",
		"owner":          "bob",
		"env":            "dev",
	}
	if !child.Labels().Equal(want) {
		t.Fatalf("child labels = %#v, want %#v", child.Labels(), want)
	}
	if child.ParentID() != parent.ID() {
		t.Fatalf("parent ID = %q", child.ParentID())
	}

	if _, err := host.Create(ctx, CreateRequest{
		ID: "orphan", Kind: "document", ParentID: "missing",
	}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("create with missing parent = %v, want ErrObjectNotFound", err)
	}
}

func TestCreateRejectsMandatoryConflictWithInheritedParent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	parentHost := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "internal"}),
	)
	if _, err := parentHost.Create(ctx, CreateRequest{ID: "parent", Kind: "folder"}); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	childHost := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "public"}),
	)
	_, err := childHost.Create(ctx, CreateRequest{
		ID: "child", Kind: "document", ParentID: "parent",
	})
	if !errors.Is(err, ErrImmutableLabel) {
		t.Fatalf("create child = %v, want ErrImmutableLabel", err)
	}
	if got := store.Stats(); got.Pending != 0 || got.Committed != 1 {
		t.Fatalf("Stats = %#v", got)
	}
}

func TestCreateUsesCustomInheritor(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithInheritor(func(ctx context.Context, req CreateRequest) (Labels, error) {
			return Labels{"tenant": "custom", "env": "dev"}, nil
		}),
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)
	obj, err := host.Create(ctx, CreateRequest{
		ID: "doc-1", Kind: "document", Labels: Labels{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := Labels{"tenant": "custom", "env": "prod", "classification": "secret"}
	if !obj.Labels().Equal(want) {
		t.Fatalf("labels = %#v, want %#v", obj.Labels(), want)
	}

	boom := errors.New("inheritor failed")
	host = newTestHost(t, NewMemoryStore(),
		WithInheritor(func(context.Context, CreateRequest) (Labels, error) {
			return nil, boom
		}),
	)
	if _, err := host.Create(ctx, CreateRequest{ID: "doc-2", Kind: "document"}); !errors.Is(err, boom) {
		t.Fatalf("Create with failing inheritor = %v", err)
	}
}

func TestReadBindsPermissionToFinalDigest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	var captured Access
	host := newTestHost(t, store,
		WithInheritedLabels(Labels{"tenant": "acme"}),
		WithMandatoryLabels(Labels{"classification": "secret"}),
		WithAuthorizer(AuthorizerFunc(func(ctx context.Context, access Access) (bool, error) {
			captured = access
			return true, nil
		})),
	)
	obj, err := host.Create(ctx, CreateRequest{
		ID: "doc-1", Kind: "document", Labels: Labels{"owner": "alice"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	read, err := host.Read(ctx, "alice", "doc-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Digest() != obj.Digest() {
		t.Fatalf("read digest = %s, want %s", read.Digest(), obj.Digest())
	}
	if captured.Subject != "alice" || captured.ObjectID != "doc-1" {
		t.Fatalf("access = %#v", captured)
	}
	if captured.LabelDigest != obj.Digest() {
		t.Fatalf("access digest = %s, want %s", captured.LabelDigest, obj.Digest())
	}
	if captured.LabelDigest == (Labels{"owner": "alice"}).Digest() {
		t.Fatal("permission check used request labels instead of the final merged labels")
	}
	if !captured.Labels.Equal(obj.Labels()) {
		t.Fatalf("access labels = %#v, want %#v", captured.Labels, obj.Labels())
	}
	if err := captured.Verify(); err != nil {
		t.Fatalf("captured access Verify: %v", err)
	}
}

func TestReadDeniesAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "secret"}),
		WithAuthorizer(LabelAuthorizer{Key: "classification", Value: "public"}),
	)
	if _, err := host.Create(ctx, CreateRequest{ID: "doc-1", Kind: "document"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := host.Read(ctx, "alice", "doc-1"); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("Read = %v, want ErrAccessDenied", err)
	}
	if _, err := host.Read(ctx, "", "doc-1"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Read with empty subject = %v, want ErrInvalidRequest", err)
	}
	if _, err := host.Read(ctx, "alice", "missing"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Read missing object = %v, want ErrObjectNotFound", err)
	}

	noAuth := newTestHost(t, NewMemoryStore())
	if _, err := noAuth.Create(ctx, CreateRequest{ID: "doc-1", Kind: "document"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := noAuth.Read(ctx, "alice", "doc-1"); !errors.Is(err, ErrNoAuthorizer) {
		t.Fatalf("Read without authorizer = %v, want ErrNoAuthorizer", err)
	}

	boom := errors.New("authorizer failed")
	failing := newTestHost(t, NewMemoryStore(),
		WithAuthorizer(AuthorizerFunc(func(context.Context, Access) (bool, error) {
			return false, boom
		})),
	)
	if _, err := failing.Create(ctx, CreateRequest{ID: "doc-1", Kind: "document"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := failing.Read(ctx, "alice", "doc-1"); !errors.Is(err, boom) {
		t.Fatalf("Read = %v, want authorizer error", err)
	}
}

func TestAccessVerifyRejectsDigestMismatch(t *testing.T) {
	labels := Labels{"owner": "alice"}
	access := Access{
		Subject:     "alice",
		ObjectID:    "doc-1",
		Labels:      labels,
		LabelDigest: Labels{"owner": "mallory"}.Digest(),
	}
	if err := access.Verify(); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Verify = %v, want ErrDigestMismatch", err)
	}
}

type tamperStore struct {
	Store
	record ObjectRecord
}

func (s *tamperStore) Get(context.Context, string) (ObjectRecord, error) {
	return s.record, nil
}

func TestGetRejectsTamperedAuthorizationState(t *testing.T) {
	ctx := context.Background()
	labels := Labels{"role": "user"}
	store := &tamperStore{record: ObjectRecord{
		ObjectMeta:  ObjectMeta{ID: "doc-1", Kind: "document"},
		Labels:      labels,
		LabelDigest: Labels{"role": "admin"}.Digest(),
	}}
	host := newTestHost(t, store)
	if _, err := host.Get(ctx, "doc-1"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Get = %v, want ErrDigestMismatch", err)
	}
}

func TestCreateRejectsTamperedParentAuthorizationState(t *testing.T) {
	ctx := context.Background()
	store := &tamperStore{record: ObjectRecord{
		ObjectMeta:  ObjectMeta{ID: "parent", Kind: "folder"},
		Labels:      Labels{"role": "user"},
		LabelDigest: Labels{"role": "admin"}.Digest(),
	}}
	host := newTestHost(t, store)
	_, err := host.Create(ctx, CreateRequest{
		ID: "child", Kind: "document", ParentID: "parent",
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Create = %v, want ErrDigestMismatch", err)
	}
}

func TestReturnedObjectCannotMutateAuthorizationState(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)
	obj, err := host.Create(ctx, CreateRequest{
		ID: "doc-1", Kind: "document", Data: []byte("hello"),
		Labels: Labels{"owner": "alice"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	obj.Labels()["classification"] = "public"
	obj.Data()[0] = 'X'

	again, err := host.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.Labels()["classification"] != "secret" {
		t.Fatalf("classification = %q", again.Labels()["classification"])
	}
	if string(again.Data()) != "hello" {
		t.Fatalf("data = %q", again.Data())
	}
}

func TestConcurrentDuplicateCreatesDoNotAbortWinner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithMandatoryLabels(Labels{"classification": "secret"}),
	)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("doc-%d", i)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := host.Create(ctx, CreateRequest{
					ID: id, Kind: "document", Labels: Labels{"owner": "alice"},
				})
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		var success, duplicate int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrObjectExists):
				duplicate++
			default:
				t.Fatalf("unexpected create error for %s: %v", id, err)
			}
		}
		if success != 1 || duplicate != 1 {
			t.Fatalf("%s: success=%d duplicate=%d", id, success, duplicate)
		}
		obj, err := host.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if obj.Labels()["classification"] != "secret" || obj.Labels()["owner"] != "alice" {
			t.Fatalf("%s labels = %#v", id, obj.Labels())
		}
	}
	if got := store.Stats(); got != (StoreStats{Pending: 0, Committed: 100}) {
		t.Fatalf("Stats = %#v", got)
	}
}

func TestCreateHonorsCancellation(t *testing.T) {
	store := NewMemoryStore()
	host := newTestHost(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := host.Create(ctx, CreateRequest{ID: "doc-1", Kind: "document"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create = %v, want context.Canceled", err)
	}
	if got := store.Stats(); got != (StoreStats{}) {
		t.Fatalf("Stats = %#v", got)
	}
}

func TestNewHostValidation(t *testing.T) {
	if _, err := NewHost(nil); !errors.Is(err, ErrNilStore) {
		t.Fatalf("NewHost(nil) = %v, want ErrNilStore", err)
	}
	if _, err := NewHost(NewMemoryStore(), WithMandatoryLabels(Labels{"bad key": "v"})); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("NewHost with invalid mandatory labels = %v", err)
	}
	if _, err := NewHost(NewMemoryStore(), WithInheritedLabels(Labels{"bad key": "v"})); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("NewHost with invalid inherited labels = %v", err)
	}
	if _, err := NewHost(NewMemoryStore(), WithInheritor(nil)); err == nil {
		t.Fatal("NewHost with nil inheritor succeeded")
	}
	if _, err := NewHost(NewMemoryStore(), WithAuthorizer(nil)); err == nil {
		t.Fatal("NewHost with nil authorizer succeeded")
	}
}

func TestReadDoesNotReturnStagedObject(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	host := newTestHost(t, store,
		WithAuthorizer(AuthorizerFunc(func(context.Context, Access) (bool, error) { return true, nil })),
	)
	token, err := store.CreateObject(ctx, ObjectMeta{ID: "doc-1", Kind: "document"})
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	labels := Labels{"owner": "alice"}
	if err := store.PersistLabels(ctx, token, labels, labels.Digest()); err != nil {
		t.Fatalf("PersistLabels: %v", err)
	}
	if _, err := host.Read(ctx, "alice", "doc-1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Read staged object = %v, want ErrObjectNotFound", err)
	}
}
