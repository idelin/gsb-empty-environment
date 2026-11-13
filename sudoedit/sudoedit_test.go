package sudoedit

import (
	"strings"
	"testing"
)

const (
	allowedVim  = "/usr/bin/vim"
	allowedNano = "/usr/bin/nano"
	targetFile  = "/etc/sudoers.d/role"
	shadowFile  = "/etc/shadow"
)

// fakeInspector treats the listed paths as unsafe (symlink or writable).
type fakeInspector struct {
	symlinks map[string]bool
	writable map[string]bool
}

func (f fakeInspector) IsSymlink(path string) bool { return f.symlinks[path] }

func (f fakeInspector) IsWritable(path string) bool { return f.writable[path] }

func safeInspector() fakeInspector {
	return fakeInspector{
		symlinks: map[string]bool{},
		writable: map[string]bool{},
	}
}

func baseRequest(editor string) Request {
	return Request{
		Env:             map[string]string{"EDITOR": editor},
		Targets:         []string{targetFile},
		Authorized:      []string{targetFile},
		EditorAllowlist: map[string]bool{allowedVim: true, allowedNano: true},
		Inspector:       safeInspector(),
	}
}

// A legitimate absolute editor with an authorized, safe target is allowed.
func TestAuthorize_LegitimateEdit(t *testing.T) {
	req := baseRequest(allowedVim)

	decision := Authorize(req)
	if !decision.Allowed {
		t.Fatalf("expected allowed, got reason %s: %v", decision.Reason, decision.Err)
	}
	if decision.Editor != allowedVim {
		t.Fatalf("expected editor %q, got %q", allowedVim, decision.Editor)
	}
	if len(decision.Files) != 1 || decision.Files[0] != targetFile {
		t.Fatalf("expected single target %q, got %v", targetFile, decision.Files)
	}
}

// The original attack: EDITOR adds an unauthorized second file after "--".
func TestAuthorize_EDITORWithDoubleDashInjectsUnauthorizedFile(t *testing.T) {
	req := baseRequest(allowedVim + " -- " + shadowFile)

	decision := Authorize(req)
	if decision.Allowed {
		t.Fatalf("expected denial for injected %q", shadowFile)
	}
	if decision.Reason != ReasonUnauthorizedFile {
		t.Fatalf("expected reason %q, got %q", ReasonUnauthorizedFile, decision.Reason)
	}
	if decision.DeniedPath != shadowFile {
		t.Fatalf("expected denied path %q, got %q", shadowFile, decision.DeniedPath)
	}

	// The same injected file is allowed once it is in the authorized set.
	req.Authorized = []string{targetFile, shadowFile}
	if got := Authorize(req); !got.Allowed {
		t.Fatalf("expected allowed when both files are authorized, got %v", got.Err)
	} else if len(got.Files) != 2 {
		t.Fatalf("expected two edited files, got %v", got.Files)
	}
}

// A positional-looking argument before "--" is also an extra file.
func TestAuthorize_PositionalEditorArgumentIsExtraFile(t *testing.T) {
	req := baseRequest(allowedVim + " -n " + shadowFile)

	decision := Authorize(req)
	if decision.Allowed || decision.Reason != ReasonUnauthorizedFile ||
		decision.DeniedPath != shadowFile {
		t.Fatalf("expected unauthorized %q, got allowed=%v reason=%q path=%q",
			shadowFile, decision.Allowed, decision.Reason, decision.DeniedPath)
	}
}

// A symlinked parent directory makes every target beneath it unsafe.
func TestAuthorize_SymlinkParentDirectoryDenied(t *testing.T) {
	req := baseRequest(allowedVim)
	req.Inspector = fakeInspector{
		symlinks: map[string]bool{"/etc/sudoers.d": true},
		writable: map[string]bool{},
	}

	decision := Authorize(req)
	if decision.Allowed {
		t.Fatalf("expected denial for symlinked parent directory")
	}
	if decision.Reason != ReasonUnsafePath || decision.DeniedPath != "/etc/sudoers.d" {
		t.Fatalf("expected unsafe parent, got reason=%q path=%q",
			decision.Reason, decision.DeniedPath)
	}
}

