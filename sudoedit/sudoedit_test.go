package sudoedit_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"example.org/netsec/q0793/sudoedit"
)

// fakeFS is an in-memory filesystem keyed by absolute paths. Missing paths
// report fs.ErrNotExist, matching os.Lstat.
type fakeFS struct {
	entries map[string]sudoedit.Entry
}

func (f fakeFS) Lstat(path string) (sudoedit.Entry, error) {
	if entry, ok := f.entries[path]; ok {
		return entry, nil
	}
	return sudoedit.Entry{}, fs.ErrNotExist
}

func newFakeFS() fakeFS {
	dir := fs.ModeDir | 0o755
	reg := fs.FileMode(0o644)
	return fakeFS{entries: map[string]sudoedit.Entry{
		"/":               {Mode: dir, OwnerUID: 0, OwnerGID: 0},
		"/etc":            {Mode: dir, OwnerUID: 0, OwnerGID: 0},
		"/etc/conf.d":     {Mode: dir, OwnerUID: 0, OwnerGID: 0},
		"/etc/conf.d/a":   {Mode: reg, OwnerUID: 0, OwnerGID: 0},
		"/etc/conf.d/b":   {Mode: reg, OwnerUID: 0, OwnerGID: 0},
		"/etc/shadow":     {Mode: reg, OwnerUID: 0, OwnerGID: 0},
		"/etc/passwd":     {Mode: reg, OwnerUID: 0, OwnerGID: 0},
		"/etc/linkdir":    {Mode: fs.ModeSymlink | 0o777, OwnerUID: 0, OwnerGID: 0},
		"/etc/link.conf":  {Mode: fs.ModeSymlink | 0o777, OwnerUID: 0, OwnerGID: 0},
		"/tmp":            {Mode: fs.ModeDir | 0o1777, OwnerUID: 0, OwnerGID: 0},
		"/tmp/loose.conf": {Mode: reg, OwnerUID: 0, OwnerGID: 0},
		"/srv":            {Mode: fs.ModeDir | 0o775, OwnerUID: 0, OwnerGID: 200},
		"/srv/grp.conf":   {Mode: reg, OwnerUID: 0, OwnerGID: 200},
		"/usr":            {Mode: dir, OwnerUID: 0, OwnerGID: 0},
		"/usr/bin":        {Mode: dir, OwnerUID: 0, OwnerGID: 0},
		"/usr/bin/vim":    {Mode: reg | 0o755, OwnerUID: 0, OwnerGID: 0},
		"/bin":            {Mode: dir, OwnerUID: 0, OwnerGID: 0},
		"/bin/vim":        {Mode: reg | 0o755, OwnerUID: 0, OwnerGID: 0},
	}}
}

func testCaller() sudoedit.Caller {
	return sudoedit.Caller{UID: 1000, GID: 1000, Groups: []int{1000, 200, 300}}
}

func baseRequest(fsys fakeFS) sudoedit.Request {
	return sudoedit.Request{
		Env:             map[string]string{},
		Targets:         []string{"/etc/conf.d/a"},
		Authorized:      []string{"/etc/conf.d/", "/etc/passwd"},
		EditorWhitelist: []string{"/usr/bin/vim", "/bin/nano"},
		Caller:          testCaller(),
		FS:              fsys,
	}
}

func TestEditorDashDashSmugglesUnauthorizedFile(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "vim -- /etc/shadow"

	got := sudoedit.Authorize(req)
	if got.Allow {
		t.Fatalf("expected rejection, got allowed decision: %+v", got)
	}
	if got.Reason != sudoedit.ReasonEditorNotAbsolute {
		t.Fatalf("expected editor_not_absolute, got %q (%s)", got.Reason, got.Detail)
	}
}

func TestAbsoluteEditorDashDashSmugglesUnauthorizedFile(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim -- /etc/shadow"

	got := sudoedit.Authorize(req)
	if got.Allow {
		t.Fatalf("expected rejection, got allowed decision: %+v", got)
	}
	if got.Reason != sudoedit.ReasonUnauthorizedFile {
		t.Fatalf("expected unauthorized_file, got %q (%s)", got.Reason, got.Detail)
	}
	if got.File != "/etc/shadow" {
		t.Fatalf("expected offending file /etc/shadow, got %q", got.File)
	}
}

func TestEditorPositionalFileIsAlsoAuthorized(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim /etc/shadow"

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonUnauthorizedFile || got.File != "/etc/shadow" {
		t.Fatalf("expected unauthorized /etc/shadow, got %+v", got)
	}
}

func TestOptionsAfterDashDashAreFiles(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim -- -R"

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonPathNotAbsolute {
		t.Fatalf("expected relative smuggled file to be rejected, got %+v", got)
	}
}

