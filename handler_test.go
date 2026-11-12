package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type fakeOpenCall struct {
	pid     uint32
	view    View
	relPath string
	flags   uint32
	mode    uint32
}

type fakeSendCall struct {
	id  uint64
	src *os.File
}

type fakeRespondCall struct {
	id    uint64
	val   int32
	errno syscall.Errno
}

type fakeKernel struct {
	validErrs  []error
	validCalls int

	readCalls int
	readFn    func(pid uint32, addr uint64, max int) ([]byte, error)
	memory    map[uint64][]byte

	view    View
	viewErr error

	dirfdBases map[int32]string
	dirfdErr   error

	openCalls []fakeOpenCall
	openErr   error
	openFn    func(view View, relPath string, flags uint32, mode uint32) (*os.File, error)

	sendCalls []fakeSendCall
	sendFD    int
	sendErr   error

	respondCalls []fakeRespondCall
	respondErr   error
}

func newFakeKernel() *fakeKernel {
	return &fakeKernel{
		memory:     make(map[uint64][]byte),
		dirfdBases: make(map[int32]string),
		sendFD:     42,
	}
}

func (f *fakeKernel) NotifValid(id uint64) error {
	i := f.validCalls
	f.validCalls++
	if i < len(f.validErrs) {
		return f.validErrs[i]
	}
	return nil
}

func (f *fakeKernel) ReadCString(pid uint32, addr uint64, max int) ([]byte, error) {
	f.readCalls++
	if f.readFn != nil {
		return f.readFn(pid, addr, max)
	}
	p, ok := f.memory[addr]
	if !ok {
		return nil, syscall.EFAULT
	}
	if len(p) > max {
		return nil, syscall.ENAMETOOLONG
	}
	return append([]byte(nil), p...), nil
}

func (f *fakeKernel) ProcessView(pid uint32) (View, error) {
	if f.viewErr != nil {
		return View{}, f.viewErr
	}
	return f.view, nil
}

func (f *fakeKernel) DirFDBase(pid uint32, dirfd int32) (string, error) {
	if f.dirfdErr != nil {
		return "", f.dirfdErr
	}
	base, ok := f.dirfdBases[dirfd]
	if !ok {
		return "", syscall.EBADF
	}
	return base, nil
}

func (f *fakeKernel) OpenInView(view View, relPath string, flags uint32, mode uint32) (*os.File, error) {
	f.openCalls = append(f.openCalls, fakeOpenCall{
		view:    view,
		relPath: relPath,
		flags:   flags,
		mode:    mode,
	})
	if f.openErr != nil {
		return nil, f.openErr
	}
	if f.openFn != nil {
		return f.openFn(view, relPath, flags, mode)
	}
	return os.Open(testJoin(view.Root, relPath))
}

func (f *fakeKernel) SendFD(id uint64, src *os.File) (int, error) {
	f.sendCalls = append(f.sendCalls, fakeSendCall{id: id, src: src})
	if f.sendErr != nil {
		return -1, f.sendErr
	}
	return f.sendFD, nil
}

func (f *fakeKernel) Respond(id uint64, val int32, errno syscall.Errno) error {
	f.respondCalls = append(f.respondCalls, fakeRespondCall{id: id, val: val, errno: errno})
	if f.respondErr != nil {
		return f.respondErr
	}
	return nil
}

func nativeOpenatNotification(id uint64, pid uint32, dirfd int32, addr uint64, flags uint32) Notification {
	nr, _ := OpenatSyscall(0)
	return Notification{
		ID:      id,
		PID:     pid,
		Syscall: nr,
		Args: [6]uint64{
			uint64(uint32(dirfd)),
			addr,
			uint64(flags),
			0, 0, 0,
		},
	}
}

