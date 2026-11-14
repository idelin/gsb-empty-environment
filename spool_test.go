package netsec

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	controlName = "cfA001host"
	dataName    = "dfA001host"
	secretToken = "TOP-SECRET-CANARY"
)

type spoolFixture struct {
	root     string
	spoolA   string
	spoolB   string
	canary   string
	registry *Registry
	queueA   *Queue
	queueB   *Queue
	limits   Limits
}

func newSpoolFixture(t *testing.T) *spoolFixture {
	t.Helper()
	root := t.TempDir()
	spoolA := filepath.Join(root, "spool", "queueA")
	spoolB := filepath.Join(root, "spool", "queueB")
	if err := os.MkdirAll(spoolA, 0o750); err != nil {
		t.Fatalf("mkdir spoolA: %v", err)
	}
	if err := os.MkdirAll(spoolB, 0o750); err != nil {
		t.Fatalf("mkdir spoolB: %v", err)
	}

	// A sensitive file that lives outside every spool directory. A
	// successful traversal would let the service print its contents.
	canary := filepath.Join(root, "etc", "service.conf")
	if err := os.MkdirAll(filepath.Dir(canary), 0o750); err != nil {
		t.Fatalf("mkdir canary dir: %v", err)
	}
	if err := os.WriteFile(canary, []byte(secretToken+"\n"), 0o640); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	if err := os.WriteFile(filepath.Join(spoolA, controlName),
		[]byte("Hcontrol\n"), 0o640); err != nil {
		t.Fatalf("write control: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spoolA, dataName),
		[]byte("job payload\n"), 0o640); err != nil {
		t.Fatalf("write data: %v", err)
	}

	limits := Limits{MaxJobBytes: 1024, MaxFileBytes: 256, MaxFiles: 4}
	queueA, err := NewQueue("queueA", spoolA, limits)
	if err != nil {
		t.Fatalf("NewQueue A: %v", err)
	}
	if err := queueA.AllowControl(controlName); err != nil {
		t.Fatalf("AllowControl: %v", err)
	}
	queueB, err := NewQueue("queueB", spoolB, limits)
	if err != nil {
		t.Fatalf("NewQueue B: %v", err)
	}
	registry := NewRegistry()
	if err := registry.AddQueue(queueA); err != nil {
		t.Fatalf("AddQueue A: %v", err)
	}
	if err := registry.AddQueue(queueB); err != nil {
		t.Fatalf("AddQueue B: %v", err)
	}

	return &spoolFixture{
		root:     root,
		spoolA:   spoolA,
		spoolB:   spoolB,
		canary:   canary,
		registry: registry,
		queueA:   queueA,
		queueB:   queueB,
		limits:   limits,
	}
}

func readAll(t *testing.T, f *File) string {
	t.Helper()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", f.Name(), err)
	}
	return string(data)
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}

func TestValidJobOpensOnlySpoolFiles(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	job, err := fx.queueA.OpenJob(ctx, controlName, []string{dataName})
	if err != nil {
		t.Fatalf("OpenJob: %v", err)
	}
	defer job.Close()

	if got := readAll(t, job.Control); got != "Hcontrol\n" {
		t.Fatalf("control contents = %q", got)
	}
	if got := readAll(t, job.Data[0]); got != "job payload\n" {
		t.Fatalf("data contents = %q", got)
	}
	if job.TotalSize != int64(len("Hcontrol\n")+len("job payload\n")) {
		t.Fatalf("total size = %d", job.TotalSize)
	}
	if job.Control.Name() != controlName || job.Data[0].Name() != dataName {
		t.Fatalf("unexpected file names: %q %q", job.Control.Name(), job.Data[0].Name())
	}
}

func TestPathTraversalRejected(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	traversalNames := []string{
		"..",
		".",
		"../",
		"../etc/service.conf",
		"../../etc/service.conf",
		"dfA001/../../../etc/service.conf",
		"subdir/dfA001",
		"dfA001host/..",
		"dfA001host/../dfA001host",
		"",
		"df\x00A001",
	}
	for _, name := range traversalNames {
		if _, err := fx.queueA.OpenDataFile(ctx, name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("OpenDataFile(%q) err = %v, want ErrInvalidName", name, err)
		}
		if _, err := fx.queueA.OpenControl(ctx, name); !errors.Is(err, ErrControlNotAllowed) {
			t.Fatalf("OpenControl(%q) err = %v, want ErrControlNotAllowed", name, err)
		}
	}

	// Even a well-formed control name that traverses is refused and never
	// reveals the canary contents.
	evil := "cfA001/../../etc/service.conf"
	if _, err := fx.queueA.OpenControl(ctx, evil); !errors.Is(err, ErrControlNotAllowed) {
		t.Fatalf("OpenControl traversal err = %v", err)
	}
	if _, err := fx.registry.OpenJob(ctx, "queueA", evil, []string{dataName}); err == nil {
		t.Fatalf("OpenJob accepted traversal control name")
	}
}