func TestSymlinkParentDirectoryRejected(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Authorized = []string{"/etc/linkdir/"}
	req.Targets = []string{"/etc/linkdir/conf"}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonSymlink {
		t.Fatalf("expected symlink rejection, got %+v", got)
	}
	if got.File != "/etc/linkdir/conf" {
		t.Fatalf("expected offending target path, got %q", got.File)
	}
}

func TestSymlinkTargetRejected(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Authorized = []string{"/etc/link.conf"}
	req.Targets = []string{"/etc/link.conf"}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonSymlink {
		t.Fatalf("expected symlink rejection, got %+v", got)
	}
}

func TestDotDotSegmentRejected(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Targets = []string{"/etc/conf.d/../shadow"}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonPathTraversal {
		t.Fatalf("expected path_traversal rejection, got %+v", got)
	}
	if got.File != "/etc/conf.d/../shadow" {
		t.Fatalf("expected the raw path in File, got %q", got.File)
	}
}

func TestEditorVariablePriority(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		editor  string
		allowed bool
		reason  sudoedit.Reason
	}{
		{
			name:    "SUDO_EDITOR wins",
			env:     map[string]string{"SUDO_EDITOR": "/usr/bin/vim", "VISUAL": "/bin/nano", "EDITOR": "vim"},
			editor:  "/usr/bin/vim",
			allowed: true,
		},
		{
			name:    "VISUAL wins over EDITOR",
			env:     map[string]string{"VISUAL": "/bin/nano", "EDITOR": "/usr/bin/vim"},
			editor:  "/bin/nano",
			allowed: true,
		},
		{
			name:    "EDITOR used as fallback",
			env:     map[string]string{"EDITOR": "/usr/bin/vim"},
			editor:  "/usr/bin/vim",
			allowed: true,
		},
		{
			name:    "empty SUDO_EDITOR is skipped",
			env:     map[string]string{"SUDO_EDITOR": "", "VISUAL": "", "EDITOR": "/usr/bin/vim"},
			editor:  "/usr/bin/vim",
			allowed: true,
		},
		{
			name:    "all empty is rejected",
			env:     map[string]string{"SUDO_EDITOR": "", "VISUAL": "", "EDITOR": ""},
			allowed: false,
			reason:  sudoedit.ReasonNoEditor,
		},
		{
			name:    "relative editor requires PATH and is rejected",
			env:     map[string]string{"EDITOR": "vim"},
			allowed: false,
			reason:  sudoedit.ReasonEditorNotAbsolute,
		},
		{
			name:    "unwhitelisted absolute editor is rejected",
			env:     map[string]string{"EDITOR": "/bin/vim"},
			allowed: false,
			reason:  sudoedit.ReasonEditorNotWhitelisted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRequest(newFakeFS())
			req.Env = tc.env
			got := sudoedit.Authorize(req)
			if got.Allow != tc.allowed {
				t.Fatalf("allowed=%v, want %v (reason=%q detail=%q)", got.Allow, tc.allowed, got.Reason, got.Detail)
			}
			if tc.allowed && got.Editor != tc.editor {
				t.Fatalf("editor=%q, want %q", got.Editor, tc.editor)
			}
			if !tc.allowed && got.Reason != tc.reason {
				t.Fatalf("reason=%q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

func TestLegalEditAllowed(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim -R"
	req.Targets = []string{"/etc/conf.d/a", "/etc/passwd", "/etc/conf.d/a"}

	got := sudoedit.Authorize(req)
	if !got.Allow {
		t.Fatalf("expected allow, got %+v", got)
	}
	if got.Editor != "/usr/bin/vim" {
		t.Fatalf("editor=%q, want /usr/bin/vim", got.Editor)
	}
	wantArgs := []string{"-R"}
	if !reflect.DeepEqual(got.EditorArgs, wantArgs) {
		t.Fatalf("editor args=%v, want %v", got.EditorArgs, wantArgs)
	}
	wantFiles := []string{"/etc/conf.d/a", "/etc/passwd"}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("files=%v, want %v", got.Files, wantFiles)
	}
}

func TestLegalDashDashWithAuthorizedFile(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim -R -- /etc/passwd"
	req.Targets = []string{"/etc/conf.d/b"}

	got := sudoedit.Authorize(req)
	if !got.Allow {
		t.Fatalf("expected allow, got %+v", got)
	}
	wantFiles := []string{"/etc/conf.d/b", "/etc/passwd"}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("files=%v, want %v", got.Files, wantFiles)
	}
}

func TestWritableAncestorRejected(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Authorized = []string{"/tmp/loose.conf"}
	req.Targets = []string{"/tmp/loose.conf"}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonWritable {
		t.Fatalf("expected writable rejection, got %+v", got)
	}
	if got.File != "/tmp/loose.conf" {
		t.Fatalf("File=%q, want /tmp/loose.conf", got.File)
	}
}

func TestGroupWritableAncestorRejected(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Authorized = []string{"/srv/grp.conf"}
	req.Targets = []string{"/srv/grp.conf"}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonWritable {
		t.Fatalf("expected group-writable rejection, got %+v", got)
	}
}

func TestDirectoryPrefixAuthorization(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Targets = []string{"/etc/conf.d/b"}

	got := sudoedit.Authorize(req)
	if !got.Allow {
		t.Fatalf("expected prefix rule to authorize the file, got %+v", got)
	}
}

func TestPrefixDoesNotMatchSiblingPrefix(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Authorized = []string{"/etc/conf.d/"}
	req.Targets = []string{"/etc/conf.d.evil/x"}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonUnauthorizedFile {
		t.Fatalf("expected unauthorized sibling prefix, got %+v", got)
	}
}

func TestAuthorizationRunsAfterEnvironmentSplit(t *testing.T) {
	// The smuggled file is traversal-laden: splitting the editor first is
	// what surfaces it, proving authorization is driven by the parsed
	// environment rather than the original argument list alone.
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim -- /etc/conf.d/../shadow"

	got := sudoedit.Authorize(req)
	if got.Allow {
		t.Fatalf("expected rejection, got %+v", got)
	}
	if got.Reason != sudoedit.ReasonPathTraversal {
		t.Fatalf("expected path_traversal for the parsed extra file, got %q", got.Reason)
	}
	if got.File != "/etc/conf.d/../shadow" {
		t.Fatalf("File=%q, want the raw smuggled path", got.File)
	}
}

func TestEnvFromList(t *testing.T) {
	env := sudoedit.EnvFromList([]string{
		"EDITOR=/usr/bin/vim",
		"VISUAL=/bin/nano",
		"SUDO_EDITOR=",
		"WEIRD=foo=bar=baz",
	})
	want := map[string]string{
		"EDITOR":      "/usr/bin/vim",
		"VISUAL":      "/bin/nano",
		"SUDO_EDITOR": "",
		"WEIRD":       "foo=bar=baz",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env=%v, want %v", env, want)
	}
}

func TestRootCallerFailsClosed(t *testing.T) {
	req := baseRequest(newFakeFS())
	req.Env["EDITOR"] = "/usr/bin/vim"
	req.Caller = sudoedit.Caller{UID: 0, GID: 0}

	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonWritable {
		t.Fatalf("expected root caller to fail closed, got %+v", got)
	}
}

func TestOSFSIntegration(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("integration writability test requires a non-root process")
	}

	temp := t.TempDir()
	editor := filepath.Join(temp, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := sudoedit.Request{
		Env:             map[string]string{"EDITOR": editor},
		EditorWhitelist: []string{editor},
		Caller:          sudoedit.CurrentCaller(),
		FS:              sudoedit.OSFS{},
	}

	// A target owned and writable by the caller must be rejected.
	loose := filepath.Join(temp, "loose")
	if err := os.WriteFile(loose, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	req.Authorized = []string{loose}
	req.Targets = []string{loose}
	got := sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonWritable {
		t.Fatalf("expected writable rejection for caller-owned file, got %+v", got)
	}

	// A symlinked target must be rejected before following it.
	link := filepath.Join(temp, "link")
	if err := os.Symlink(loose, link); err != nil {
		t.Fatal(err)
	}
	req.Targets = []string{link}
	req.Authorized = []string{link}
	got = sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonSymlink {
		t.Fatalf("expected symlink rejection, got %+v", got)
	}

	// A missing parent directory must be reported rather than allowed.
	missing := filepath.Join(temp, "no-such-dir", "conf")
	req.Targets = []string{missing}
	req.Authorized = []string{missing}
	got = sudoedit.Authorize(req)
	if got.Allow || got.Reason != sudoedit.ReasonParentMissing {
		t.Fatalf("expected parent_missing rejection, got %+v", got)
	}
}

func TestOSFSLstatMetadata(t *testing.T) {
	temp := t.TempDir()
	file := filepath.Join(temp, "stat-target")
	if err := os.WriteFile(file, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	entry, err := sudoedit.OSFS{}.Lstat(file)
	if err != nil {
		t.Fatalf("Lstat returned error: %v", err)
	}
	if entry.Mode&fs.ModeSymlink != 0 {
		t.Fatalf("regular file reported as symlink: %v", entry.Mode)
	}
	if entry.OwnerUID != os.Getuid() || entry.OwnerGID != os.Getgid() {
		t.Fatalf("owner=%d:%d, want %d:%d", entry.OwnerUID, entry.OwnerGID, os.Getuid(), os.Getgid())
	}

	if _, err := (sudoedit.OSFS{}).Lstat(filepath.Join(temp, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}
