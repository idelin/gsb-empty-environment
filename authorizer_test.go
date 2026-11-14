package netsec

import (
	"errors"
	"testing"
	"time"
)

const sessionTTL = time.Hour

// newServerWithDoc 构建带一个文档的测试环境，返回服务端与 owner 会话。
func newServerWithDoc(t *testing.T, docID, owner, initial string, opts ...Option) (*Server, *Authorizer, Session) {
	t.Helper()
	auth := NewAuthorizer(opts...)
	srv := NewServer(auth)
	if err := srv.CreateDocument(docID, owner, initial); err != nil {
		t.Fatalf("create document: %v", err)
	}
	sess, err := auth.IssueSession("sess-"+owner, docID, owner, sessionTTL)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	return srv, auth, sess
}

func mustSubmit(t *testing.T, srv *Server, op Operation) uint64 {
	t.Helper()
	v, err := srv.Submit(op)
	if err != nil {
		t.Fatalf("submit op %q: %v", op.ID, err)
	}
	return v
}

// TestReplayRejected 验证旧操作重放无法把已删除的敏感段落合回文档。
func TestReplayRejected(t *testing.T) {
	srv, auth, sess := newServerWithDoc(t, "doc1", "alice", "", WithMaxLag(1))

	insertSecret := Operation{
		ID: "op-1", DocID: "doc1", SessionID: sess.ID, SubjectID: "alice",
		BaseVersion: 0, Type: OpInsert, Position: 0, Text: "SECRET ",
	}
	mustSubmit(t, srv, insertSecret)
	mustSubmit(t, srv, Operation{
		ID: "op-2", DocID: "doc1", SessionID: sess.ID, SubjectID: "alice",
		BaseVersion: 1, Type: OpInsert, Position: 7, Text: "public",
	})
	mustSubmit(t, srv, Operation{
		ID: "op-3", DocID: "doc1", SessionID: sess.ID, SubjectID: "alice",
		BaseVersion: 2, Type: OpDelete, Position: 0, Length: 7,
	})
	if got := srv.Content("doc1"); got != "public" {
		t.Fatalf("content after delete = %q, want %q", got, "public")
	}

	// 原样重放已应用的插入操作：操作 ID 重复，拒绝。
	if _, err := srv.Submit(insertSecret); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay same op: err = %v, want ErrReplay", err)
	}
	// 换一个新操作 ID 但基于超出滞后窗口的旧版本：拒绝。
	stale := insertSecret
	stale.ID = "op-evil"
	stale.BaseVersion = 1 // 当前版本 3，maxLag 1
	if _, err := srv.Submit(stale); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale base version: err = %v, want ErrStaleVersion", err)
	}
	// 来自未来的版本同样拒绝。
	future := insertSecret
	future.ID = "op-future"
	future.BaseVersion = 99
	if _, err := srv.Submit(future); !errors.Is(err, ErrFutureVersion) {
		t.Fatalf("future base version: err = %v, want ErrFutureVersion", err)
	}

	if srv.Contains("doc1", "SECRET") {
		t.Fatal("sensitive paragraph was merged back via replay")
	}
	if v, _ := auth.Version("doc1"); v != 3 {
		t.Fatalf("version = %d, want 3 (rejected ops must not advance version)", v)
	}
}

// TestCrossDocumentRejected 验证会话绑定文档之外的操作被拒绝。
func TestCrossDocumentRejected(t *testing.T) {
	srv, auth, sessA := newServerWithDoc(t, "docA", "alice", "alpha")
	if err := srv.CreateDocument("docB", "alice", "beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.IssueSession("sess-b", "docB", "alice", sessionTTL); err != nil {
		t.Fatal(err)
	}

	// 用 docA 的会话向 docB 提交操作。
	cross := Operation{
		ID: "x-1", DocID: "docB", SessionID: sessA.ID, SubjectID: "alice",
		BaseVersion: 0, Type: OpInsert, Position: 0, Text: "HACK",
	}
	if _, err := srv.Submit(cross); !errors.Is(err, ErrCrossDocument) {
		t.Fatalf("cross-document op: err = %v, want ErrCrossDocument", err)
	}
	if got := srv.Content("docB"); got != "beta" {
		t.Fatalf("docB content = %q, want unchanged %q", got, "beta")
	}
	// 跨文档读取历史同样拒绝。
	if _, err := srv.History("docB", sessA.ID); !errors.Is(err, ErrCrossDocument) {
		t.Fatalf("cross-document history: err = %v, want ErrCrossDocument", err)
	}
}