// testJoin is a userspace approximation of RESOLVE_IN_ROOT: it follows no
// symlinks, but it also refuses to let ".." climb above root.
func testJoin(root, rel string) string {
	var parts []string
	for _, part := range strings.Split(rel, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, part)
		}
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func TestHandleOpenUsesOneSnapshotWhenMemoryIsRewritten(t *testing.T) {
	const ptr = uint64(0x1000)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"safe.txt", "evil.txt"} {
		if err := os.WriteFile(filepath.Join(root, "work", name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fk := newFakeKernel()
	fk.memory[ptr] = []byte("safe.txt")
	fk.view = View{Root: root, Cwd: "work"}
	fk.readFn = func(pid uint32, addr uint64, max int) ([]byte, error) {
		// Snapshot exactly what the kernel's single copy would have produced,
		// then let the attacker rewrite the traced process's memory.
		snapshot := append([]byte(nil), fk.memory[addr]...)
		fk.memory[addr] = []byte("evil.txt")
		return snapshot, nil
	}

	h := NewHandler(fk)
	n := nativeOpenatNotification(7, 1234, AT_FDCWD, ptr, syscall.O_RDONLY)
	res, err := h.Handle(n)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if fk.readCalls != 1 {
		t.Fatalf("path memory read %d times, want exactly once", fk.readCalls)
	}
	if len(fk.openCalls) != 1 {
		t.Fatalf("opened %d times, want 1", len(fk.openCalls))
	}
	if got := fk.openCalls[0].relPath; got != "work/safe.txt" {
		t.Fatalf("opened %q, want the single-copy snapshot %q (attacker rewrite leaked through)", got, "work/safe.txt")
	}
	if res.Path != "safe.txt" {
		t.Fatalf("result path = %q, want safe.txt", res.Path)
	}
	if len(fk.sendCalls) != 1 {
		t.Fatalf("SendFD calls = %d, want 1", len(fk.sendCalls))
	}
	if res.TargetFD != 42 {
		t.Fatalf("target fd = %d, want 42", res.TargetFD)
	}
}

func TestHandleOpenNotificationInvalidAfterRead(t *testing.T) {
	const ptr = uint64(0x2000)
	fk := newFakeKernel()
	fk.memory[ptr] = []byte("ok.txt")
	fk.view = View{Root: t.TempDir(), Cwd: ""}
	fk.validErrs = []error{nil, ErrNotificationGone}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(9, 1234, AT_FDCWD, ptr, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: ptr,
	})
	if err == nil || !errors.Is(err, ErrNotificationGone) {
		t.Fatalf("err = %v, want ErrNotificationGone", err)
	}
	if res != nil {
		t.Fatalf("result = %+v, want nil after expired notification", res)
	}
	if fk.validCalls != 2 {
		t.Fatalf("ID_VALID calls = %d, want before and after read", fk.validCalls)
	}
	if fk.readCalls != 1 {
		t.Fatalf("path memory read %d times, want 1", fk.readCalls)
	}
	if len(fk.openCalls) != 0 || len(fk.sendCalls) != 0 || len(fk.respondCalls) != 0 {
		t.Fatalf("acted after invalidation: open=%d send=%d respond=%d",
			len(fk.openCalls), len(fk.sendCalls), len(fk.respondCalls))
	}
}

func TestHandleOpenNotificationInvalidBeforeRead(t *testing.T) {
	fk := newFakeKernel()
	fk.validErrs = []error{syscall.ENOENT}
	fk.view = View{Root: t.TempDir()}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(10, 1234, AT_FDCWD, 0x3000, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: 0x3000,
	})
	if err == nil || !errors.Is(err, ErrNotificationGone) {
		t.Fatalf("err = %v, want ErrNotificationGone", err)
	}
	if res != nil {
		t.Fatalf("result = %+v, want nil", res)
	}
	if fk.validCalls != 1 {
		t.Fatalf("ID_VALID calls = %d, want 1", fk.validCalls)
	}
	if fk.readCalls != 0 || len(fk.openCalls) != 0 || len(fk.sendCalls) != 0 || len(fk.respondCalls) != 0 {
		t.Fatalf("touched memory or notification after invalid ID")
	}
}

