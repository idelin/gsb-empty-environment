package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig(module Module) Config {
	if module.Name == "" {
		module.Name = "module"
	}
	if module.Root == "" && module.Path == "" {
		module.Root = "."
	}
	return Config{Modules: map[string]Module{module.Name: module}}
}

func TestModuleNameIsExactAndCaseSensitive(t *testing.T) {
	cfg := testConfig(Module{Name: "Docs"})
	request := Request{Module: "Docs", Path: "README"}

	if decision := Evaluate(cfg, request); !decision.Allowed {
		t.Fatalf("exact module name denied: %+v", decision)
	}
	for _, name := range []string{"docs", "DOCS", "Docs ", "Doc"} {
		decision := Evaluate(cfg, Request{Module: name, Path: "README"})
		if decision.Allowed || decision.Reason != ReasonUnknownModule {
			t.Fatalf("module name %q was not rejected: %+v", name, decision)
		}
	}
}

func TestHostsDenyWinsAndAllowListDefaultsToDeny(t *testing.T) {
	module := Module{
		Name:       "files",
		HostsAllow: []string{"10.0.0.0/8", "*.trusted.example"},
		HostsDeny:  []string{"10.1.2.3"},
	}
	cfg := testConfig(module)

	tests := []struct {
		host    string
		allowed bool
	}{
		{"10.1.2.3:873", false},
		{"10.2.3.4", true},
		{"192.168.1.1", false},
		{"build.trusted.example", true},
		{"BUILD.TRUSTED.EXAMPLE.", true},
		{"untrusted.example", false},
	}
	for _, test := range tests {
		decision := Evaluate(cfg, Request{Module: "files", Host: test.host})
		if decision.Allowed != test.allowed {
			t.Fatalf("host %q: allowed=%v, want %v (%+v)", test.host, decision.Allowed, test.allowed, decision)
		}
	}

	denyOnly := testConfig(Module{
		Name:      "files",
		HostsDeny: []string{"192.168.10.0/24"},
	})
	if decision := Evaluate(denyOnly, Request{Module: "files", Host: "192.168.20.5"}); !decision.Allowed {
		t.Fatalf("non-matching deny-only request denied: %+v", decision)
	}
	if decision := Evaluate(denyOnly, Request{Module: "files", Host: "192.168.10.9"}); decision.Allowed {
		t.Fatalf("deny match was allowed: %+v", decision)
	}
}

func TestHostsSupportNetmaskAndIPv6CIDR(t *testing.T) {
	cfg := testConfig(Module{
		Name:       "files",
		HostsAllow: []string{"10.1.0.0/255.255.0.0", "2001:db8::/32"},
	})
	tests := []struct {
		host    string
		allowed bool
	}{
		{"10.1.2.3", true},
		{"10.2.0.1", false},
		{"2001:db8::1", true},
		{"2001:db9::1", false},
	}
	for _, test := range tests {
		decision := Evaluate(cfg, Request{Module: "files", Host: test.host})
		if decision.Allowed != test.allowed {
			t.Fatalf("host %q: allowed=%v, want %v (%+v)", test.host, decision.Allowed, test.allowed, decision)
		}
	}
}

func TestRefusedOptionsCoverShortLongAndNoForms(t *testing.T) {
	cfg := testConfig(Module{
		Name:          "files",
		RefuseOptions: []string{"delete", "recursive", "archive", "compress"},
	})

	for _, options := range [][]string{
		{"--delete"},
		{"--delete-before"},
		{"--no-delete"},
		{"-r"},
		{"-rv"},
		{"--recursive"},
		{"--no-recursive"},
		{"--no-r"},
		{"-a"},
		{"-z"},
	} {
		decision := Evaluate(cfg, Request{Module: "files", Options: options})
		if decision.Allowed || decision.Reason != ReasonOptionRefused {
			t.Fatalf("options %v were not refused: %+v", options, decision)
		}
	}

	if decision := Evaluate(cfg, Request{Module: "files", Options: []string{"-R"}}); !decision.Allowed {
		t.Fatalf("case-distinct short option -R was refused as -r: %+v", decision)
	}
}

