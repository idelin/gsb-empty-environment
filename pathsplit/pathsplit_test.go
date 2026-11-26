package pathsplit

import (
	"errors"
	"testing"
)

func testFS() MapFS {
	return MapFS{
		"/":                   {Kind: KindDir},
		"/uploads":            {Kind: KindDir},
		"/uploads/avatar.jpg": {Kind: KindFile},
		"/uploads/shell.php":  {Kind: KindFile},
		"/uploads/shell.PHP":  {Kind: KindFile},
		"/images":             {Kind: KindDir},
		"/images/pic.jpg":     {Kind: KindFile},
		"/data":               {Kind: KindDir},
		"/data/middle.php":    {Kind: KindFile},
		"/app":                {Kind: KindDir},
		"/app/index.php":      {Kind: KindFile},
		"/app/upper.PHP":      {Kind: KindFile},
		"/app/space.php":      {Kind: KindFile},
		"/app/phpsdir.php":    {Kind: KindDir},
		"/app/sub":            {Kind: KindDir},
		"/app/sub/second.php": {Kind: KindFile},
		"/app/loop.php":       {Kind: KindSymlink, Target: "/app/loop.php"},
		"/app/a.php":          {Kind: KindSymlink, Target: "/app/b.php"},
		"/app/b.php":          {Kind: KindSymlink, Target: "/app/a.php"},
		"/app/intoupload.php": {Kind: KindSymlink, Target: "/uploads/shell.php"},
		"/app/rel.php":        {Kind: KindSymlink, Target: "../realdir/good.php"},
		"/realdir":            {Kind: KindDir},
		"/realdir/good.php":   {Kind: KindFile},
		"/realdir/escape.php": {Kind: KindSymlink, Target: "../../evil.php"},
		"/linkdir":            {Kind: KindSymlink, Target: "/realdir"},
	}
}