func TestHandleOpenValidRelativePathUsesCwdAndInjectsFD(t *testing.T) {
	const ptr = uint64(0x4000)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("legitimate file contents")
	if err := os.WriteFile(filepath.Join(root, "work", "hello.txt"), want, 0o600); err != nil {
		t.Fatal(err)
	}

	fk := newFakeKernel()
	fk.memory[ptr] = []byte("hello.txt")
	fk.view = View{Root: root, Cwd: "work"}

	h := NewHandler(fk)
	n := nativeOpenatNotification(11, 1234, AT_FDCWD, ptr, syscall.O_RDONLY)
	res, err := h.Handle(n)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.Path != "hello.txt" {
		t.Fatalf("Path = %q", res.Path)
	}
	if res.ResolvedPath != "work/hello.txt" {
		t.Fatalf("ResolvedPath = %q, want work/hello.txt", res.ResolvedPath)
	}
	if res.View.Cwd != "work" {
		t.Fatalf("view cwd = %q", res.View.Cwd)
	}
	if len(fk.openCalls) != 1 || fk.openCalls[0].relPath != "work/hello.txt" {
		t.Fatalf("open calls = %+v", fk.openCalls)
	}
	if res.TargetFD != 42 {
		t.Fatalf("TargetFD = %d, want injected fd 42", res.TargetFD)
	}
	if len(fk.sendCalls) != 1 || fk.sendCalls[0].id != 11 {
		t.Fatalf("send calls = %+v, want exactly one for notification 11", fk.sendCalls)
	}
	if len(fk.respondCalls) != 0 {
		t.Fatalf("unexpected denial response: %+v", fk.respondCalls)
	}
	if fk.validCalls != 2 || fk.readCalls != 1 {
		t.Fatalf("valid=%d read=%d, want 2 and 1", fk.validCalls, fk.readCalls)
	}
}

func TestHandleOpenAbsolutePathUsesProcessRoot(t *testing.T) {
	const ptr = uint64(0x5000)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}

	fk := newFakeKernel()
	fk.memory[ptr] = []byte("/etc/inside.txt")
	fk.view = View{Root: root, Cwd: "work"}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(12, 1234, AT_FDCWD, ptr, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: ptr,
	})
	if err != nil {
		t.Fatalf("HandleOpen: %v", err)
	}
	if res.ResolvedPath != "etc/inside.txt" {
		t.Fatalf("ResolvedPath = %q, want etc/inside.txt under the process root", res.ResolvedPath)
	}
	if len(fk.openCalls) != 1 || fk.openCalls[0].view.Root != root {
		t.Fatalf("open used wrong view: %+v", fk.openCalls)
	}
}

func TestHandleOpenRelativeNeverEscapesRoot(t *testing.T) {
	const ptr = uint64(0x6000)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("root file"), 0o600); err != nil {
		t.Fatal(err)
	}

	fk := newFakeKernel()
	fk.memory[ptr] = []byte("../../../../outside.txt")
	fk.view = View{Root: root, Cwd: "work"}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(13, 1234, AT_FDCWD, ptr, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: ptr,
	})
	if err != nil {
		t.Fatalf("HandleOpen: %v", err)
	}
	if res.ResolvedPath != "work/../../../../outside.txt" {
		t.Fatalf("resolved = %q, kernel must see unclean path so RESOLVE_IN_ROOT can clamp it", res.ResolvedPath)
	}
	if got := fk.openCalls[0].relPath; got != "work/../../../../outside.txt" {
		t.Fatalf("open path = %q", got)
	}
}

