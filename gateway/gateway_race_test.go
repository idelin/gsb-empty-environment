package gateway_test

import (
	"context"
	"errors"
	"testing"

	"prescreen.local/ps-59b347fb09/gateway"
	"prescreen.local/ps-59b347fb09/mock"
)

// 准备期间发生连接重建：旧连接上拿到的编号必须被作废，不允许残留为活语句，
// 后续执行应在新连接上重新准备并成功。
func TestPrepareDuringRebuildReconcilesStaleStatement(t *testing.T) {
	server := mock.NewServer()
	oldConn := mock.NewConnection(server, connA1, tenantA)
	newConn := mock.NewConnection(server, connA2, tenantA)
	g := gateway.New(oldConn, tenantA)

	release := make(chan struct{})
	blocked := make(chan struct{})
	server.PrepareGate = release
	server.PrepareGateConn = connA1
	server.PrepareBlocked = blocked

	type prepResult struct {
		entry *gateway.Entry
		err   error
	}
	resCh := make(chan prepResult, 1)
	go func() {
		e, err := g.Prepare(context.Background(), tenantA, tmpl, paramTypes)
		resCh <- prepResult{e, err}
	}()

	<-blocked
	g.Rebuild(newConn)
	close(release)

	pr := <-resCh
	if pr.err != nil {
		t.Fatalf("Prepare: %v", pr.err)
	}
	entry := pr.entry

	// 旧连接语句已作废（无法再关闭，直接废弃）；句柄当前无可用语句。
	if _, ok := entry.CurrentServerStmtID(); ok {
		t.Fatal("stale statement from rebuilt connection remained attached")
	}

	// 新连接上重新准备并执行成功。
	res, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 5))
	if err != nil {
		t.Fatalf("exec after reconcile: %v", err)
	}
	if res.Rows[0][1] != int64(5) {
		t.Fatalf("args wrong: %+v", res.Rows)
	}
	h := entry.Handle()
	if h.ConnID != connA2 || h.ConnGen != 2 {
		t.Fatalf("handle rebound wrong: %+v", h)
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
	// 新连接上的语句必须全部关闭；旧连接随重建消失，其编号无人能再回收。
	newConnLive := 0
	for _, c := range server.CallsOfKind("close") {
		if c.ConnID == connA2 {
			newConnLive++
		}
	}
	if newConnLive != 1 {
		t.Fatalf("new conn close count = %d, want 1 (no leak on current connection)", newConnLive)
	}
}

// 网关关闭后再 Prepare/Exec 必须返回 ErrClosed，而不是退化为其他执行路径。
func TestGatewayClosed(t *testing.T) {
	server := mock.NewServer()
	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)
	g.Close()

	if _, err := g.Prepare(context.Background(), tenantA, tmpl, paramTypes); !errors.Is(err, gateway.ErrClosed) {
		t.Fatalf("Prepare after close err = %v, want ErrClosed", err)
	}
	if _, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 1)); !errors.Is(err, gateway.ErrClosed) {
		t.Fatalf("Exec after gateway close err = %v, want ErrClosed", err)
	}
}