func TestRefusedOptionAcceptsShortConfigurationSpelling(t *testing.T) {
	cfg := testConfig(Module{
		Name:          "files",
		RefuseOptions: []string{"-r", "--no-archive", "delete"},
	})
	for _, option := range []string{"--recursive", "-a", "--delete-after"} {
		decision := Evaluate(cfg, Request{Module: "files", Options: []string{option}})
		if decision.Allowed {
			t.Fatalf("option %q was allowed despite refused aliases", option)
		}
	}
}

func TestNoShortOptionPreservesCaseAndOptionValuesAreNotFlags(t *testing.T) {
	cfg := testConfig(Module{
		Name:          "files",
		RefuseOptions: []string{"relative", "protect-args"},
	})
	if decision := Evaluate(cfg, Request{Module: "files", Options: []string{"--no-R"}}); decision.Allowed {
		t.Fatalf("--no-R did not refuse relative: %+v", decision)
	}
	// In -essh, the characters after e are the value for the rsh option, not
	// additional short flags such as s (protect-args).
	if decision := Evaluate(cfg, Request{Module: "files", Options: []string{"-essh"}}); !decision.Allowed {
		t.Fatalf("-essh was parsed as a protect-args flag: %+v", decision)
	}
}

func TestRequestPathCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(Module{Name: "files", Root: root})

	for _, requestPath := range []string{
		"../../etc/passwd",
		"foo/../../etc/passwd",
		"/etc/passwd",
		"..",
		"foo\\..\\..\\etc\\passwd",
	} {
		decision := Evaluate(cfg, Request{Module: "files", Path: requestPath})
		if decision.Allowed {
			t.Fatalf("escaping path %q was allowed", requestPath)
		}
	}

	for _, requestPath := range []string{"", ".", "foo/../bar", "dir/file.txt"} {
		decision := Evaluate(cfg, Request{Module: "files", Path: requestPath})
		if !decision.Allowed {
			t.Fatalf("safe path %q was denied: %+v", requestPath, decision)
		}
	}
}

func TestSymlinkOutsideRootIsRejectedWithoutChroot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "module")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "inside-link")); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(Module{Name: "files", Root: root, UseChroot: false})
	for _, requestPath := range []string{"outside-link", "outside-link/secret.txt"} {
		decision := Evaluate(cfg, Request{Module: "files", Path: requestPath})
		if decision.Allowed || decision.Reason != ReasonSymlinkEscape {
			t.Fatalf("path through outside symlink %q was allowed: %+v", requestPath, decision)
		}
	}

	for _, requestPath := range []string{"inside.txt", "inside-link"} {
		decision := Evaluate(cfg, Request{Module: "files", Path: requestPath})
		if !decision.Allowed {
			t.Fatalf("path through inside symlink %q was denied: %+v", requestPath, decision)
		}
	}
}

func TestRelativeSymlinkOutsideRootIsRejectedWithoutChroot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "module")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(Module{Name: "files", Root: root})
	decision := Evaluate(cfg, Request{Module: "files", Path: "escape/anything"})
	if decision.Allowed || decision.Reason != ReasonSymlinkEscape {
		t.Fatalf("relative symlink outside root was allowed: %+v", decision)
	}
}

func TestSymlinkWithParentComponentCannotBypassRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "module")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(Module{Name: "files", Root: root})
	decision := Evaluate(cfg, Request{Module: "files", Path: "outside-link/../secret.txt"})
	if decision.Allowed || decision.Reason != ReasonSymlinkEscape {
		t.Fatalf("symlink plus parent traversal was allowed: %+v", decision)
	}
}

func TestReadOnlyRejectsUploadsOnly(t *testing.T) {
	cfg := testConfig(Module{Name: "files", ReadOnly: true})

	if decision := Evaluate(cfg, Request{Module: "files", Upload: true}); decision.Allowed || decision.Reason != ReasonReadOnly {
		t.Fatalf("read-only upload was allowed: %+v", decision)
	}
	if decision := Evaluate(cfg, Request{Module: "files", Write: true}); decision.Allowed || decision.Reason != ReasonReadOnly {
		t.Fatalf("read-only write was allowed: %+v", decision)
	}
	if decision := Evaluate(cfg, Request{Module: "files"}); !decision.Allowed {
		t.Fatalf("read from read-only module was denied: %+v", decision)
	}

	writable := testConfig(Module{Name: "files", ReadOnly: false})
	if decision := Evaluate(writable, Request{Module: "files", Upload: true}); !decision.Allowed {
		t.Fatalf("upload to writable module was denied: %+v", decision)
	}
}

