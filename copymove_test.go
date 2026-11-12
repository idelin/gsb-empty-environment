package netsec

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// fakeFS 是一个内存树，实现 FileSystem。
type fakeFS struct {
	nodes map[string]Member
}

func newFakeFS() *fakeFS { return &fakeFS{nodes: map[string]Member{}} }

func (f *fakeFS) add(m Member) { f.nodes[m.Path] = m }

func (f *fakeFS) Stat(_ context.Context, p string) (Member, error) {
	if m, ok := f.nodes[p]; ok {
		return m, nil
	}
	return Member{}, ErrNotExist
}

func (f *fakeFS) Walk(_ context.Context, p string) ([]Member, error) {
	if _, ok := f.nodes[p]; !ok {
		return nil, ErrNotExist
	}
	var out []Member
	for path, m := range f.nodes {
		if path == p || strings.HasPrefix(path, p+"/") {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *fakeFS) Copy(_ context.Context, src, dst string) error {
	m, ok := f.nodes[src]
	if !ok {
		return ErrNotExist
	}
	f.nodes[dst] = Member{Path: dst, Collection: m.Collection, Symlink: m.Symlink}
	return nil
}

func (f *fakeFS) Remove(_ context.Context, p string) error {
	for path := range f.nodes {
		if path == p || strings.HasPrefix(path, p+"/") {
			delete(f.nodes, path)
		}
	}
	return nil
}

func (f *fakeFS) has(p string) bool { _, ok := f.nodes[p]; return ok }

// ruleAuth 按路径前缀规则放行，默认全部允许。
type ruleAuth struct {
	noRead   map[string]bool
	noWrite  map[string]bool
	noDelete map[string]bool
}

func (a ruleAuth) CanRead(_ context.Context, _, p string) bool   { return !a.noRead[p] }
func (a ruleAuth) CanWrite(_ context.Context, _, p string) bool  { return !a.noWrite[p] }
func (a ruleAuth) CanDelete(_ context.Context, _, p string) bool { return !a.noDelete[p] }

const (
	tenant = "acme"
	root   = "/tenants/acme"
	host   = "dav.example.com"
)

func newEngine(t *testing.T, fs FileSystem, auth Authorizer, mode FailureMode) *Engine {
	t.Helper()
	e, err := NewEngine(auth, fs, Options{Tenant: tenant, TenantRoot: root, OnPartialFailure: mode})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// buildTree 构造 /tenants/acme/src/{a.txt, sub/, sub/b.txt, link}。
func buildTree() *fakeFS {
	fs := newFakeFS()
	fs.add(Member{Path: root, Collection: true})
	fs.add(Member{Path: root + "/src", Collection: true})
	fs.add(Member{Path: root + "/src/a.txt"})
	fs.add(Member{Path: root + "/src/sub", Collection: true})
	fs.add(Member{Path: root + "/src/sub/b.txt"})
	fs.add(Member{Path: root + "/dst", Collection: true})
	return fs
}

func copyReq(dest string) Request {
	return Request{
		Method:      MethodCopy,
		Host:        host,
		Source:      root + "/src",
		Destination: dest,
		Depth:       "infinity",
		Overwrite:   true,
	}
}

func TestDestinationTraversalAndHost(t *testing.T) {
	cases := []struct {
		name string
		dest string
		want int // 期望整体状态码
	}{
		{"percent encoded dotdot", root + "/dst/%2e%2e/evil", 400},
		{"uppercase encoded dotdot", root + "/dst/%2E%2E/evil", 400},
		{"mixed dotdot", root + "/dst/.%2e/evil", 400},
		{"encoded slash dotdot", root + "/dst%2f..%2fevil", 400},
		{"plain dotdot", root + "/dst/../evil", 400},
		{"escapes tenant", "/tenants/bob/dst", 403},
		{"tenant prefix lookalike", "/tenants/acme-evil/dst", 403},
		{"cross host absolute", "https://evil.example.com" + root + "/dst/x", 403},
		{"cross host other tenant", "https://evil.example.com/tenants/bob/x", 403},
		{"backslash", root + `/dst\..\evil`, 400},
		{"ok absolute same host", "https://dav.example.com" + root + "/dst/x", 201},
		{"ok absolute default port", "https://dav.example.com:443" + root + "/dst/y", 201},
		{"ok relative", root + "/dst/z", 201},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := buildTree()
			e := newEngine(t, fs, ruleAuth{}, KeepCopied)
			res, err := e.CopyOrMove(context.Background(), copyReq(tc.dest))
			if err != nil {
				t.Fatalf("CopyOrMove: %v", err)
			}
			if res.Status != tc.want {
				t.Fatalf("status = %d, want %d", res.Status, tc.want)
			}
		})
	}
}

func TestUnauthorizedChildKeepCopied(t *testing.T) {
	fs := buildTree()
	auth := ruleAuth{noRead: map[string]bool{root + "/src/sub": true}}
	e := newEngine(t, fs, auth, KeepCopied)

	res, err := e.CopyOrMove(context.Background(), copyReq(root+"/dst/out"))
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 207 {
		t.Fatalf("status = %d, want 207", res.Status)
	}
	// 无权集合及其后代均未复制，其余成员保留。
	if !fs.has(root+"/dst/out") || !fs.has(root+"/dst/out/a.txt") {
		t.Fatalf("expected copied members kept, got %v", fs.nodes)
	}
	if fs.has(root+"/dst/out/sub") || fs.has(root+"/dst/out/sub/b.txt") {
		t.Fatalf("denied subtree must not be copied")
	}
	// 207 中逐项 403，且子项不重复列出。
	if len(res.Items) != 1 || res.Items[0].Href != root+"/src/sub" || res.Items[0].Status != 403 {
		t.Fatalf("items = %+v, want single 403 for denied collection", res.Items)
	}
	body, err := res.MarshalMultistatus()
	if err != nil {
		t.Fatalf("MarshalMultistatus: %v", err)
	}
	if !strings.Contains(string(body), "403 Forbidden") || !strings.Contains(string(body), root+"/src/sub") {
		t.Fatalf("multistatus body missing 403 entry:\n%s", body)
	}
}

func TestUnauthorizedChildRollbackAll(t *testing.T) {
	fs := buildTree()
	auth := ruleAuth{noRead: map[string]bool{root + "/src/sub/b.txt": true}}
	e := newEngine(t, fs, auth, RollbackAll)

	res, err := e.CopyOrMove(context.Background(), copyReq(root+"/dst/out"))
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 403 || !res.RolledBack {
		t.Fatalf("status = %d rolledBack = %v, want 403/true", res.Status, res.RolledBack)
	}
	if fs.has(root + "/dst/out") {
		t.Fatalf("rollback must remove all copied members, got %v", fs.nodes)
	}
	// 源保持不动。
	if !fs.has(root+"/src/sub/b.txt") || !fs.has(root+"/src/a.txt") {
		t.Fatalf("source tree must be untouched")
	}
}

func TestOverwriteFalse(t *testing.T) {
	fs := buildTree()
	fs.add(Member{Path: root + "/dst/out", Collection: true})
	e := newEngine(t, fs, ruleAuth{}, KeepCopied)

	req := copyReq(root + "/dst/out")
	req.Overwrite = false // Overwrite: F
	res, err := e.CopyOrMove(context.Background(), req)
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 412 {
		t.Fatalf("status = %d, want 412", res.Status)
	}
	if fs.has(root + "/dst/out/a.txt") {
		t.Fatalf("nothing may be copied when Overwrite: F and dest exists")
	}

	// Overwrite: T 覆盖成功，返回 204。
	req.Overwrite = true
	res, err = e.CopyOrMove(context.Background(), req)
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 204 {
		t.Fatalf("status = %d, want 204", res.Status)
	}
	if !fs.has(root + "/dst/out/a.txt") {
		t.Fatalf("overwrite copy must populate destination")
	}
}

func TestSymlinkMemberRejected(t *testing.T) {
	fs := buildTree()
	fs.add(Member{Path: root + "/src/link", Symlink: true})
	e := newEngine(t, fs, ruleAuth{}, KeepCopied)

	res, err := e.CopyOrMove(context.Background(), copyReq(root+"/dst/out"))
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 207 {
		t.Fatalf("status = %d, want 207", res.Status)
	}
	if len(res.Items) != 1 || res.Items[0].Href != root+"/src/link" || res.Items[0].Status != 403 {
		t.Fatalf("items = %+v, want 403 for symlink member", res.Items)
	}
	if fs.has(root + "/dst/out/link") {
		t.Fatalf("symlink member must not be copied")
	}
	if !fs.has(root + "/dst/out/a.txt") {
		t.Fatalf("regular members must still be copied")
	}
}

func TestMoveRequiresDeletePermission(t *testing.T) {
	// 源删除权限被拒：整体 403，源与目标均无变化。
	fs := buildTree()
	auth := ruleAuth{noDelete: map[string]bool{root + "/src": true}}
	e := newEngine(t, fs, auth, KeepCopied)

	req := copyReq(root + "/dst/out")
	req.Method = MethodMove
	res, err := e.CopyOrMove(context.Background(), req)
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 403 {
		t.Fatalf("status = %d, want 403", res.Status)
	}
	if fs.has(root+"/dst/out") || !fs.has(root+"/src/a.txt") {
		t.Fatalf("move with denied delete must not copy or remove anything")
	}

	// 有删除权限：移动成功，源被移除。
	fs2 := buildTree()
	e2 := newEngine(t, fs2, ruleAuth{}, KeepCopied)
	res, err = e2.CopyOrMove(context.Background(), req)
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 201 {
		t.Fatalf("status = %d, want 201", res.Status)
	}
	if fs2.has(root+"/src") || !fs2.has(root+"/dst/out/sub/b.txt") {
		t.Fatalf("move must relocate the whole tree, got %v", fs2.nodes)
	}
}

func TestDepthZeroCopiesRootOnly(t *testing.T) {
	fs := buildTree()
	e := newEngine(t, fs, ruleAuth{}, KeepCopied)

	req := copyReq(root + "/dst/out")
	req.Depth = "0"
	res, err := e.CopyOrMove(context.Background(), req)
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 201 {
		t.Fatalf("status = %d, want 201", res.Status)
	}
	if !fs.has(root+"/dst/out") || fs.has(root+"/dst/out/a.txt") {
		t.Fatalf("Depth: 0 must copy only the collection itself")
	}
}

func TestDestWriteDenied(t *testing.T) {
	fs := buildTree()
	auth := ruleAuth{noWrite: map[string]bool{root + "/dst": true}}
	e := newEngine(t, fs, auth, KeepCopied)

	res, err := e.CopyOrMove(context.Background(), copyReq(root+"/dst/out"))
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 403 || fs.has(root+"/dst/out") {
		t.Fatalf("status = %d, want 403 and no copy", res.Status)
	}
}

func TestCopyIntoItselfRejected(t *testing.T) {
	fs := buildTree()
	e := newEngine(t, fs, ruleAuth{}, KeepCopied)

	res, err := e.CopyOrMove(context.Background(), copyReq(root+"/src/inner"))
	if err != nil {
		t.Fatalf("CopyOrMove: %v", err)
	}
	if res.Status != 403 {
		t.Fatalf("status = %d, want 403", res.Status)
	}
}