func TestAbsolutePathRejected(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	absoluteNames := []string{
		fx.canary,
		"/etc/service.conf",
		"/" + dataName,
		filepath.Join(fx.spoolA, dataName),
	}
	for _, name := range absoluteNames {
		_, err := fx.queueA.OpenDataFile(ctx, name)
		assertErrIs(t, err, ErrAbsolute)
		_, err = fx.queueA.OpenControl(ctx, name)
		if !errors.Is(err, ErrControlNotAllowed) {
			t.Fatalf("OpenControl(%q) err = %v, want ErrControlNotAllowed", name, err)
		}
	}

	// Absolute queue names and spool directories are refused too.
	if _, err := NewQueue("/queueA", fx.spoolA, fx.limits); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("NewQueue absolute name err = %v", err)
	}
	if _, err := NewQueue("relQueue", "relative/spool", fx.limits); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("NewQueue relative spool err = %v", err)
	}
}

func TestControlWhitelistAndRegistryEnforced(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	// A syntactically valid control file that was never admitted.
	if err := fx.queueA.AllowControl("not-a-control-file"); !errors.Is(err, ErrControlNotAllowed) {
		t.Fatalf("AllowControl bad pattern err = %v", err)
	}
	if _, err := fx.queueA.OpenControl(ctx, "cfA999host"); !errors.Is(err, ErrControlNotAllowed) {
		t.Fatalf("unadmitted control err = %v", err)
	}

	// Unknown queues are rejected by the registry.
	if _, err := fx.registry.Queue("ghost"); !errors.Is(err, ErrQueueNotAllowed) {
		t.Fatalf("unknown queue err = %v", err)
	}
	if _, _, err := fx.registry.OpenControl(ctx, "ghost", controlName); !errors.Is(err, ErrQueueNotAllowed) {
		t.Fatalf("registry OpenControl unknown queue err = %v", err)
	}
	if _, err := fx.registry.OpenJob(ctx, "ghost", controlName, nil); !errors.Is(err, ErrQueueNotAllowed) {
		t.Fatalf("registry OpenJob unknown queue err = %v", err)
	}

	// Duplicate registration and overlapping spools are rejected.
	dup, err := NewQueue("queueA", fx.spoolA, fx.limits)
	if err != nil {
		t.Fatalf("NewQueue dup: %v", err)
	}
	if err := fx.registry.AddQueue(dup); !errors.Is(err, ErrQueueNotAllowed) {
		t.Fatalf("duplicate queue err = %v", err)
	}
	nested := filepath.Join(fx.spoolA, "nested")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	nestedQueue, err := NewQueue("nestedQ", nested, fx.limits)
	if err != nil {
		t.Fatalf("NewQueue nested: %v", err)
	}
	if err := fx.registry.AddQueue(nestedQueue); !errors.Is(err, ErrOutsideSpool) {
		t.Fatalf("overlapping spool err = %v", err)
	}
}

