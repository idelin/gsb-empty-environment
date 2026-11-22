package gateway_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"prescreen.local/ps-59b347fb09/gateway"
	"prescreen.local/ps-59b347fb09/mock"
)

// 场景 3：取消。ctx 取消立即返回，迟到回调被丢弃，不覆盖句柄。
func TestCancelDiscardsLateCallback(t *testing.T) {
	server := mock.NewServer()
	gate := make(chan struct{})
	server.ExecGate = gate
	server.GateOnce = true

	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)
	idBefore, _ := entry.CurrentServerStmtID()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := entry.Exec(ctx, tenantA, tmpl, argsFor(tenantA, 1))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not return promptly")
	}

	close(gate) // 迟到回调到达
	entry.WaitDrained()

	idAfter, _ := entry.CurrentServerStmtID()
	if idAfter != idBefore {
		t.Fatalf("late callback overwrote handle: id %d -> %d", idBefore, idAfter)
	}
	res, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 9))
	if err != nil {
		t.Fatalf("exec after cancel: %v", err)
	}
	if res.Rows[0][1] != int64(9) {
		t.Fatalf("post-cancel args wrong: %+v", res.Rows)
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
}

// 场景 4：参数数量/类型/序号错误。校验失败不得产生任何 exec，更不能拼接 SQL。
func TestArgumentCountAndTypeErrors(t *testing.T) {
	server := mock.NewServer()
	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)

	badCount := []gateway.NamedArg{
		{Ordinal: 1, Type: gateway.ParamString, Value: tenantA},
	}
	if _, err := entry.Exec(context.Background(), tenantA, tmpl, badCount); !errors.Is(err, gateway.ErrArgCount) {
		t.Fatalf("arg count err = %v, want ErrArgCount", err)
	}

	badType := []gateway.NamedArg{
		{Ordinal: 1, Type: gateway.ParamString, Value: tenantA},
		{Ordinal: 2, Type: gateway.ParamString, Value: "not-an-int"},
	}
	if _, err := entry.Exec(context.Background(), tenantA, tmpl, badType); !errors.Is(err, gateway.ErrArgType) {
		t.Fatalf("arg type err = %v, want ErrArgType", err)
	}

	badOrdinal := []gateway.NamedArg{
		{Ordinal: 2, Type: gateway.ParamString, Value: tenantA},
		{Ordinal: 1, Type: gateway.ParamInt64, Value: 1},
	}
	if _, err := entry.Exec(context.Background(), tenantA, tmpl, badOrdinal); !errors.Is(err, gateway.ErrArgType) {
		t.Fatalf("ordinal err = %v, want ErrArgType", err)
	}

	if n := len(server.CallsOfKind("exec")); n != 0 {
		t.Fatalf("validation failure produced %d exec calls, want 0", n)
	}

	if _, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 7)); err != nil {
		t.Fatalf("valid exec: %v", err)
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
}