func TestExcludePatternsAndNegation(t *testing.T) {
	cfg := testConfig(Module{
		Name:    "files",
		Exclude: []string{"*.log", "private/**", "!private/keep.txt"},
	})
	tests := []struct {
		requestPath string
		excluded    bool
	}{
		{"debug.log", true},
		{"logs/debug.log", true},
		{"private/secret.txt", true},
		{"private/keep.txt", false},
		{"public/file.txt", false},
	}
	for _, test := range tests {
		decision := Evaluate(cfg, Request{Module: "files", Path: test.requestPath})
		if test.excluded && (decision.Allowed || decision.Reason != ReasonExcluded) {
			t.Fatalf("path %q was not excluded: %+v", test.requestPath, decision)
		}
		if !test.excluded && !decision.Allowed {
			t.Fatalf("path %q was unexpectedly excluded: %+v", test.requestPath, decision)
		}
	}
}

func TestAnchoredExcludeCoversDescendants(t *testing.T) {
	cfg := testConfig(Module{Name: "files", Exclude: []string{"/private"}})
	if decision := Evaluate(cfg, Request{Module: "files", Path: "private/secret.txt"}); decision.Allowed {
		t.Fatalf("descendant of anchored exclusion was allowed: %+v", decision)
	}
}

func TestCheckReturnsTypedDenial(t *testing.T) {
	cfg := testConfig(Module{Name: "files", ReadOnly: true})
	err := Check(cfg, Request{Module: "files", Upload: true})
	if err == nil {
		t.Fatal("expected denial")
	}
	var denial *DeniedError
	if !errors.As(err, &denial) || denial.Reason != ReasonReadOnly {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := Check(cfg, Request{Module: "files"}); err != nil {
		t.Fatalf("read request should pass: %v", err)
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfigString(`
# Global defaults are inherited by modules.
hosts deny = 0.0.0.0/8
refuse options = delete recursive
use chroot = no

[Files]
path = /srv/files # trailing comments are ignored
hosts allow = 10.0.0.0/8, *.example.com
hosts deny = 10.1.0.0/16
read only = yes
exclude = *.tmp private/**

[Upload]
path = /srv/upload
read only = no
use chroot = yes

[files]
path = /srv/case-distinct
`)
	if err != nil {
		t.Fatal(err)
	}

	files, ok := cfg.Modules["Files"]
	if !ok {
		t.Fatal("Files module missing")
	}
	if !files.ReadOnly || files.UseChroot {
		t.Fatalf("module defaults/overrides wrong: %+v", files)
	}
	if files.Root != "/srv/files" {
		t.Fatalf("inline comment was not stripped from path: %q", files.Root)
	}
	if got, want := strings.Join(files.HostsAllow, ","), "10.0.0.0/8,*.example.com"; got != want {
		t.Fatalf("hosts allow = %q, want %q", got, want)
	}
	if _, ok := cfg.Modules["files"]; !ok {
		t.Fatal("case-distinct lower-case module missing")
	}

	upload := cfg.Modules["Upload"]
	if upload.ReadOnly {
		t.Fatal("read only = no was not parsed")
	}
	if !upload.UseChroot {
		t.Fatal("use chroot = yes was not parsed")
	}

	decision := Evaluate(*cfg, Request{Module: "files", Host: "10.9.9.9"})
	if !decision.Allowed {
		t.Fatalf("lower-case module lookup failed: %+v", decision)
	}
	decision = Evaluate(*cfg, Request{Module: "Files", Host: "10.1.2.3"})
	if decision.Allowed || decision.Reason != ReasonHostDenied {
		t.Fatalf("deny should override allow: %+v", decision)
	}
}

func TestParseConfigEmptyModuleIsRetained(t *testing.T) {
	cfg, err := ParseConfigString("[Empty]\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Modules["Empty"]; !ok {
		t.Fatal("empty module was dropped")
	}
}
