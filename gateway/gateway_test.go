package gateway_test

import (
	"context"
	"testing"
	"time"

	"prescreen.local/ps-59b347fb09/gateway"
	"prescreen.local/ps-59b347fb09/mock"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
	connA1  = "conn-A-1"
	connA2  = "conn-A-2"
	tmpl    = "SELECT id FROM accounts WHERE tenant = $1 AND balance > $2"
)

var paramTypes = []gateway.ParamType{gateway.ParamString, gateway.ParamInt64}

func argsFor(tenant string, balance int64) []gateway.NamedArg {
	return []gateway.NamedArg{
		{Ordinal: 1, Type: gateway.ParamString, Value: tenant},
		{Ordinal: 2, Type: gateway.ParamInt64, Value: balance},
	}
}

func mustPrepare(t *testing.T, g *gateway.Gateway, db, template string) *gateway.Entry {
	t.Helper()
	entry, err := g.Prepare(context.Background(), db, template, paramTypes)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return entry
}

func waitForServerInflight(t *testing.T, s *mock.Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.ServerInflight() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server inflight = %d, want %d", s.ServerInflight(), want)
}

// 两条物理连接都从编号 1 开始分配服务端语句编号（故意相同）。
func TestTwoConnectionsReturnSameServerID(t *testing.T) {
	server := mock.NewServer()
	c1 := mock.NewConnection(server, connA1, tenantA)
	c2 := mock.NewConnection(server, connA2, tenantA)

	g1 := gateway.New(c1, tenantA)
	e1 := mustPrepare(t, g1, tenantA, tmpl)
	id1, ok := e1.CurrentServerStmtID()
	if !ok || id1 != 1 {
		t.Fatalf("first conn server id = %d (ok=%v), want 1", id1, ok)
	}

	g2 := gateway.New(c2, tenantA)
	e2 := mustPrepare(t, g2, tenantA, tmpl)
	id2, _ := e2.CurrentServerStmtID()
	if id2 != 1 {
		t.Fatalf("second conn server id = %d, want 1 (identical IDs on purpose)", id2)
	}
	if err := e1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e2.Close(); err != nil {
		t.Fatal(err)
	}
}

// 场景 1：连接重建。旧连接在途执行被作废，新连接重新准备，
// 相同编号也绝不串到另一物理连接；核对模板与参数归属。
func TestConnectionRebuild(t *testing.T) {
	server := mock.NewServer()
	gate := make(chan struct{})
	server.ExecGate = gate
	server.GateOnce = true

	oldConn := mock.NewConnection(server, connA1, tenantA)
	newConn := mock.NewConnection(server, connA2, tenantA)
	g := gateway.New(oldConn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)

	if gen := g.CurrentGeneration(); gen != 1 {
		t.Fatalf("initial generation = %d, want 1", gen)
	}
	h := entry.Handle()
	if h.DatabaseID != tenantA || h.Digest != gateway.DigestTemplate(tmpl) ||
		h.ConnID != connA1 || h.ConnGen != 1 {
		t.Fatalf("handle binding wrong: %+v", h)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 100))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)

	g.Rebuild(newConn)
	if gen := g.CurrentGeneration(); gen != 2 {
		t.Fatalf("generation after rebuild = %d, want 2", gen)
	}

	close(gate)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("in-flight exec after rebuild = %v, want nil (re-prepare + retry)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight exec never returned after rebuild")
	}

	// 在途执行被作废后，Exec 在新连接上自动重准备并重试成功，参数原样归属。
	var post []mock.Call
	for _, c := range server.CallsOfKind("exec") {
		if c.ConnID == connA2 {
			post = append(post, c)
		}
	}
	if len(post) != 1 {
		t.Fatalf("post-rebuild exec on new conn = %d, want 1 (auto re-prepare + retry)", len(post))
	}
	pe := post[0]
	if pe.Template != tmpl || pe.ServerID != 1 || len(pe.Args) != 2 ||
		pe.Args[0].Value != tenantA || pe.Args[1].Value != int64(100) {
		t.Fatalf("retried exec template/arg attribution wrong: %+v", pe)
	}

	// 后续执行复用新连接上的语句。
	res, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 200))
	if err != nil {
		t.Fatalf("exec after rebuild: %v", err)
	}
	if res.Rows[0][0] != tenantA || res.Rows[0][1] != int64(200) {
		t.Fatalf("bound args wrong after rebuild: %+v", res.Rows)
	}

	id, _ := entry.CurrentServerStmtID()
	if id != 1 {
		t.Fatalf("new connection server id = %d, want 1 (same number, different connection)", id)
	}
	h = entry.Handle()
	if h.ConnID != connA2 || h.ConnGen != 2 {
		t.Fatalf("handle rebound wrong: %+v", h)
	}

	post = post[:0]
	for _, c := range server.CallsOfKind("exec") {
		if c.ConnID == connA2 {
			post = append(post, c)
		}
	}
	if len(post) != 2 {
		t.Fatalf("new conn exec count = %d, want 2", len(post))
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
}

// 场景 2：模式变化。服务端编号失效后重准备并自动重试一次，
// 全程只走 prepare/exec 结构化路径。
func TestSchemaChangeReprepareAndRetry(t *testing.T) {
	server := mock.NewServer()
	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)
	initialGen := entry.Generation()

	server.InjectUnknownStatement(func(c mock.Call) bool {
		return c.Kind == "exec" && c.ConnID == connA1
	}, "schema changed: statement invalidated")

	res, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 50))
	if err != nil {
		t.Fatalf("exec after schema change: %v", err)
	}
	if entry.Generation() != initialGen+1 {
		t.Fatalf("generation = %d, want %d", entry.Generation(), initialGen+1)
	}
	if res.Rows[0][1] != int64(50) {
		t.Fatalf("retry args wrong: %+v", res.Rows)
	}

	if n := len(server.CallsOfKind("prepare")); n != 2 {
		t.Fatalf("prepare count = %d, want 2", n)
	}
	execs := server.CallsOfKind("exec")
	if len(execs) != 2 {
		t.Fatalf("exec count = %d, want 2 (failed + retried)", len(execs))
	}
	for _, c := range execs {
		if c.Template != tmpl || len(c.Args) != 2 {
			t.Fatalf("exec without structured binding: %+v", c)
		}
	}
	if server.LiveCount() != 1 {
		t.Fatalf("live statements = %d, want 1 after old drained", server.LiveCount())
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
	if server.LiveCount() != 0 {
		t.Fatalf("live after Close = %d, want 0", server.LiveCount())
	}
}
