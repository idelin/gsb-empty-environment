package preview

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"example.org/netsec/q-019/budget"
)

type mockStore struct {
	mu        sync.Mutex
	renders   int
	entries   []Entry
	renderErr error
}

func (m *mockStore) Render(_ context.Context, entries []Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.renderErr != nil {
		return m.renderErr
	}
	m.renders++
	m.entries = entries
	return nil
}

func (m *mockStore) renderCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renders
}

func leaf(name string, payload []byte) *budget.Node {
	data := append([]byte(nil), payload...)
	return &budget.Node{
		Name:   name,
		Decode: func(_ context.Context, w io.Writer) error { _, err := w.Write(data); return err },
	}
}

func limits() budget.Limits {
	return budget.Limits{MaxTotalBytes: 64, MaxDepth: 4, MaxLeaves: 16, MaxLiveBuffers: 2}
}

func TestPreviewRendersOnFullSuccess(t *testing.T) {
	store := &mockStore{}
	svc, err := NewService(limits(), store)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	tree := &budget.Node{
		Name: "attach",
		Children: []*budget.Node{
			leaf("a", []byte("alpha")),
			{Name: "dir", Children: []*budget.Node{leaf("b", []byte("beta"))}},
		},
	}

	entries, err := svc.Preview(context.Background(), tree)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if store.renderCount() != 1 {
		t.Fatalf("render count = %d, want 1", store.renderCount())
	}
	if len(entries) != 2 || string(entries[1].Payload) != "beta" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestPreviewAttackRendersNothing(t *testing.T) {
	store := &mockStore{}
	svc, err := NewService(limits(), store)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Hostile node lies about size and streams far more than the byte budget.
	attack := &budget.Node{
		Name:         "evil",
		DeclaredSize: 1,
		Decode: func(_ context.Context, w io.Writer) error {
			chunk := make([]byte, 32)
			for {
				if _, werr := w.Write(chunk); werr != nil {
					return werr
				}
			}
		},
	}

	entries, err := svc.Preview(context.Background(), attack)
	if !errors.Is(err, budget.ErrTotalBytesExceeded) {
		t.Fatalf("err = %v, want ErrTotalBytesExceeded", err)
	}
	if entries != nil {
		t.Fatalf("entries must be nil on failure, got %+v", entries)
	}
	if store.renderCount() != 0 {
		t.Fatalf("render count = %d, want 0 (no partial publish)", store.renderCount())
	}
}

func TestPreviewPropagatesRenderFailure(t *testing.T) {
	renderErr := errors.New("store unavailable")
	store := &mockStore{renderErr: renderErr}
	svc, _ := NewService(limits(), store)

	_, err := svc.Preview(context.Background(), leaf("a", []byte("x")))
	if !errors.Is(err, renderErr) {
		t.Fatalf("err = %v, want render error", err)
	}
}

func TestNewServiceRejectsBadConfig(t *testing.T) {
	if _, err := NewService(limits(), nil); !errors.Is(err, budget.ErrInvalidConfig) {
		t.Fatalf("nil store: err = %v", err)
	}
	bad := limits()
	bad.MaxLiveBuffers = 0
	if _, err := NewService(bad, &mockStore{}); !errors.Is(err, budget.ErrInvalidConfig) {
		t.Fatalf("bad limits: err = %v", err)
	}
}
