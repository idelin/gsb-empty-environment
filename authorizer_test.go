package netsec

import (
	"errors"
	"path/filepath"
	"testing"
)

const testRoot = "/srv/sftproot"

type accessCall struct {
	path string
	perm Permission
}

// fakeFS is an in-memory FS implementation: a node tree with symlinks,
// per-path permission overrides and an opaque handle table.
type fakeFS struct {
	nodes   map[string]bool // host path -> exists
	links   map[string]string
	handles map[string]string
	denied  map[string]map[Permission]bool
	calls   []accessCall
}

func newFakeFS() *fakeFS {
	f := &fakeFS{
		nodes:   map[string]bool{},
		links:   map[string]string{},
		handles: map[string]string{},
		denied:  map[string]map[Permission]bool{},
	}
	for _, dir := range []string{
		testRoot,
		testRoot + "/a",
		testRoot + "/b",
		testRoot + "/hl",
	} {
		f.nodes[dir] = true
	}
	for _, file := range []string{
		testRoot + "/a/f.txt",
		testRoot + "/b/f.txt",
		testRoot + "/secret.txt",
		testRoot + "/src.dat",
		testRoot + "/dst.dat",
		testRoot + "/stat.dat",
	} {
		f.nodes[file] = true
	}
	return f
}

func (f *fakeFS) EvalSymlinks(p string) (string, error) {
	return f.resolve(p, map[string]bool{})
}

func (f *fakeFS) resolve(p string, seen map[string]bool) (string, error) {
	p = filepath.Clean(p)
	parts := splitComponents(p)
	cur := "/"
	for _, comp := range parts {
		next := filepath.Join(cur, comp)
		target, isLink := f.links[next]
		if !isLink {
			cur = next
			continue
		}
		if seen[next] {
			return "", statusError(SSH_FX_FAILURE, "symlink loop at %q", next)
		}
		seen[next] = true
		if filepath.IsAbs(target) {
			cur = target
		} else {
			cur = filepath.Join(cur, target)
		}
		real, err := f.resolve(cur, seen)
		if err != nil {
			return "", err
		}
		cur = real
	}
	return filepath.Clean(cur), nil
}

func splitComponents(p string) []string {
	p = filepath.Clean(p)
	if p == "/" {
		return nil
	}
	var out []string
	cur := ""
	for _, r := range p {
		if r == '/' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func (f *fakeFS) ResolveHandle(handle string) (string, error) {
	p, ok := f.handles[handle]
	if !ok {
		return "", statusError(SSH_FX_BAD_MESSAGE, "unknown handle %q", handle)
	}
	return p, nil
}

func (f *fakeFS) Access(p string, perm Permission) error {
	f.calls = append(f.calls, accessCall{p, perm})
	if f.denied[p] != nil && f.denied[p][perm] {
		return statusError(SSH_FX_PERMISSION_DENIED, "%s denied on %q", perm, p)
	}
	return nil
}

func (f *fakeFS) deny(p string, perm Permission) {
	if f.denied[p] == nil {
		f.denied[p] = map[Permission]bool{}
	}
	f.denied[p][perm] = true
}

func newTestAuthorizer(t *testing.T, readOnly bool) (*Authorizer, *fakeFS) {
	t.Helper()
	f := newFakeFS()
	f.handles["rh"] = testRoot + "/src.dat"
	f.handles["wh"] = testRoot + "/dst.dat"
	f.handles["sh"] = testRoot + "/stat.dat"
	a, err := New(Config{Root: testRoot, FS: f, ReadOnly: readOnly})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, f
}

func mustStatusCode(t *testing.T, err error) uint32 {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	return se.Code
}

func TestReadOnlyBlocksEveryWriteClassExtension(t *testing.T) {
	a, _ := newTestAuthorizer(t, true)

	cases := []struct {
		name string
		req  ExtendedRequest
	}{
		{"posix-rename", ExtendedRequest{
			Name: ExtPosixRename, OldPath: "/a/f.txt", NewPath: "/b/f.txt",
		}},
		{"hardlink", ExtendedRequest{
			Name: ExtHardlink, TargetPath: "/secret.txt", LinkPath: "/hl/secret.txt",
		}},
		{"copy-data", ExtendedRequest{
			Name: ExtCopyData, ReadHandle: "rh", WriteHandle: "wh",
		}},
		{"fsetstat", ExtendedRequest{
			Name: ExtFSetStat, Handle: "sh",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := mustStatusCode(t, a.CheckExtended(tc.req)); code != SSH_FX_PERMISSION_DENIED {
				t.Fatalf("got status %d, want SSH_FX_PERMISSION_DENIED", code)
			}
		})
	}

	// Reads stay available in read-only mode; plain writes are blocked too.
	if err := a.CheckRead("/a/f.txt"); err != nil {
		t.Fatalf("read should be allowed in read-only mode: %v", err)
	}
	if code := mustStatusCode(t, a.CheckWrite("/a/f.txt")); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("plain write status = %d, want permission denied", code)
	}
}

func TestRenameEscapingRootIsRejected(t *testing.T) {
	a, f := newTestAuthorizer(t, false)

	// Virtual paths are anchored at "/" and lexically cleaned, so ".." can
	// never climb past the root: it collapses back inside the root and the
	// request is authorized as a normal in-root rename.
	req := ExtendedRequest{
		Name:    ExtPosixRename,
		OldPath: "/a/f.txt",
		NewPath: "../../etc/passwd",
	}
	if err := a.CheckExtended(req); err != nil {
		t.Fatalf("in-root rename with redundant .. should be allowed: %v", err)
	}
	for _, call := range f.calls {
		if !filepath.IsAbs(call.path) || len(call.path) < len(testRoot) || call.path[:len(testRoot)] != testRoot {
			t.Fatalf("access check escaped root: %v", call)
		}
	}

	// Symlink escape: the path looks inside the root but its real target is not.
	f.nodes[testRoot+"/evil"] = true
	f.links[testRoot+"/evil"] = "/etc"
	req = ExtendedRequest{
		Name:    ExtPosixRename,
		OldPath: "/evil/passwd",
		NewPath: "/b/passwd",
	}
	if code := mustStatusCode(t, a.CheckExtended(req)); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("symlink escape status = %d, want permission denied", code)
	}

	// Same guarantee on the basic path-checking API.
	if code := mustStatusCode(t, a.CheckWrite("/evil/shadow")); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("symlink escape (basic) status = %d, want permission denied", code)
	}
}