func TestSymlinkRejected(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	// A data file name that is actually a symlink to the outside canary.
	link := filepath.Join(fx.spoolA, "dfA002link")
	if err := os.Symlink(fx.canary, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	f, err := fx.queueA.OpenDataFile(ctx, "dfA002link")
	if f != nil {
		f.Close()
	}
	assertErrIs(t, err, ErrSymlink)

	// Symlinks pointing inside the spool are refused as well.
	innerLink := filepath.Join(fx.spoolA, "dfA003link")
	if err := os.Symlink(filepath.Join(fx.spoolA, dataName), innerLink); err != nil {
		t.Fatalf("inner symlink: %v", err)
	}
	f2, err := fx.queueA.OpenDataFile(ctx, "dfA003link")
	if f2 != nil {
		f2.Close()
	}
	assertErrIs(t, err, ErrSymlink)

	// Directories and other non-regular files are rejected.
	subdir := filepath.Join(fx.spoolA, "dfA004dir")
	if err := os.MkdirAll(subdir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f3, err := fx.queueA.OpenDataFile(ctx, "dfA004dir")
	if f3 != nil {
		f3.Close()
	}
	assertErrIs(t, err, ErrNotRegular)
}

func TestSpoolDirectorySwapRejected(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	// Rebuild queue A so its spool path becomes a symlink after
	// construction, simulating an attacker swapping the directory.
	swapRoot := filepath.Join(fx.root, "swap")
	if err := os.MkdirAll(swapRoot, 0o750); err != nil {
		t.Fatalf("mkdir swap: %v", err)
	}
	linkDir := filepath.Join(swapRoot, "spool-link")
	realDir := filepath.Join(swapRoot, "real-spool")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, dataName), []byte("x"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink spool: %v", err)
	}
	if _, err := NewQueue("swapped", linkDir, fx.limits); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked spool at construction err = %v", err)
	}

	// Swap an already-validated spool directory out from under the queue.
	mutable := filepath.Join(fx.root, "mutable-spool")
	if err := os.MkdirAll(mutable, 0o750); err != nil {
		t.Fatalf("mkdir mutable: %v", err)
	}
	q, err := NewQueue("mutable", mutable, fx.limits)
	if err != nil {
		t.Fatalf("NewQueue mutable: %v", err)
	}
	if err := os.Remove(mutable); err != nil {
		t.Fatalf("remove mutable: %v", err)
	}
	if err := os.Symlink(fx.canary, mutable); err != nil {
		t.Fatalf("replace with symlink: %v", err)
	}
	f, err := q.OpenDataFile(ctx, dataName)
	if f != nil {
		f.Close()
	}
	assertErrIs(t, err, ErrSymlink)
}

func TestQueueIsolation(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	// The same control name only exists in queue A's spool. Queue B must
	// never be able to open queue A's file, even if it admits the name.
	if err := fx.queueB.AllowControl(controlName); err != nil {
		t.Fatalf("AllowControl on B: %v", err)
	}
	f, err := fx.queueB.OpenControl(ctx, controlName)
	if f != nil {
		f.Close()
	}
	if err == nil {
		t.Fatalf("queue B opened queue A's control file")
	}

	// A data file placed in B is invisible to A through the component API.
	if err := os.WriteFile(filepath.Join(fx.spoolB, dataName), []byte("B-only"), 0o640); err != nil {
		t.Fatalf("write B data: %v", err)
	}
	fb, err := fx.queueB.OpenDataFile(ctx, dataName)
	if err != nil {
		t.Fatalf("queue B open own data: %v", err)
	}
	if got := readAll(t, fb); got != "B-only" {
		t.Fatalf("queue B data = %q", got)
	}
	fb.Close()

	// Queue A has its own distinct payload and cannot reach B's spool.
	fa, err := fx.queueA.OpenDataFile(ctx, dataName)
	if err != nil {
		t.Fatalf("queue A open own data: %v", err)
	}
	if got := readAll(t, fa); got != "job payload\n" {
		t.Fatalf("queue A data = %q", got)
	}
	fa.Close()

	// Every resolved path must have the owning queue spool as prefix.
	for _, q := range []*Queue{fx.queueA, fx.queueB} {
		opener := func(name string) string {
			g, err := q.OpenDataFile(ctx, name)
			if err != nil {
				return ""
			}
			defer g.Close()
			return filepath.Join(q.SpoolDir(), g.Name())
		}
		path := opener(dataName)
		if path != "" && !strings.HasPrefix(path, q.SpoolDir()+string(os.PathSeparator)) {
			t.Fatalf("resolved %q outside spool %q", path, q.SpoolDir())
		}
	}
}

func TestCanaryNeverReadable(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	attacks := []string{
		"../../../etc/service.conf",
		"../../etc/service.conf",
		"../etc/service.conf",
		fx.canary,
	}
	for _, name := range attacks {
		f, err := fx.queueA.OpenDataFile(ctx, name)
		if f != nil {
			data, _ := io.ReadAll(f)
			f.Close()
			if strings.Contains(string(data), secretToken) {
				t.Fatalf("attack %q leaked canary contents", name)
			}
			t.Fatalf("attack %q unexpectedly opened a file", name)
		}
		if err == nil {
			t.Fatalf("attack %q produced no error", name)
		}
	}
}