func TestHandleOpenReadErrorStillRevalidatesAndDenies(t *testing.T) {
	fk := newFakeKernel()
	fk.view = View{Root: t.TempDir()}
	fk.readFn = func(pid uint32, addr uint64, max int) ([]byte, error) {
		return nil, syscall.EFAULT
	}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(14, 1234, AT_FDCWD, 0x7000, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: 0x7000,
	})
	var denial *DenialError
	if !errors.As(err, &denial) {
		t.Fatalf("err = %v, want DenialError", err)
	}
	if denial.Errno != syscall.EFAULT {
		t.Fatalf("denial errno = %v, want EFAULT", denial.Errno)
	}
	if res == nil || res.Errno != syscall.EFAULT {
		t.Fatalf("result = %+v", res)
	}
	if fk.validCalls != 2 {
		t.Fatalf("ID_VALID calls = %d, want 2 even after read failure", fk.validCalls)
	}
	if len(fk.respondCalls) != 1 || fk.respondCalls[0].errno != syscall.EFAULT {
		t.Fatalf("responses = %+v", fk.respondCalls)
	}
	if len(fk.openCalls) != 0 || len(fk.sendCalls) != 0 {
		t.Fatalf("opened/injected after failed path read")
	}
}

func TestHandleOpenReadErrorAndExpiredDoesNotRespond(t *testing.T) {
	fk := newFakeKernel()
	fk.readFn = func(pid uint32, addr uint64, max int) ([]byte, error) {
		return nil, syscall.EFAULT
	}
	fk.validErrs = []error{nil, ErrNotificationGone}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(15, 1234, AT_FDCWD, 0x8000, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: 0x8000,
	})
	if err == nil || !errors.Is(err, ErrNotificationGone) {
		t.Fatalf("err = %v, want ErrNotificationGone", err)
	}
	if res != nil {
		t.Fatalf("result = %+v", res)
	}
	if len(fk.respondCalls) != 0 {
		t.Fatalf("responded to expired notification: %+v", fk.respondCalls)
	}
}

func TestHandleOpenDirFDUsesProcessView(t *testing.T) {
	const ptr = uint64(0x9000)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	fk := newFakeKernel()
	fk.memory[ptr] = []byte("x")
	fk.view = View{Root: root, Cwd: "work"}
	fk.dirfdBases[5] = "data"

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(16, 1234, 5, ptr, 0), OpenRequest{
		DirFD:    5,
		PathAddr: ptr,
	})
	if err != nil {
		t.Fatalf("HandleOpen: %v", err)
	}
	if res.ResolvedPath != "data/x" {
		t.Fatalf("ResolvedPath = %q, want data/x", res.ResolvedPath)
	}
}

func TestHandleOpenDeniesOpenError(t *testing.T) {
	fk := newFakeKernel()
	fk.memory[0xa000] = []byte("missing")
	fk.view = View{Root: t.TempDir()}
	fk.openErr = syscall.EACCES

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(17, 1234, AT_FDCWD, 0xa000, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: 0xa000,
	})
	var denial *DenialError
	if !errors.As(err, &denial) || denial.Errno != syscall.EACCES {
		t.Fatalf("err = %v, want EACCES denial", err)
	}
	if res == nil || res.Errno != syscall.EACCES || res.TargetFD != -1 {
		t.Fatalf("result = %+v", res)
	}
	if len(fk.respondCalls) != 1 || fk.respondCalls[0].errno != syscall.EACCES {
		t.Fatalf("responses = %+v", fk.respondCalls)
	}
	if len(fk.sendCalls) != 0 {
		t.Fatalf("injected fd after open failure")
	}
}

func TestHandleOpenSendFDErrorIsReportedAsDenial(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fk := newFakeKernel()
	fk.memory[0xb000] = []byte("x")
	fk.view = View{Root: root}
	fk.sendErr = syscall.EBADF

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(18, 1234, AT_FDCWD, 0xb000, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: 0xb000,
	})
	var denial *DenialError
	if !errors.As(err, &denial) || denial.Errno != syscall.EBADF {
		t.Fatalf("err = %v, want EBADF denial", err)
	}
	if res == nil || res.Allowed() || res.Errno != syscall.EBADF {
		t.Fatalf("result = %+v, want denied", res)
	}
	if len(fk.respondCalls) != 1 || fk.respondCalls[0].errno != syscall.EBADF {
		t.Fatalf("responses = %+v", fk.respondCalls)
	}
}

