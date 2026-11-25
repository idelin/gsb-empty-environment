package pathsplit_test

import (
	"errors"
	"testing"

	"prescreen.local/ps-847c68412f/pathsplit"
)

func file() pathsplit.Entry {
	return pathsplit.Entry{Kind: pathsplit.RegularFile}
}

func dir() pathsplit.Entry {
	return pathsplit.Entry{Kind: pathsplit.Directory}
}

func link(target string) pathsplit.Entry {
	return pathsplit.Entry{Kind: pathsplit.Symlink, Target: target}
}

func newSplitter(t *testing.T, files pathsplit.Manifest, scriptDirs, uploadDirs []string) *pathsplit.Splitter {
	t.Helper()
	s, err := pathsplit.New(pathsplit.Config{
		Root:       "/srv/www",
		Files:      files,
		ScriptDirs: scriptDirs,
		UploadDirs: uploadDirs,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

func TestSplitScriptAndPathInfo(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":           dir(),
		"/app/index.php": file(),
	}, []string{"/app"}, nil)

	got, err := s.Split("/app/index.php/users/42")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got.ScriptFilename != "/srv/www/app/index.php" {
		t.Errorf("ScriptFilename = %q", got.ScriptFilename)
	}
	if got.PathInfo != "/users/42" {
		t.Errorf("PathInfo = %q", got.PathInfo)
	}
}

func TestAvatarPathInfoCannotExecuteUploadedFile(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/uploads":                  dir(),
		"/uploads/avatar.jpg":       file(),
		"/uploads/avatar.jpg/x.php": file(),
	}, []string{"/"}, []string{"/uploads"})

	_, err := s.Split("/uploads/avatar.jpg/x.php")
	if !errors.Is(err, pathsplit.ErrUploadPath) {
		t.Fatalf("Split() error = %v, want ErrUploadPath", err)
	}
}

func TestScriptInUploadDirectoryIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/uploads":           dir(),
		"/uploads/shell.php": file(),
	}, []string{"/"}, []string{"/uploads"})

	_, err := s.Split("/uploads/shell.php")
	if !errors.Is(err, pathsplit.ErrUploadPath) {
		t.Fatalf("Split() error = %v, want ErrUploadPath", err)
	}
}

func TestSymlinkLoopIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/loop.php":  link("/loop2.php"),
		"/loop2.php": link("/loop.php"),
	}, []string{"/"}, nil)

	_, err := s.Split("/loop.php/path")
	if !errors.Is(err, pathsplit.ErrSymlinkLoop) {
		t.Fatalf("Split() error = %v, want ErrSymlinkLoop", err)
	}
}

func TestFinalSymlinkToUploadIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/scripts":           dir(),
		"/scripts/shell.php": link("/uploads/shell.php"),
		"/uploads":           dir(),
		"/uploads/shell.php": file(),
	}, []string{"/scripts"}, []string{"/uploads"})

	_, err := s.Split("/scripts/shell.php")
	if !errors.Is(err, pathsplit.ErrUploadPath) {
		t.Fatalf("Split() error = %v, want ErrUploadPath", err)
	}
}

func TestIntermediateSymlinkToUploadIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/scripts":           dir(),
		"/scripts/up":        link("/uploads"),
		"/uploads":           dir(),
		"/uploads/shell.php": file(),
	}, []string{"/scripts"}, []string{"/uploads"})

	_, err := s.Split("/scripts/up/shell.php")
	if !errors.Is(err, pathsplit.ErrUploadPath) {
		t.Fatalf("Split() error = %v, want ErrUploadPath", err)
	}
}

func TestFinalSymlinkToUploadDirectoryIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/scripts":        dir(),
		"/scripts/up.php": link("/uploads"),
		"/uploads":        dir(),
	}, []string{"/scripts"}, []string{"/uploads"})

	_, err := s.Split("/scripts/up.php")
	if !errors.Is(err, pathsplit.ErrUploadPath) {
		t.Fatalf("Split() error = %v, want ErrUploadPath", err)
	}
}

func TestUppercasePHPSuffix(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":           dir(),
		"/app/index.PHP": file(),
	}, []string{"/app"}, nil)

	got, err := s.Split("/app/index.PHP/more")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got.ScriptFilename != "/srv/www/app/index.PHP" || got.PathInfo != "/more" {
		t.Fatalf("Split() = %+v", got)
	}
}