// TestForgedIdentityRejected 验证伪造客户端标识与越权编辑被拒绝。
func TestForgedIdentityRejected(t *testing.T) {
	srv, auth, sess := newServerWithDoc(t, "doc1", "alice", "data")
	if err := auth.Grant("doc1", "bob", RoleEditor); err != nil {
		t.Fatal(err)
	}
	if err := auth.Grant("doc1", "carol", RoleViewer); err != nil {
		t.Fatal(err)
	}

	// 盗用 alice 的会话，但把操作主体伪造成 bob。
	forged := Operation{
		ID: "f-1", DocID: "doc1", SessionID: sess.ID, SubjectID: "bob",
		BaseVersion: 0, Type: OpInsert, Position: 0, Text: "HACK",
	}
	if _, err := srv.Submit(forged); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("forged subject: err = %v, want ErrIdentityMismatch", err)
	}
	// 没有会话的攻击者直接提交。
	noSess := forged
	noSess.ID = "f-2"
	noSess.SessionID = "sess-nonexistent"
	noSess.SubjectID = "alice"
	if _, err := srv.Submit(noSess); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("no session: err = %v, want ErrUnknownSession", err)
	}
	// viewer 只有读权限，编辑被拒绝。
	viewerSess, err := auth.IssueSession("sess-carol", "doc1", "carol", sessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	viewerOp := Operation{
		ID: "f-3", DocID: "doc1", SessionID: viewerSess.ID, SubjectID: "carol",
		BaseVersion: 0, Type: OpInsert, Position: 0, Text: "HACK",
	}
	if _, err := srv.Submit(viewerOp); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("viewer edit: err = %v, want ErrPermissionDenied", err)
	}
	// 无任何角色的主体连会话都拿不到。
	if _, err := auth.IssueSession("sess-mallory", "doc1", "mallory", sessionTTL); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("issue session to stranger: err = %v, want ErrPermissionDenied", err)
	}
	if got := srv.Content("doc1"); got != "data" {
		t.Fatalf("content = %q, want unchanged %q", got, "data")
	}
}

// TestLegitimateMerge 验证合法并发操作经授权与变换后正确合并。
func TestLegitimateMerge(t *testing.T) {
	srv, auth, aliceSess := newServerWithDoc(t, "doc1", "alice", "hello world")
	if err := auth.Grant("doc1", "bob", RoleEditor); err != nil {
		t.Fatal(err)
	}
	bobSess, err := auth.IssueSession("sess-bob", "doc1", "bob", sessionTTL)
	if err != nil {
		t.Fatal(err)
	}

	// alice 与 bob 基于同一版本 v0 并发编辑。
	mustSubmit(t, srv, Operation{
		ID: "a-1", DocID: "doc1", SessionID: aliceSess.ID, SubjectID: "alice",
		BaseVersion: 0, Type: OpInsert, Position: 0, Text: "X",
	})
	// bob 的操作基于 v0，服务端先授权（滞后 1 在窗口内），再变换位置 6 -> 7。
	v, err := srv.Submit(Operation{
		ID: "b-1", DocID: "doc1", SessionID: bobSess.ID, SubjectID: "bob",
		BaseVersion: 0, Type: OpInsert, Position: 6, Text: "Y",
	})
	if err != nil {
		t.Fatalf("concurrent op rejected: %v", err)
	}
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	if got := srv.Content("doc1"); got != "Xhello Yworld" {
		t.Fatalf("merged content = %q, want %q", got, "Xhello Yworld")
	}
	// 历史按版本记录两条操作及其真实主体。
	history, err := srv.History("doc1", aliceSess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].SubjectID != "alice" || history[1].SubjectID != "bob" {
		t.Fatalf("unexpected history: %+v", history)
	}
}