// A writable target itself is rejected.
func TestAuthorize_WritableTargetDenied(t *testing.T) {
	req := baseRequest(allowedVim)
	req.Inspector = fakeInspector{
		symlinks: map[string]bool{},
		writable: map[string]bool{targetFile: true},
	}

	decision := Authorize(req)
	if decision.Allowed || decision.Reason != ReasonUnsafePath ||
		decision.DeniedPath != targetFile {
		t.Fatalf("expected writable target denial, got allowed=%v reason=%q path=%q",
			decision.Allowed, decision.Reason, decision.DeniedPath)
	}
}

// ".." segments are rejected before lexical normalization.
func TestAuthorize_PathTraversalDenied(t *testing.T) {
	cases := map[string]string{
		"target traversal":   "/etc/sudoers.d/../shadow",
		"injected traversal": allowedVim + " -- /etc/./../etc/shadow",
		"leading traversal":  "/../etc/shadow",
		"normalized bypass":  "/etc/sudoers.d/../../etc/shadow",
	}

	for name, editorOrTarget := range cases {
		t.Run(name, func(t *testing.T) {
			req := baseRequest(allowedVim)
			if name == "target traversal" || name == "leading traversal" ||
				name == "normalized bypass" {
				req.Targets = []string{editorOrTarget}
			} else {
				req.Env["EDITOR"] = editorOrTarget
			}

			decision := Authorize(req)
			if decision.Allowed || decision.Reason != ReasonPathTraversal {
				t.Fatalf("expected path-traversal denial, got allowed=%v reason=%q",
					decision.Allowed, decision.Reason)
			}
		})
	}
}

// SUDO_EDITOR beats VISUAL beats EDITOR; only the first non-empty one counts.
func TestAuthorize_EditorVariablePrecedence(t *testing.T) {
	t.Run("SUDO_EDITOR wins over VISUAL and EDITOR", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Env = map[string]string{
			"SUDO_EDITOR": allowedNano,
			"VISUAL":      allowedVim,
			"EDITOR":      "vim",
		}

		decision := Authorize(req)
		if !decision.Allowed {
			t.Fatalf("expected allowed: %v", decision.Err)
		}
		if decision.Editor != allowedNano {
			t.Fatalf("expected %q, got %q", allowedNano, decision.Editor)
		}
	})

	t.Run("VISUAL wins over EDITOR when SUDO_EDITOR is empty", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Env = map[string]string{
			"SUDO_EDITOR": "",
			"VISUAL":      allowedNano,
			"EDITOR":      allowedVim,
		}

		decision := Authorize(req)
		if !decision.Allowed || decision.Editor != allowedNano {
			t.Fatalf("expected VISUAL editor %q, got allowed=%v editor=%q",
				allowedNano, decision.Allowed, decision.Editor)
		}
	})

	t.Run("EDITOR is the final fallback", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Env = map[string]string{"EDITOR": allowedVim}

		if got := Authorize(req); !got.Allowed || got.Editor != allowedVim {
			t.Fatalf("expected EDITOR fallback, got allowed=%v editor=%q",
				got.Allowed, got.Editor)
		}
	})

	t.Run("no variable set is denied", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Env = map[string]string{}

		got := Authorize(req)
		if got.Allowed || got.Reason != ReasonNoEditor {
			t.Fatalf("expected no-editor denial, got allowed=%v reason=%q",
				got.Allowed, got.Reason)
		}
	})
}