func TestInsecurePermissionsRejected(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	worldWritable := filepath.Join(fx.spoolA, "dfA005wide")
	if err := os.WriteFile(worldWritable, []byte("wide"), 0o666); err != nil {
		t.Fatalf("write world writable: %v", err)
	}
	if err := os.Chmod(worldWritable, 0o666); err != nil {
		t.Fatalf("chmod world writable: %v", err)
	}
	if mode := fileMode(t, worldWritable); mode&0o002 == 0 {
		t.Skipf("filesystem masks world-write bit (mode %v)", mode)
	}
	f, err := fx.queueA.OpenDataFile(ctx, "dfA005wide")
	if f != nil {
		f.Close()
	}
	assertErrIs(t, err, ErrInsecurePermission)

	groupWritable := filepath.Join(fx.spoolA, "dfA006grp")
	if err := os.WriteFile(groupWritable, []byte("grp"), 0o660); err != nil {
		t.Fatalf("write group writable: %v", err)
	}
	if err := os.Chmod(groupWritable, 0o660); err != nil {
		t.Fatalf("chmod group writable: %v", err)
	}
	if mode := fileMode(t, groupWritable); mode&0o020 == 0 {
		t.Skipf("filesystem masks group-write bit (mode %v)", mode)
	}
	f2, err := fx.queueA.OpenDataFile(ctx, "dfA006grp")
	if f2 != nil {
		f2.Close()
	}
	assertErrIs(t, err, ErrInsecurePermission)

	// An insecure spool directory is rejected at construction.
	wideDir := filepath.Join(fx.root, "wide-spool")
	if err := os.MkdirAll(wideDir, 0o777); err != nil {
		t.Fatalf("mkdir wide spool: %v", err)
	}
	if err := os.Chmod(wideDir, 0o777); err != nil {
		t.Fatalf("chmod wide spool: %v", err)
	}
	if mode := fileMode(t, wideDir); mode&0o022 == 0 {
		t.Skipf("filesystem masks writable bits on directories (mode %v)", mode)
	}
	if _, err := NewQueue("wideQ", wideDir, fx.limits); !errors.Is(err, ErrInsecurePermission) {
		t.Fatalf("wide spool dir err = %v", err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestJobAndFileLimitsEnforced(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx := context.Background()

	// File count limit.
	var many []string
	for i := 0; i < fx.limits.MaxFiles+1; i++ {
		name := "dfB" + string(rune('a'+i)) + "host"
		many = append(many, name)
		if err := os.WriteFile(filepath.Join(fx.spoolA, name), []byte("x"), 0o640); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, err := fx.queueA.OpenJob(ctx, controlName, many); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("file count limit err = %v", err)
	}

	// Per-file size limit.
	bigName := "dfBbig0host"
	big := make([]byte, fx.limits.MaxFileBytes+1)
	if err := os.WriteFile(filepath.Join(fx.spoolA, bigName), big, 0o640); err != nil {
		t.Fatalf("write big: %v", err)
	}
	f, err := fx.queueA.OpenDataFile(ctx, bigName)
	if f != nil {
		f.Close()
	}
	assertErrIs(t, err, ErrTooLarge)

	// Aggregate job size limit.
	smallLimits := Limits{MaxJobBytes: 4, MaxFileBytes: 64, MaxFiles: 8}
	q, err := NewQueue("small", fx.spoolA, smallLimits)
	if err != nil {
		t.Fatalf("NewQueue small: %v", err)
	}
	if err := q.AllowControl(controlName); err != nil {
		t.Fatalf("AllowControl: %v", err)
	}
	if _, err := q.OpenJob(ctx, controlName, []string{dataName}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("job size limit err = %v", err)
	}
}

func TestReadEnforcesPerFileCap(t *testing.T) {
	root := t.TempDir()
	spool := filepath.Join(root, "cap-spool")
	if err := os.MkdirAll(spool, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	name := "dfC001host"
	payload := strings.Repeat("z", 4)
	if err := os.WriteFile(filepath.Join(spool, name), []byte(payload), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	q, err := NewQueue("cap", spool, Limits{MaxFileBytes: 4})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	f, err := q.OpenDataFile(context.Background(), name)
	if err != nil {
		t.Fatalf("OpenDataFile: %v", err)
	}
	defer f.Close()

	// Simulate a TOCTOU growth: the file is within the limit at open time
	// but is appended afterwards. Reads must still stop at the cap.
	if err := os.WriteFile(filepath.Join(spool, name), []byte(strings.Repeat("z", 10)), 0o640); err != nil {
		t.Fatalf("append growth: %v", err)
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("read %d bytes, want 4 (per-file cap)", len(got))
	}
}

func TestCanceledContextRejected(t *testing.T) {
	fx := newSpoolFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fx.queueA.OpenDataFile(ctx, dataName); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OpenDataFile err = %v", err)
	}
	if _, err := fx.queueA.OpenJob(ctx, controlName, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OpenJob err = %v", err)
	}
}