func TestHandleOpenOverlongPathDeniesWithoutOpening(t *testing.T) {
	fk := newFakeKernel()
	fk.memory[0xc000] = []byte(strings.Repeat("a", MaxPathBytes+1))
	fk.view = View{Root: t.TempDir()}

	h := NewHandler(fk)
	res, err := h.HandleOpen(nativeOpenatNotification(19, 1234, AT_FDCWD, 0xc000, 0), OpenRequest{
		DirFD:    AT_FDCWD,
		PathAddr: 0xc000,
	})
	var denial *DenialError
	if !errors.As(err, &denial) || denial.Errno != syscall.ENAMETOOLONG {
		t.Fatalf("err = %v, want ENAMETOOLONG denial", err)
	}
	if res == nil || res.Errno != syscall.ENAMETOOLONG {
		t.Fatalf("result = %+v", res)
	}
	if len(fk.openCalls) != 0 || len(fk.sendCalls) != 0 {
		t.Fatalf("opened an overlong path")
	}
}

func TestHandleRejectsNonOpenatWithoutTouchingTarget(t *testing.T) {
	fk := newFakeKernel()
	h := NewHandler(fk)
	_, err := h.Handle(Notification{ID: 1, PID: 2, Syscall: 999})
	if err == nil || !errors.Is(err, ErrUnsupportedSyscall) {
		t.Fatalf("err = %v, want ErrUnsupportedSyscall", err)
	}
	if fk.validCalls != 0 || fk.readCalls != 0 {
		t.Fatalf("touched target for non-openat notification")
	}
}

func TestOpenatRequestDecoding(t *testing.T) {
	nr, ok := OpenatSyscall(0)
	if !ok {
		t.Fatal("native architecture has no openat number")
	}
	n := Notification{
		Arch:    0,
		Syscall: nr,
		Args:    [6]uint64{^uint64(99), 0x1234, 0x10, 0x20},
	}
	req, err := n.OpenatRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.DirFD != AT_FDCWD || req.PathAddr != 0x1234 || req.Flags != 0x10 || req.Mode != 0x20 {
		t.Fatalf("decoded = %+v", req)
	}
	if _, err := (Notification{Arch: 0xdeadbeef, Syscall: nr}).OpenatRequest(); !errors.Is(err, ErrUnsupportedSyscall) {
		t.Fatalf("unknown arch err = %v", err)
	}
}

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		path    string
		want    string
		wantErr error
	}{
		{name: "cwd relative", base: "work", path: "a/b", want: "work/a/b"},
		{name: "root cwd", base: "", path: "a", want: "a"},
		{name: "dot cwd", base: ".", path: "a", want: "a"},
		{name: "absolute ignores cwd", base: "work", path: "/etc/passwd", want: "etc/passwd"},
		{name: "leading slashes", base: "work", path: "///a", want: "a"},
		{name: "root path", base: "work", path: "/", want: "."},
		{name: "dotdot preserved", base: "work/sub", path: "../x", want: "work/sub/../x"},
		{name: "empty", base: "work", path: "", wantErr: syscall.ENOENT},
		{name: "nul", base: "work", path: "a\x00b", wantErr: syscall.EINVAL},
		{name: "absolute base rejected", base: "/work", path: "a", wantErr: syscall.EINVAL},
		{name: "escaping base rejected", base: "../work", path: "a", wantErr: syscall.EINVAL},
		{name: "too long", base: "", path: strings.Repeat("a", MaxPathBytes+1), wantErr: syscall.ENAMETOOLONG},
		{name: "resolved too long", base: "work", path: strings.Repeat("a", MaxPathBytes), wantErr: syscall.ENAMETOOLONG},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePath(tt.base, tt.path)
			if tt.wantErr != nil {
				if err == nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolvePath(%q, %q) err = %v, want %v", tt.base, tt.path, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePath: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolvePath(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}

func TestHandlerRequiresKernel(t *testing.T) {
	var h Handler
	_, err := h.Handle(nativeOpenatNotification(1, 2, AT_FDCWD, 0x1000, 0))
	if !errors.Is(err, ErrNoKernel) {
		t.Fatalf("err = %v, want ErrNoKernel", err)
	}
}