// The editor must be an absolute allowlist member; PATH lookup never happens.
func TestAuthorize_EditorMustBeAbsoluteAndAllowed(t *testing.T) {
	t.Run("relative editor is not resolved via PATH", func(t *testing.T) {
		req := baseRequest("vim")
		got := Authorize(req)
		if got.Allowed || got.Reason != ReasonEditorNotAbsolute {
			t.Fatalf("expected absolute-path denial, got allowed=%v reason=%q",
				got.Allowed, got.Reason)
		}
	})

	t.Run("absolute editor outside allowlist is denied", func(t *testing.T) {
		req := baseRequest("/usr/local/bin/pwn")
		got := Authorize(req)
		if got.Allowed || got.Reason != ReasonEditorNotAllowed {
			t.Fatalf("expected allowlist denial, got allowed=%v reason=%q",
				got.Allowed, got.Reason)
		}
	})

	t.Run("whitespace-separated first field is the executable", func(t *testing.T) {
		req := baseRequest("  " + allowedVim + " -n  ")
		got := Authorize(req)
		if !got.Allowed || got.Editor != allowedVim {
			t.Fatalf("expected allowed %q, got allowed=%v editor=%q err=%v",
				allowedVim, got.Allowed, got.Editor, got.Err)
		}
	})
}

// Splitting happens before authorization: an editor failure must surface
// even when every target file is otherwise authorized and safe.
func TestAuthorize_SplittingPrecedesAuthorization(t *testing.T) {
	req := Request{
		Env:             map[string]string{"EDITOR": "vim -- " + shadowFile},
		Targets:         []string{targetFile},
		Authorized:      []string{targetFile},
		EditorAllowlist: map[string]bool{allowedVim: true},
		Inspector:       safeInspector(),
	}

	got := Authorize(req)
	if got.Allowed || got.Reason != ReasonEditorNotAbsolute {
		t.Fatalf("expected editor validation before file checks, got allowed=%v reason=%q",
			got.Allowed, got.Reason)
	}
}

// Directory-prefix authorization and lexical normalization behave as specified.
func TestAuthorize_PrefixAndNormalization(t *testing.T) {
	t.Run("directory prefix authorizes contents", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Targets = []string{"/etc/sudoers.d/nested/file.conf"}
		req.Authorized = []string{"/etc/sudoers.d/"}

		got := Authorize(req)
		if !got.Allowed {
			t.Fatalf("expected prefix match: %v", got.Err)
		}
	})

	t.Run("prefix does not match sibling directory", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Targets = []string{"/etc/sudoers.d.backup/file"}
		req.Authorized = []string{"/etc/sudoers.d/"}

		got := Authorize(req)
		if got.Allowed || got.Reason != ReasonUnauthorizedFile {
			t.Fatalf("expected unauthorized sibling, got allowed=%v reason=%q",
				got.Allowed, got.Reason)
		}
	})

	t.Run("redundant slashes and dots normalize before matching", func(t *testing.T) {
		req := baseRequest(allowedVim)
		req.Targets = []string{"/etc/sudoers.d/./role"}

		got := Authorize(req)
		if !got.Allowed {
			t.Fatalf("expected normalized match: %v", got.Err)
		}
		if len(got.Files) != 1 || got.Files[0] != targetFile {
			t.Fatalf("expected normalized target %q, got %v", targetFile, got.Files)
		}
	})

	t.Run("relative extra file cannot be matched", func(t *testing.T) {
		req := baseRequest(allowedVim + " -- relative/file")
		got := Authorize(req)
		if got.Allowed || got.Reason != ReasonPathNotAbsolute {
			t.Fatalf("expected non-absolute denial, got allowed=%v reason=%q",
				got.Allowed, got.Reason)
		}
	})
}

func TestDenialError(t *testing.T) {
	req := baseRequest(allowedVim + " -- " + shadowFile)
	got := Authorize(req)

	var denial *DenialError
	if got.Err == nil {
		t.Fatalf("expected denial error")
	}
	if !asDenial(got.Err, &denial) {
		t.Fatalf("expected *DenialError, got %T", got.Err)
	}
	if denial.Reason != ReasonUnauthorizedFile || !strings.Contains(denial.Error(), shadowFile) {
		t.Fatalf("unexpected denial: %+v", denial)
	}
}

func asDenial(err error, target **DenialError) bool {
	denial, ok := err.(*DenialError)
	if !ok {
		return false
	}
	*target = denial
	return true
}