func TestTrailingDotsSpacesAndEncodedVariants(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":           dir(),
		"/app/index.php": file(),
	}, []string{"/app"}, nil)

	for _, requestPath := range []string{
		"/app/index.php%20",
		"/app/index.php.",
		"/app/index.php.%20",
		"/app/index.php%2E%2E%20",
		"/app/index%2Ephp/tail",
		"/app/index%2ephp.%20/tail",
	} {
		t.Run(requestPath, func(t *testing.T) {
			got, err := s.Split(requestPath)
			if err != nil {
				t.Fatalf("Split() error = %v", err)
			}
			if got.ScriptFilename != "/srv/www/app/index.php" {
				t.Fatalf("ScriptFilename = %q", got.ScriptFilename)
			}
		})
	}
}

func TestNULIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":           dir(),
		"/app/index.php": file(),
	}, []string{"/app"}, nil)

	_, err := s.Split("/app/index.php%00/tail")
	if !errors.Is(err, pathsplit.ErrUnsafePath) {
		t.Fatalf("Split() error = %v, want ErrUnsafePath", err)
	}
}

func TestDotDotAndBackslashTraversalAreRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":           dir(),
		"/app/index.php": file(),
	}, []string{"/app"}, nil)

	for _, requestPath := range []string{
		"/app/index.php/%2e%2e/secret",
		"/app/%2e%2e/app/index.php",
		"/app/index.php/%5c..%5csecret",
		"/app/index.php/..\\secret",
	} {
		t.Run(requestPath, func(t *testing.T) {
			_, err := s.Split(requestPath)
			if !errors.Is(err, pathsplit.ErrUnsafePath) {
				t.Fatalf("Split() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestEarliestEligibleCandidateWins(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":         dir(),
		"/app/one.php": file(),
		"/app/two.php": file(),
	}, []string{"/app"}, nil)

	got, err := s.Split("/app/one.php/two.php/more")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got.ScriptFilename != "/srv/www/app/one.php" || got.PathInfo != "/two.php/more" {
		t.Fatalf("Split() = %+v", got)
	}
}

func TestScriptOutsideAllowlistIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":           dir(),
		"/app/index.php": file(),
	}, []string{"/scripts"}, nil)

	_, err := s.Split("/app/index.php")
	if !errors.Is(err, pathsplit.ErrScriptNotAllowed) {
		t.Fatalf("Split() error = %v, want ErrScriptNotAllowed", err)
	}
}

func TestSymlinkOutsideRootIsRejected(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":         dir(),
		"/app/bad.php": link("../../etc/passwd"),
	}, []string{"/app"}, nil)

	_, err := s.Split("/app/bad.php")
	if !errors.Is(err, pathsplit.ErrOutsideRoot) {
		t.Fatalf("Split() error = %v, want ErrOutsideRoot", err)
	}
}

func TestFinalSymlinkToAllowedScriptUsesResolvedFilename(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/app":              dir(),
		"/app/alias.php":    link("/scripts/real.php"),
		"/scripts":          dir(),
		"/scripts/real.php": file(),
	}, []string{"/scripts"}, nil)

	got, err := s.Split("/app/alias.php/extra")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got.ScriptFilename != "/srv/www/scripts/real.php" || got.PathInfo != "/extra" {
		t.Fatalf("Split() = %+v", got)
	}
}

func TestSymlinkedScriptDirectoryAllowlist(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/public":        link("/app"),
		"/app":           dir(),
		"/app/index.php": file(),
	}, []string{"/public"}, nil)

	got, err := s.Split("/public/index.php")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got.ScriptFilename != "/srv/www/app/index.php" {
		t.Fatalf("ScriptFilename = %q", got.ScriptFilename)
	}
}

func TestNoCandidate(t *testing.T) {
	s := newSplitter(t, pathsplit.Manifest{
		"/uploads":            dir(),
		"/uploads/avatar.jpg": file(),
	}, []string{"/"}, []string{"/not-this-dir"})

	_, err := s.Split("/uploads/avatar.jpg")
	if !errors.Is(err, pathsplit.ErrNoScript) {
		t.Fatalf("Split() error = %v, want ErrNoScript", err)
	}
}
