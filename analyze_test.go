package netsec

import (
	"debug/elf"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func directory(mode fs.FileMode, uid uint32) FileInfo {
	return FileInfo{Mode: fs.ModeDir | mode, UID: uid, GID: uid}
}

func regular(mode fs.FileMode, uid uint32) FileInfo {
	return FileInfo{Mode: mode, UID: uid, GID: uid}
}

func standardView() MapFileView {
	return MapFileView{
		"/":              directory(0o755, 0),
		"/opt":           directory(0o755, 0),
		"/opt/app":       directory(0o755, 0),
		"/opt/app/bin":   directory(0o755, 0),
		"/opt/app/lib":   directory(0o755, 0),
		"/usr":           directory(0o755, 0),
		"/usr/lib":       directory(0o755, 0),
		"/usr/local":     directory(0o755, 0),
		"/usr/local/lib": directory(0o755, 0),
	}
}

func issueCode(dependency Dependency, code IssueCode) bool {
	for _, issue := range dependency.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestWritableRPATHIsRejected(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:   []string{"libevil.so"},
		HasRPATH: true,
		RPATH:    "/tmp/evil",
	})
	view := standardView()
	view["/tmp"] = directory(0o777, 0)
	view["/tmp/evil"] = directory(0o755, 0)
	view["/tmp/evil/libevil.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{Files: view, Origin: "/opt/app/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Safe {
		t.Fatal("writable RPATH was accepted")
	}
	if len(report.Needed) != 1 {
		t.Fatalf("got %d dependencies, want 1", len(report.Needed))
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Path != "/tmp/evil/libevil.so" || dependency.Source != SourceRPATH {
		t.Fatalf("unexpected resolution: %+v", dependency)
	}
	if !issueCode(dependency, IssueWorldWritable) {
		t.Fatalf("missing world-writable issue: %+v", dependency.Issues)
	}
}

func TestWritablePathBeforeSafeCacheHitIsRejected(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:   []string{"libfoo.so"},
		HasRPATH: true,
		RPATH:    "/tmp/evil",
	})
	view := standardView()
	view["/tmp"] = directory(0o777, 0)
	view["/tmp/evil"] = directory(0o755, 0)
	view["/usr/local/lib/libfoo.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files: view,
		Cache: []CacheEntry{{Name: "libfoo.so", Path: "/usr/local/lib/libfoo.so"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Source != SourceCache {
		t.Fatalf("cache hit was not used: %+v", dependency)
	}
	if dependency.Safe || report.Safe {
		t.Fatal("safe resolution incorrectly hid the earlier writable RPATH")
	}
	if !issueCode(dependency, IssueWorldWritable) {
		t.Fatalf("missing writable-path issue: %+v", dependency.Issues)
	}
	if dependency.Search[0].Source != SourceRPATH || dependency.Search[0].Exists {
		t.Fatalf("RPATH should have been checked first: %+v", dependency.Search)
	}
}

func TestRelativeRPATHIsRejectedEvenWhenItLandsInSafeDirectory(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:   []string{"librel.so"},
		HasRPATH: true,
		RPATH:    "../lib",
	})
	view := standardView()
	view["/opt/app/lib/librel.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:      view,
		WorkingDir: "/opt/app/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Path != "/opt/app/lib/librel.so" {
		t.Fatalf("unexpected resolution: %+v", dependency)
	}
	if !issueCode(dependency, IssueRelativePath) || dependency.Safe {
		t.Fatalf("relative RPATH was accepted: %+v", dependency)
	}
}

func TestEmptyRPATHElementMeansWorkingDirectory(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:   []string{"libcwd.so"},
		HasRPATH: true,
		RPATH:    "",
	})
	view := standardView()
	view["/opt/app/bin/libcwd.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:      view,
		WorkingDir: "/opt/app/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Path != "/opt/app/bin/libcwd.so" {
		t.Fatalf("empty RPATH element did not resolve through cwd: %+v", dependency)
	}
	if !issueCode(dependency, IssueRelativePath) || dependency.Safe {
		t.Fatalf("empty RPATH element was accepted: %+v", dependency)
	}
}

