package connauthz

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
)

func netPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() {
		a.Close()
		b.Close()
	})
	return a, b
}

type fakeStream struct {
	id     int
	closed atomic.Bool
}

func (s *fakeStream) Close() error {
	s.closed.Store(true)
	return nil
}

type fakeMux struct {
	opens atomic.Int64
}

func (m *fakeMux) OpenStream(ctx context.Context) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := int(m.opens.Add(1))
	return &fakeStream{id: id}, nil
}

func TestAuthorizedMux_GatesOpenStreamAndKeepsOldStreams(t *testing.T) {
	pki := buildPKI(t)
	policy1, err := NewPolicyBuilder(1).
		AddRoot(pki.rootA).
		AddRoot(pki.rootB).
		AddIntermediate(pki.crossAbyB).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	authz, err := NewAuthorizer(policy1)
	if err != nil {
		t.Fatal(err)
	}

	recorded := [][]byte{pki.leaf.Raw, pki.inter.Raw}
	conn := &Connection{rawChain: recorded}
	mux := &fakeMux{}
	gated := NewAuthorizedMux(mux, authz, conn)

	// 第一代：开流成功，旧流保持打开。
	stream1, err := gated.OpenStream(context.Background())
	if err != nil {
		t.Fatalf("gen1 open stream: %v", err)
	}
	old := stream1.(*fakeStream)
	if got := mux.opens.Load(); got != 1 {
		t.Fatalf("underlying mux opens = %d, want 1", got)
	}

	// 第二代：撤销叶子证书。
	policy2, err := NewPolicyBuilder(2).
		AddRoot(pki.rootA).
		AddRoot(pki.rootB).
		AddIntermediate(pki.crossAbyB).
		Revoke(pki.leaf).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := authz.Rotate(policy2); err != nil {
		t.Fatalf("rotate gen2: %v", err)
	}

	_, err = gated.OpenStream(context.Background())
	if err == nil {
		t.Fatal("revoked leaf must be denied on new stream")
	}
	var denial *DenialError
	if !errors.As(err, &denial) {
		t.Fatalf("want *DenialError, got %T: %v", err, err)
	}
	if denial.Generation != 2 || !errors.Is(err, ErrRevoked) {
		t.Fatalf("denial = %v, want gen2 ErrRevoked", err)
	}
	if got := mux.opens.Load(); got != 1 {
		t.Fatalf("denied open must not touch underlying mux, opens = %d", got)
	}

	// 旧流按既有决定继续存在，不被追溯关闭。
	if old.closed.Load() {
		t.Fatal("existing stream must not be closed by rotation")
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close old stream: %v", err)
	}

	// 第三代：解除撤销，开流恢复。
	policy3, err := NewPolicyBuilder(3).
		AddRoot(pki.rootA).
		AddRoot(pki.rootB).
		AddIntermediate(pki.crossAbyB).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := authz.Rotate(policy3); err != nil {
		t.Fatalf("rotate gen3: %v", err)
	}
	if _, err := gated.OpenStream(context.Background()); err != nil {
		t.Fatalf("gen3 open stream should be restored: %v", err)
	}
}

func TestRotate_RejectsStaleGeneration(t *testing.T) {
	pki := buildPKI(t)
	policy1, _ := NewPolicyBuilder(1).AddRoot(pki.rootA).Build()
	authz, err := NewAuthorizer(policy1)
	if err != nil {
		t.Fatal(err)
	}

	stale, _ := NewPolicyBuilder(1).AddRoot(pki.rootA).Build()
	err = authz.Rotate(stale)
	if err == nil {
		t.Fatal("stale rotation must be rejected")
	}
	if authz.Current().Generation() != 1 {
		t.Fatalf("current generation = %d, want unchanged 1", authz.Current().Generation())
	}

	older := NewPolicyBuilder(0)
	if _, err := older.Build(); err == nil {
		t.Fatal("generation 0 policy must be rejected")
	}
}