// TestRollbackAuthorization 验证回滚与历史读取均需授权。
func TestRollbackAuthorization(t *testing.T) {
	srv, auth, ownerSess := newServerWithDoc(t, "doc1", "alice", "abc SECRET def")
	if err := auth.Grant("doc1", "bob", RoleEditor); err != nil {
		t.Fatal(err)
	}
	if err := auth.Grant("doc1", "carol", RoleViewer); err != nil {
		t.Fatal(err)
	}
	bobSess, _ := auth.IssueSession("sess-bob", "doc1", "bob", sessionTTL)
	carolSess, _ := auth.IssueSession("sess-carol", "doc1", "carol", sessionTTL)

	// bob 合法删除敏感段落。
	mustSubmit(t, srv, Operation{
		ID: "d-1", DocID: "doc1", SessionID: bobSess.ID, SubjectID: "bob",
		BaseVersion: 0, Type: OpDelete, Position: 4, Length: 7,
	})
	if got := srv.Content("doc1"); got != "abc def" {
		t.Fatalf("content = %q, want %q", got, "abc def")
	}

	// editor 无权回滚，viewer 也无权回滚。
	if err := srv.Rollback("doc1", bobSess.ID, 0); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("editor rollback: err = %v, want ErrPermissionDenied", err)
	}
	if err := srv.Rollback("doc1", carolSess.ID, 0); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("viewer rollback: err = %v, want ErrPermissionDenied", err)
	}
	// viewer 可以读历史，但无角色主体不能。
	if _, err := srv.History("doc1", carolSess.ID); err != nil {
		t.Fatalf("viewer history: %v", err)
	}
	// 回滚目标版本不能超过当前版本。
	if err := srv.Rollback("doc1", ownerSess.ID, 99); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("rollback beyond current: err = %v, want ErrInvalidOperation", err)
	}
	// 未授权回滚不得改变内容。
	if got := srv.Content("doc1"); got != "abc def" {
		t.Fatalf("content after denied rollback = %q, want %q", got, "abc def")
	}

	// owner 授权回滚到 v0，敏感段落按授权恢复。
	if err := srv.Rollback("doc1", ownerSess.ID, 0); err != nil {
		t.Fatalf("owner rollback: %v", err)
	}
	if got := srv.Content("doc1"); got != "abc SECRET def" {
		t.Fatalf("content after rollback = %q, want %q", got, "abc SECRET def")
	}
	if v, _ := auth.Version("doc1"); v != 0 {
		t.Fatalf("version after rollback = %d, want 0", v)
	}
	// 回滚后基于当前版本的新操作仍可授权。
	mustSubmit(t, srv, Operation{
		ID: "d-2", DocID: "doc1", SessionID: ownerSess.ID, SubjectID: "alice",
		BaseVersion: 0, Type: OpDelete, Position: 4, Length: 7,
	})
	if srv.Contains("doc1", "SECRET") {
		t.Fatal("sensitive paragraph present after authorized re-delete")
	}
}

// TestSessionExpiry 验证过期会话被拒绝。
func TestSessionExpiry(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	srv, _, sess := newServerWithDoc(t, "doc1", "alice", "data", WithClock(clock))

	now = now.Add(2 * sessionTTL)
	op := Operation{
		ID: "e-1", DocID: "doc1", SessionID: sess.ID, SubjectID: "alice",
		BaseVersion: 0, Type: OpInsert, Position: 0, Text: "HACK",
	}
	if _, err := srv.Submit(op); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired session: err = %v, want ErrSessionExpired", err)
	}
	if got := srv.Content("doc1"); got != "data" {
		t.Fatalf("content = %q, want unchanged %q", got, "data")
	}
}