func TestOriginPointingAtWritableDirectoryIsRejected(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:     []string{"liborigin.so"},
		HasRUNPATH: true,
		RUNPATH:    "$ORIGIN",
	})
	view := standardView()
	view["/home"] = directory(0o755, 0)
	view["/home/build"] = directory(0o755, 1000)
	view["/home/build/bin"] = directory(0o755, 1000)
	view["/home/build/bin/liborigin.so"] = regular(0o644, 1000)

	report, err := Analyze(elfPath, Options{
		Files:  view,
		Origin: "/home/build/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Needed) != 1 {
		t.Fatalf("got %d dependencies, want 1", len(report.Needed))
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Path != "/home/build/bin/liborigin.so" {
		t.Fatalf("unexpected resolution: %+v", dependency)
	}
	if !issueCode(dependency, IssueUntrustedOwner) || dependency.Safe {
		t.Fatalf("writable $ORIGIN was accepted: %+v", dependency)
	}
}

func TestSafeOriginLayoutIsAccepted(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:     []string{"libsafe.so"},
		HasRUNPATH: true,
		RUNPATH:    "${ORIGIN}/../lib",
	})
	view := standardView()
	view["/opt/app/lib/libsafe.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{Files: view, Origin: "/opt/app/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Safe {
		t.Fatalf("safe layout was rejected: %+v", report.Needed)
	}
	dependency := report.Needed[0]
	if dependency.Path != "/opt/app/lib/libsafe.so" || dependency.Source != SourceRUNPATH {
		t.Fatalf("unexpected resolution: %+v", dependency)
	}
	if len(dependency.Issues) != 0 {
		t.Fatalf("unexpected issues: %+v", dependency.Issues)
	}
}

func TestRUNPATHSuppressesRPATH(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:     []string{"libsafe.so"},
		HasRPATH:   true,
		RPATH:      "/tmp/evil",
		HasRUNPATH: true,
		RUNPATH:    "$ORIGIN/../lib",
	})
	view := standardView()
	view["/tmp"] = directory(0o777, 0)
	view["/tmp/evil"] = directory(0o755, 0)
	view["/tmp/evil/libsafe.so"] = regular(0o644, 0)
	view["/opt/app/lib/libsafe.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{Files: view, Origin: "/opt/app/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Safe {
		t.Fatalf("RUNPATH should suppress RPATH: %+v", report.Needed)
	}
	dependency := report.Needed[0]
	if dependency.Source != SourceRUNPATH || dependency.Path != "/opt/app/lib/libsafe.so" {
		t.Fatalf("unexpected resolution: %+v", dependency)
	}
	if len(dependency.Search) != 1 {
		t.Fatalf("RPATH directory was searched despite RUNPATH: %+v", dependency.Search)
	}
}

func TestCachePrecedesDefaultDirectory(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{Needed: []string{"libpick.so"}})
	view := standardView()
	view["/usr/local/lib/libpick.so"] = regular(0o644, 0)
	view["/opt/app/lib/libpick.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:       view,
		Cache:       []CacheEntry{{Name: "libpick.so", Path: "/usr/local/lib/libpick.so"}},
		DefaultDirs: []string{"/opt/app/lib"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if dependency.Source != SourceCache || dependency.Path != "/usr/local/lib/libpick.so" {
		t.Fatalf("cache did not precede defaults: %+v", dependency)
	}
	if !report.Safe {
		t.Fatalf("safe cache hit rejected: %+v", dependency.Issues)
	}
}

func TestDefaultDirectoryUsedAfterCacheMiss(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{Needed: []string{"libfallback.so"}})
	view := standardView()
	view["/opt/app/lib/libfallback.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:       view,
		Cache:       []CacheEntry{{Name: "libfallback.so", Path: "/usr/local/lib/absent.so"}},
		DefaultDirs: []string{"/opt/app/lib"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Source != SourceDefault || dependency.Path != "/opt/app/lib/libfallback.so" {
		t.Fatalf("default fallback was not used: %+v", dependency)
	}
	if !report.Safe {
		t.Fatalf("safe default hit rejected: %+v", dependency.Issues)
	}
}

func TestSlashNeededPathBypassesSearchDirectories(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:   []string{"../lib/libdirect.so"},
		HasRPATH: true,
		RPATH:    "/usr/local/lib",
	})
	view := standardView()
	view["/opt/app/lib/libdirect.so"] = regular(0o644, 0)
	view["/usr/local/lib/libdirect.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:      view,
		WorkingDir: "/opt/app/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if !dependency.Resolved || dependency.Source != SourceDirect || dependency.Path != "/opt/app/lib/libdirect.so" {
		t.Fatalf("slash path did not bypass RPATH: %+v", dependency)
	}
	if !issueCode(dependency, IssueRelativePath) || dependency.Safe {
		t.Fatalf("relative direct dependency was accepted: %+v", dependency)
	}
	if len(dependency.Search) != 1 {
		t.Fatalf("RPATH was searched for a slash path: %+v", dependency.Search)
	}
}