func testConfig() Config {
	return Config{
		FS:         testFS(),
		ScriptDirs: []string{"/app", "/realdir", "/linkdir"},
		UploadDirs: []string{"/uploads"},
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantErr      error
		wantScript   string
		wantResolved string
		wantPathInfo string
	}{
		// The avatar attack: avatar.jpg/x.php lives under an upload dir and the
		// x.php segment must never be turned into a script.
		{name: "avatar image then php under uploads", path: "/uploads/avatar.jpg/x.php", wantErr: ErrUploadDenied},
		// A real .php file inside an upload directory is still never executed.
		{name: "shell.php in uploads", path: "/uploads/shell.php", wantErr: ErrUploadDenied},
		{name: "shell.PHP uppercase in uploads", path: "/uploads/shell.PHP", wantErr: ErrUploadDenied},
		{name: "encoded dot shell in uploads", path: "/uploads/shell%2Ephp", wantErr: ErrUploadDenied},

		// Symlink loops are rejected.
		{name: "self symlink loop", path: "/app/loop.php", wantErr: ErrSymlinkLoop},
		{name: "mutual symlink loop", path: "/app/a.php", wantErr: ErrSymlinkLoop},

		// A symlink that ultimately points into an upload directory is denied.
		{name: "symlink points into uploads", path: "/app/intoupload.php", wantErr: ErrUploadDenied},

		// Escaping above the site root via ".." is rejected.
		{name: "symlink escapes site root", path: "/realdir/escape.php", wantErr: ErrOutsideRoot},

		// Unsafe leading path / PATH_INFO content.
		{name: "trailing encoded NUL", path: "/app/index.php%00", wantErr: ErrMalformedPath},
		{name: "NUL in PATH_INFO", path: "/app/index.php/foo%00bar", wantErr: ErrMalformedPath},
		{name: "dotdot segment", path: "/app/../app/index.php", wantErr: ErrMalformedPath},
		{name: "encoded dotdot segment", path: "/app/%2e%2e/index.php", wantErr: ErrMalformedPath},
		{name: "dotdot in PATH_INFO", path: "/app/index.php/../../etc", wantErr: ErrMalformedPath},
		{name: "raw backslash", path: `/app\index.php`, wantErr: ErrMalformedPath},
		{name: "encoded backslash", path: "/app/%5Cindex.php", wantErr: ErrMalformedPath},
		{name: "malformed percent encoding", path: "/app/%zz/index.php", wantErr: ErrMalformedPath},

		// Successful splits.
		{
			name:         "uppercase .PHP suffix",
			path:         "/app/upper.PHP",
			wantScript:   "/app/upper.PHP",
			wantResolved: "/app/upper.PHP",
		},
		{
			name:         "encoded trailing space normalized",
			path:         "/app/space.php%20",
			wantScript:   "/app/space.php",
			wantResolved: "/app/space.php",
		},
		{
			name:         "trailing dot normalized",
			path:         "/app/space.php.",
			wantScript:   "/app/space.php",
			wantResolved: "/app/space.php",
		},
		{
			name:         "script with PATH_INFO",
			path:         "/app/index.php/extra/path",
			wantScript:   "/app/index.php",
			wantResolved: "/app/index.php",
			wantPathInfo: "/extra/path",
		},
		{
			name:         "foremost candidate wins",
			path:         "/app/index.php/app/sub/second.php",
			wantScript:   "/app/index.php",
			wantResolved: "/app/index.php",
			wantPathInfo: "/app/sub/second.php",
		},
		{
			name:         "directory symlink resolved",
			path:         "/linkdir/good.php",
			wantScript:   "/linkdir/good.php",
			wantResolved: "/realdir/good.php",
		},
		{
			name:         "relative symlink target",
			path:         "/app/rel.php",
			wantScript:   "/app/rel.php",
			wantResolved: "/realdir/good.php",
		},

		// Non-runnable paths: fall back to static handling (nil, nil).
		{name: "no php segment", path: "/static/style.css"},
		{name: "php outside script whitelist", path: "/data/middle.php"},
		{name: "image then php outside whitelist", path: "/images/pic.jpg/x.php"},
		{name: "php-named directory not executed", path: "/app/phpsdir.php/whatever"},
		{name: "missing php candidate", path: "/app/missing.php"},
		{name: "root path", path: "/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(testConfig(), tc.path)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Split(%q) error = %v, want %v", tc.path, err, tc.wantErr)
				}
				if got != nil {
					t.Fatalf("Split(%q) = %+v, want nil result on error", tc.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Split(%q) unexpected error: %v", tc.path, err)
			}
			if tc.wantScript == "" {
				if got != nil {
					t.Fatalf("Split(%q) = %+v, want nil result (static fallback)", tc.path, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Split(%q) = nil, want script %q", tc.path, tc.wantScript)
			}
			if got.ScriptFilename != tc.wantScript {
				t.Errorf("ScriptFilename = %q, want %q", got.ScriptFilename, tc.wantScript)
			}
			if got.ScriptName != tc.wantScript {
				t.Errorf("ScriptName = %q, want %q", got.ScriptName, tc.wantScript)
			}
			if got.ResolvedPath != tc.wantResolved {
				t.Errorf("ResolvedPath = %q, want %q", got.ResolvedPath, tc.wantResolved)
			}
			if got.PathInfo != tc.wantPathInfo {
				t.Errorf("PathInfo = %q, want %q", got.PathInfo, tc.wantPathInfo)
			}
		})
	}
}

func TestSplitConfigValidation(t *testing.T) {
	if _, err := Split(Config{}, "/app/index.php"); err == nil {
		t.Fatal("expected error when Config.FS is nil")
	}
	cfg := testConfig()
	cfg.ScriptDirs = []string{`bad\dir`}
	if _, err := Split(cfg, "/app/index.php"); err == nil {
		t.Fatal("expected error for backslash in configured directory")
	}
}
