package netsec

import (
	"errors"
	"testing"
)

func TestSplitPathUploadedImageCannotBecomeScript(t *testing.T) {
	manifest := Manifest{
		"/uploads/avatar.jpg": {},
	}

	_, err := SplitPath(
		"/uploads/avatar.jpg/x.php",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}

func TestSplitPathUploadedPHPIsBlocked(t *testing.T) {
	manifest := Manifest{
		"/uploads/shell.php": {},
	}

	result, err := SplitPath(
		"/uploads/shell.php",
		manifest,
		[]string{"/uploads"},
		[]string{"/uploads"},
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
	if result.ScriptFilename != "" {
		t.Fatalf("expected no script filename, got %q", result.ScriptFilename)
	}
}

func TestSplitPathSymlinkIntoUploadsIsBlocked(t *testing.T) {
	manifest := Manifest{
		"/scripts/avatar.php": Symlink("../uploads/avatar.jpg"),
		"/uploads/avatar.jpg": {},
	}

	_, err := SplitPath(
		"/scripts/avatar.php/preview",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}

func TestSplitPathDirectorySymlinkIntoUploadsIsBlocked(t *testing.T) {
	manifest := Manifest{
		"/scripts/gallery":   Symlink("../uploads"),
		"/uploads/shell.php": {},
	}

	_, err := SplitPath(
		"/scripts/gallery/shell.php",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}

func TestSplitPathUppercasePHP(t *testing.T) {
	manifest := Manifest{
		"/scripts/index.PHP": {},
	}

	result, err := SplitPath(
		"/scripts/index.PHP/images/1",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if err != nil {
		t.Fatalf("SplitPath returned error: %v", err)
	}
	if result.ScriptFilename != "/scripts/index.PHP" {
		t.Fatalf("unexpected script filename: %q", result.ScriptFilename)
	}
	if result.PathInfo != "/images/1" {
		t.Fatalf("unexpected PATH_INFO: %q", result.PathInfo)
	}
}

func TestSplitPathPreservesTrailingSlashInPathInfo(t *testing.T) {
	manifest := Manifest{
		"/scripts/index.php": {},
	}

	result, err := SplitPath(
		"/scripts/index.php/",
		manifest,
		[]string{"/scripts"},
		nil,
	)
	if err != nil {
		t.Fatalf("SplitPath returned error: %v", err)
	}
	if result.PathInfo != "/" {
		t.Fatalf("unexpected PATH_INFO: %q", result.PathInfo)
	}
}

func TestSplitPathTrailingDotsAndSpaces(t *testing.T) {
	manifest := Manifest{
		"/scripts/index.PHP": {},
	}

	result, err := SplitPath(
		"/scripts/index.PHP.%20/preview",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if err != nil {
		t.Fatalf("SplitPath returned error: %v", err)
	}
	if result.ScriptFilename != "/scripts/index.PHP" {
		t.Fatalf("unexpected script filename: %q", result.ScriptFilename)
	}
	if result.PathInfo != "/preview" {
		t.Fatalf("unexpected PATH_INFO: %q", result.PathInfo)
	}
}

func TestSplitPathRejectsNULAndParentSegments(t *testing.T) {
	tests := []string{
		"/scripts/index.php/%00",
		"/scripts/index.php/%2e%2e/secret",
	}
	for _, requestPath := range tests {
		t.Run(requestPath, func(t *testing.T) {
			_, err := SplitPath(
				requestPath,
				Manifest{"/scripts/index.php": {}},
				[]string{"/scripts"},
				[]string{"/uploads"},
			)
			if !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("expected ErrInvalidPath, got %v", err)
			}
		})
	}
}

func TestSplitPathUsesEarliestEligibleCandidate(t *testing.T) {
	manifest := Manifest{
		"/scripts/front.php":           {},
		"/scripts/front.php/other.php": {},
	}

	result, err := SplitPath(
		"/scripts/front.php/other.php/deeper",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if err != nil {
		t.Fatalf("SplitPath returned error: %v", err)
	}
	if result.ScriptFilename != "/scripts/front.php" {
		t.Fatalf("unexpected script filename: %q", result.ScriptFilename)
	}
	if result.PathInfo != "/other.php/deeper" {
		t.Fatalf("unexpected PATH_INFO: %q", result.PathInfo)
	}
}

func TestSplitPathRequiresExistingScript(t *testing.T) {
	result, err := SplitPath(
		"/scripts/missing.php/other.php",
		Manifest{},
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if !errors.Is(err, ErrNotScript) {
		t.Fatalf("expected ErrNotScript, got %v", err)
	}
	if result != (SplitResult{}) {
		t.Fatalf("expected empty result, got %#v", result)
	}
}

func TestSplitPathSymlinkEscapingRootIsRejected(t *testing.T) {
	manifest := Manifest{
		"/scripts/index.php": Symlink("../../outside.php"),
	}

	_, err := SplitPath(
		"/scripts/index.php",
		manifest,
		[]string{"/scripts"},
		nil,
	)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath, got %v", err)
	}
}

func TestSplitPathSymlinkToAllowedScript(t *testing.T) {
	manifest := Manifest{
		"/scripts/alias.php": Symlink("real.php"),
		"/scripts/real.php":  {},
	}

	result, err := SplitPath(
		"/scripts/alias.php/path",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if err != nil {
		t.Fatalf("SplitPath returned error: %v", err)
	}
	if result.ScriptFilename != "/scripts/alias.php" {
		t.Fatalf("unexpected script filename: %q", result.ScriptFilename)
	}
	if result.ResolvedScript != "/scripts/real.php" {
		t.Fatalf("unexpected resolved script: %q", result.ResolvedScript)
	}
	if result.PathInfo != "/path" {
		t.Fatalf("unexpected PATH_INFO: %q", result.PathInfo)
	}
}

func TestSplitPathEncodedSlashStaysSubjectToUploadPolicy(t *testing.T) {
	manifest := Manifest{
		"/uploads/shell.php": {},
	}

	_, err := SplitPath(
		"/uploads%2fshell.php",
		manifest,
		[]string{"/uploads"},
		[]string{"/uploads"},
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}
