package entryadmit

import (
	"errors"
	"testing"
)

const (
	mod  = "example.com/foo"
	ver  = "v1.2.3"
	pref = mod + "@" + ver + "/"
)

func TestCheckAcceptsCleanArchive(t *testing.T) {
	names := []string{
		pref + "go.mod",
		pref + "go.sum",
		pref + "main.go",
		pref + "sub/pkg.go",
		pref + "sub/deep/file.txt",
	}
	if err := Check(mod, ver, names); err != nil {
		t.Fatalf("expected clean archive to pass, got %v", err)
	}
}

func TestCheckAcceptsPseudoVersion(t *testing.T) {
	version := "v0.0.0-20070101000000-deadbeefcafe"
	names := []string{
		mod + "@" + version + "/go.mod",
		mod + "@" + version + "/main.go",
	}
	if err := Check(mod, version, names); err != nil {
		t.Fatalf("pseudo-version archive rejected: %v", err)
	}
}

func TestCheckRejects(t *testing.T) {
	cases := []struct {
		name    string
		names   []string
		wantErr error
	}{
		{
			name:    "missing prefix",
			names:   []string{pref + "go.mod", "evil.txt"},
			wantErr: ErrBadPrefix,
		},
		{
			name:    "wrong module prefix",
			names:   []string{pref + "go.mod", "example.com/other@v1.0.0/x.go"},
			wantErr: ErrBadPrefix,
		},
		{
			name:    "bare prefix entry",
			names:   []string{pref + "go.mod", pref},
			wantErr: ErrInvalidPath,
		},
		{
			name:    "empty segment",
			names:   []string{pref + "go.mod", pref + "sub//file.go"},
			wantErr: ErrInvalidPath,
		},
		{
			name:    "dot segment",
			names:   []string{pref + "go.mod", pref + "./file.go"},
			wantErr: ErrInvalidPath,
		},
		{
			name:    "trailing slash",
			names:   []string{pref + "go.mod", pref + "dir/"},
			wantErr: ErrInvalidPath,
		},
		{
			name:    "backslash",
			names:   []string{pref + "go.mod", pref + `sub\file.go`},
			wantErr: ErrInvalidPath,
		},
		{
			name:    "windows traversal",
			names:   []string{pref + "go.mod", pref + `..\..\secret`},
			wantErr: ErrInvalidPath,
		},
		{
			name:    "dotdot traversal",
			names:   []string{pref + "go.mod", pref + "../secret"},
			wantErr: ErrTraversal,
		},
		{
			name:    "nested dotdot traversal",
			names:   []string{pref + "go.mod", pref + "sub/../../secret"},
			wantErr: ErrTraversal,
		},
		{
			name:    "case conflict files",
			names:   []string{pref + "go.mod", pref + "Readme.md", pref + "README.md"},
			wantErr: ErrCaseConflict,
		},
		{
			name:    "case conflict directories",
			names:   []string{pref + "go.mod", pref + "Sub/a.go", pref + "sUB/b.go"},
			wantErr: ErrCaseConflict,
		},
		{
			name: "same filename in different dirs",
			names: []string{
				pref + "go.mod",
				pref + "a/util.go",
				pref + "b/util.go",
			},
			wantErr: nil,
		},
		{
			name: "exact duplicate regular entry",
			names: []string{
				pref + "go.mod",
				pref + "main.go",
				pref + "main.go",
			},
			wantErr: ErrCaseConflict,
		},
		{
			name:    "root vendor file",
			names:   []string{pref + "go.mod", pref + "vendor/modules.txt"},
			wantErr: ErrVendor,
		},
		{
			name:    "nested vendor file",
			names:   []string{pref + "go.mod", pref + "sub/vendor/x.go"},
			wantErr: ErrVendor,
		},
		{
			name:    "nested go.mod",
			names:   []string{pref + "go.mod", pref + "sub/go.mod"},
			wantErr: ErrNestedGoMod,
		},
		{
			name: "file under nested go.mod",
			names: []string{
				pref + "go.mod",
				pref + "sub/go.mod",
				pref + "sub/pkg/file.go",
			},
			wantErr: ErrNestedGoMod,
		},
		{
			name: "nested go.mod arrives after subtree files",
			names: []string{
				pref + "go.mod",
				pref + "sub/pkg/file.go",
				pref + "sub/go.mod",
			},
			wantErr: ErrNestedGoMod,
		},
		{
			name: "deeply nested go.mod subtree",
			names: []string{
				pref + "go.mod",
				pref + "a/b/go.mod",
				pref + "a/b/c/d.go",
			},
			wantErr: ErrNestedGoMod,
		},
		{
			name:    "missing root go.mod",
			names:   []string{pref + "main.go"},
			wantErr: ErrRootGoMod,
		},
		{
			name:    "duplicate root go.mod exact",
			names:   []string{pref + "go.mod", pref + "go.mod"},
			wantErr: ErrCaseConflict,
		},
		{
			name:    "duplicate root go.mod case variant",
			names:   []string{pref + "go.mod", pref + "GO.MOD"},
			wantErr: ErrCaseConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(mod, ver, tc.names)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Check(%v) = %v, want nil", tc.names, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Check(%v) = nil, want error", tc.names)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Check(%v) = %v, want errors.Is(..., %v)", tc.names, err, tc.wantErr)
			}
			var entryErr *Error
			if !errors.As(err, &entryErr) {
				t.Fatalf("error %v is not *entryadmit.Error", err)
			}
		})
	}
}