func TestHardlinkRequiresReadableSourceAndWritableTargetDir(t *testing.T) {
	a, f := newTestAuthorizer(t, false)
	f.deny(testRoot+"/secret.txt", PermRead)

	req := ExtendedRequest{
		Name:       ExtHardlink,
		TargetPath: "/secret.txt",
		LinkPath:   "/hl/secret.txt",
	}
	if code := mustStatusCode(t, a.CheckExtended(req)); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("unreadable source status = %d, want permission denied", code)
	}

	// With read access restored the link is allowed and the target directory
	// is checked for write, not the source file.
	delete(f.denied, testRoot+"/secret.txt")
	f.calls = nil
	if err := a.CheckExtended(req); err != nil {
		t.Fatalf("hardlink should succeed: %v", err)
	}
	want := []accessCall{
		{testRoot + "/secret.txt", PermRead},
		{testRoot + "/hl", PermWrite},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("access calls = %v, want %v", f.calls, want)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v", i, f.calls[i], want[i])
		}
	}

	// A writable source but unwritable link directory must still fail.
	f.deny(testRoot+"/hl", PermWrite)
	if code := mustStatusCode(t, a.CheckExtended(req)); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("unwritable link dir status = %d, want permission denied", code)
	}
}

func TestUnknownExtensionsAreUnsupportedEvenWithVendorSuffix(t *testing.T) {
	a, f := newTestAuthorizer(t, false)

	names := []string{
		"made-up-extension",
		"posix-rename@evil.example.com",
		"hardlink@openssh.com.attacker",
		"fsetstat@openssh.comX",
		"",
	}
	for _, name := range names {
		err := a.CheckExtended(ExtendedRequest{Name: name})
		if !errors.Is(err, ErrOpUnsupported) {
			t.Fatalf("name %q: err = %v, want ErrOpUnsupported", name, err)
		}
		if code := mustStatusCode(t, err); code != SSH_FX_OP_UNSUPPORTED {
			t.Fatalf("name %q: status = %d, want OP_UNSUPPORTED", name, code)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("unknown extensions must not trigger access checks, got %v", f.calls)
	}
}

func TestWritableModeMapsExtensionsToEquivalentBasicPermissions(t *testing.T) {
	a, f := newTestAuthorizer(t, false)

	if err := a.CheckExtended(ExtendedRequest{
		Name: ExtPosixRename, OldPath: "/a/f.txt", NewPath: "/b/f.txt",
	}); err != nil {
		t.Fatalf("posix-rename: %v", err)
	}
	if err := a.CheckExtended(ExtendedRequest{
		Name: ExtCopyData, ReadHandle: "rh", WriteHandle: "wh",
	}); err != nil {
		t.Fatalf("copy-data: %v", err)
	}
	if err := a.CheckExtended(ExtendedRequest{
		Name: ExtFSetStat, Handle: "sh",
	}); err != nil {
		t.Fatalf("fsetstat: %v", err)
	}

	want := []accessCall{
		{testRoot + "/a", PermWrite},
		{testRoot + "/b", PermWrite},
		{testRoot + "/src.dat", PermRead},
		{testRoot + "/dst.dat", PermWrite},
		{testRoot + "/stat.dat", PermWrite},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("access calls = %v, want %v", f.calls, want)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v", i, f.calls[i], want[i])
		}
	}
}

func TestHandleResolvingOutsideRootIsRejected(t *testing.T) {
	a, f := newTestAuthorizer(t, false)
	f.handles["bad"] = "/etc/shadow"

	err := a.CheckExtended(ExtendedRequest{
		Name: ExtCopyData, ReadHandle: "bad", WriteHandle: "wh",
	})
	if code := mustStatusCode(t, err); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("copy-data escape status = %d, want permission denied", code)
	}

	err = a.CheckExtended(ExtendedRequest{Name: ExtFSetStat, Handle: "bad"})
	if code := mustStatusCode(t, err); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("fsetstat escape status = %d, want permission denied", code)
	}
}

func TestMalformedRequestsAreBadMessage(t *testing.T) {
	a, _ := newTestAuthorizer(t, false)

	bad := []ExtendedRequest{
		{Name: ExtPosixRename, OldPath: "/a/f.txt"},
		{Name: ExtHardlink, TargetPath: "/secret.txt"},
		{Name: ExtCopyData, ReadHandle: "rh"},
		{Name: ExtFSetStat},
	}
	for _, req := range bad {
		if code := mustStatusCode(t, a.CheckExtended(req)); code != SSH_FX_BAD_MESSAGE {
			t.Fatalf("req %+v: status = %d, want BAD_MESSAGE", req, code)
		}
	}
}

func TestNewRequiresFS(t *testing.T) {
	if _, err := New(Config{Root: testRoot}); err == nil {
		t.Fatal("New without FS should fail")
	}
}