func TestNoDefaultLibsSkipsDefaults(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed: []string{"libdefault.so"},
		Flags1: uint64(elf.DF_1_NODEFLIB),
	})
	view := standardView()
	view["/opt/app/lib/libdefault.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:       view,
		DefaultDirs: []string{"/opt/app/lib"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if dependency.Resolved {
		t.Fatalf("DF_1_NODEFLIB was ignored: %+v", dependency)
	}
	if !issueCode(dependency, IssueMissingDependency) {
		t.Fatalf("missing dependency issue not reported: %+v", dependency.Issues)
	}
}

func TestPreloadEntriesUseSameResolutionAndPermissionChecks(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{})
	view := standardView()
	view["/usr/local/lib/libcached.so"] = regular(0o644, 0)
	view["/opt/app/lib/libpreload.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:       view,
		Cache:       []CacheEntry{{Name: "libcached.so", Path: "/usr/local/lib/libcached.so"}},
		Preload:     ParsePreloadList([]byte("libcached.so\n/opt/app/lib/libpreload.so\n")),
		DefaultDirs: []string{"/usr/lib"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Preload) != 2 {
		t.Fatalf("got %d preload entries, want 2", len(report.Preload))
	}
	if !report.Preload[0].Resolved || report.Preload[0].Source != SourceCache {
		t.Fatalf("cache preload was not resolved: %+v", report.Preload[0])
	}
	if !report.Preload[1].Resolved || report.Preload[1].Source != SourceDirect {
		t.Fatalf("direct preload was not resolved: %+v", report.Preload[1])
	}
	if !report.Safe {
		t.Fatalf("safe preload layout rejected: %+v", report.Preload)
	}
}

func TestUnsafePreloadIsRejected(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{})
	view := standardView()
	view["/tmp"] = directory(0o1777, 0)
	view["/tmp/evil"] = directory(0o755, 0)
	view["/tmp/evil/libpreload.so"] = regular(0o644, 0)

	report, err := Analyze(elfPath, Options{
		Files:   view,
		Preload: []string{"/tmp/evil/libpreload.so"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Safe || report.Preload[0].Safe {
		t.Fatalf("unsafe preload accepted: %+v", report.Preload[0])
	}
	if !issueCode(report.Preload[0], IssueWorldWritable) {
		t.Fatalf("missing writable issue: %+v", report.Preload[0].Issues)
	}
}

func TestUntrustedLibraryOwnerIsRejected(t *testing.T) {
	elfPath := writeTestELF(t, testELFSpec{
		Needed:     []string{"libowned.so"},
		HasRUNPATH: true,
		RUNPATH:    "$ORIGIN/../lib",
	})
	view := standardView()
	view["/opt/app/lib/libowned.so"] = regular(0o444, 1000)

	report, err := Analyze(elfPath, Options{Files: view, Origin: "/opt/app/bin"})
	if err != nil {
		t.Fatal(err)
	}
	dependency := report.Needed[0]
	if dependency.Safe || !issueCode(dependency, IssueUntrustedOwner) {
		t.Fatalf("untrusted library owner accepted: %+v", dependency)
	}
}

func TestReadELFFallsBackToProgramHeaders(t *testing.T) {
	elfPath := writeSectionlessTestELF(t, "libstripped.so", "$ORIGIN/../lib")
	meta, err := ReadELF(elfPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Needed) != 1 || meta.Needed[0] != "libstripped.so" {
		t.Fatalf("unexpected DT_NEEDED: %+v", meta.Needed)
	}
	if !meta.HasRUNPATH || len(meta.RUNPATH) != 1 || meta.RUNPATH[0] != "$ORIGIN/../lib" {
		t.Fatalf("unexpected DT_RUNPATH: %+v", meta)
	}
}

func TestReadPreloadList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ld.so.preload")
	if err := os.WriteFile(path, []byte("  libone.so\n\tlibtwo.so  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadPreloadList(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != "libone.so" || entries[1] != "libtwo.so" {
		t.Fatalf("unexpected preload entries: %q", entries)
	}
}