// 场景 5：并发执行、重准备、关闭。旧语句等最后一个在途执行结束才关闭；
// 旧回调不能覆盖新句柄。
func TestConcurrentReprepareDrainAndStaleCallback(t *testing.T) {
	server := mock.NewServer()
	oldGate := make(chan struct{})
	retryGate := make(chan struct{})
	var gateMu sync.Mutex
	gates := []chan struct{}{oldGate, retryGate}
	var gateIdx int
	server.SetExecInterceptor(func(ctx context.Context) {
		gateMu.Lock()
		g := gates[gateIdx]
		if gateIdx < len(gates)-1 {
			gateIdx++
		}
		gateMu.Unlock()
		select {
		case <-g:
		case <-ctx.Done():
		}
	})

	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)
	oldID, _ := entry.CurrentServerStmtID()
	oldGen := entry.Generation()

	errCh := make(chan error, 1)
	go func() {
		_, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 1))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)

	if err := entry.Reprepare(context.Background()); err != nil {
		t.Fatalf("Reprepare: %v", err)
	}
	if server.LiveCount() != 2 {
		t.Fatalf("live while in-flight = %d, want 2 (old kept until drain)", server.LiveCount())
	}
	newID, _ := entry.CurrentServerStmtID()
	if newID == oldID || entry.Generation() != oldGen+1 {
		t.Fatalf("handle not switched: newID=%d oldID=%d gen=%d", newID, oldID, entry.Generation())
	}

	// 放行旧执行：其迟到回调因语句指针判旧被丢弃，Exec 自动在新语句上重试，
	// 重试被第二道门控拦住，证明执行确实切到了新语句。
	close(oldGate)
	time.Sleep(50 * time.Millisecond)
	waitForServerInflight(t, server, 1) // 旧执行结束，仅剩被第二道门控拦住的重试

	// 旧语句排空后被关闭，新语句仍存活。
	if server.LiveCount() != 1 {
		t.Fatalf("live after old drain = %d, want 1 (new statement)", server.LiveCount())
	}
	if cur, _ := entry.CurrentServerStmtID(); cur != newID {
		t.Fatalf("stale callback overwrote new handle: cur=%d want=%d", cur, newID)
	}

	// 放行重试，Exec 成功返回，参数归属到新语句。
	close(retryGate)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("auto-retry exec err = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("auto-retry exec never returned")
	}

	res, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 3))
	if err != nil {
		t.Fatalf("exec on new statement: %v", err)
	}
	if res.Rows[0][1] != int64(3) {
		t.Fatalf("new statement args wrong: %+v", res.Rows)
	}
	for _, c := range server.CallsOfKind("exec") {
		if c.ServerID == newID {
			if c.Args[0].Value != tenantA || c.Template != tmpl {
				t.Fatalf("new statement exec attribution wrong: %+v", c)
			}
			if v := c.Args[1].Value; v != int64(1) && v != int64(3) {
				t.Fatalf("unexpected arg on new statement: %+v", c)
			}
		}
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
	if server.LiveCount() != 0 {
		t.Fatalf("live after Close = %d, want 0", server.LiveCount())
	}
	if err := entry.Close(); !errors.Is(err, gateway.ErrClosed) {
		t.Fatalf("double Close err = %v, want ErrClosed", err)
	}
}

// 高并发下关闭与执行交错：服务端关闭必须严格排在在途执行排空之后，
// 且每次 close 调用都对应一次先 prepare，编号归属到正确连接。
func TestConcurrentExecAndCloseInvariant(t *testing.T) {
	server := mock.NewServer()
	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := mustPrepare(t, g, tenantA, tmpl)
			_, _ = entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, int64(i)))
			_ = entry.Close()
		}(i)
	}
	wg.Wait()

	if server.LiveCount() != 0 {
		t.Fatalf("live statements = %d, want 0", server.LiveCount())
	}
	prepares := server.CallsOfKind("prepare")
	closes := server.CallsOfKind("close")
	if len(prepares) != n || len(closes) != n {
		t.Fatalf("prepare=%d close=%d, want %d each", len(prepares), len(closes), n)
	}
	// 关闭编号必须能对应到同连接上 prepare 出的编号。
	preparedIDs := map[uint64]bool{}
	for _, p := range prepares {
		preparedIDs[p.ServerID] = true
	}
	for _, c := range closes {
		if !preparedIDs[c.ServerID] {
			t.Fatalf("close of unprepared server id %d", c.ServerID)
		}
	}
}

// 身份与模板绑定：错误租户或被篡改的模板必须被拒绝，且不产生 exec。
func TestIdentityAndTemplateBinding(t *testing.T) {
	server := mock.NewServer()
	conn := mock.NewConnection(server, connA1, tenantA)
	g := gateway.New(conn, tenantA)
	entry := mustPrepare(t, g, tenantA, tmpl)

	if _, err := entry.Exec(context.Background(), tenantB, tmpl, argsFor(tenantB, 1)); !errors.Is(err, gateway.ErrIdentityMismatch) {
		t.Fatalf("tenant err = %v, want ErrIdentityMismatch", err)
	}
	tampered := "SELECT id FROM accounts WHERE tenant = $1 AND balance > $2 -- injected"
	if _, err := entry.Exec(context.Background(), tenantA, tampered, argsFor(tenantA, 1)); !errors.Is(err, gateway.ErrTemplateMismatch) {
		t.Fatalf("template err = %v, want ErrTemplateMismatch", err)
	}
	if n := len(server.CallsOfKind("exec")); n != 0 {
		t.Fatalf("binding check produced %d exec, want 0", n)
	}

	// 正确身份/模板正常执行。
	res, err := entry.Exec(context.Background(), tenantA, tmpl, argsFor(tenantA, 42))
	if err != nil {
		t.Fatalf("valid exec: %v", err)
	}
	last := server.CallsOfKind("exec")
	c := last[len(last)-1]
	if c.DatabaseID != tenantA || c.Args[0].Value != tenantA || res.Rows[0][1] != int64(42) {
		t.Fatalf("attribution wrong: call=%+v res=%+v", c, res.Rows)
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
}