func TestAdmitStickyAfterFailure(t *testing.T) {
	checker, err := New(mod, ver)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bad := pref + "../escape"
	first := checker.Admit(bad)
	if !errors.Is(first, ErrTraversal) {
		t.Fatalf("first Admit = %v, want ErrTraversal", first)
	}
	again := checker.Admit(pref + "main.go")
	if again != first {
		t.Fatalf("expected sticky error, got %v vs %v", first, again)
	}
	if err := checker.Close(); !errors.Is(err, ErrTraversal) {
		t.Fatalf("Close = %v, want ErrTraversal", err)
	}
}

func TestCaseConflictOrderIndependent(t *testing.T) {
	checker, err := New(mod, ver)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, n := range []string{pref + "go.mod", pref + "A/B.go", pref + "a/b.go"} {
		err := checker.Admit(n)
		if n == pref+"a/b.go" {
			if !errors.Is(err, ErrCaseConflict) {
				t.Fatalf("Admit(%q) = %v, want ErrCaseConflict", n, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Admit(%q) unexpected error: %v", n, err)
		}
	}
}

func TestCheckNameStateless(t *testing.T) {
	if err := CheckName(mod, ver, pref+"main.go"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	if err := CheckName(mod, ver, pref+"../x"); !errors.Is(err, ErrTraversal) {
		t.Fatalf("traversal name: %v", err)
	}
	if err := CheckName(mod, ver, "other@v1/x.go"); !errors.Is(err, ErrBadPrefix) {
		t.Fatalf("bad prefix name: %v", err)
	}
}

func TestNewRejectsBadCoordinates(t *testing.T) {
	bad := []struct {
		module  string
		version string
	}{
		{"", "v1.0.0"},
		{"example.com/../foo", "v1.0.0"},
		{mod, ""},
		{mod, "v1/0"},
		{mod, `v1\0`},
	}
	for _, tc := range bad {
		if _, err := New(tc.module, tc.version); err == nil {
			t.Fatalf("New(%q, %q) = nil, want error", tc.module, tc.version)
		}
	}
}

func TestErrorCarriesEntry(t *testing.T) {
	err := Check(mod, ver, []string{pref + "go.mod", pref + "sub//x"})
	var entryErr *Error
	if !errors.As(err, &entryErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if entryErr.Entry != pref+"sub//x" {
		t.Fatalf("Entry = %q, want %q", entryErr.Entry, pref+"sub//x")
	}
}
