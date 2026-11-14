package depverify

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func sortStrings(s []string) { sort.Strings(s) }

// verifyLocal handles a filesystem replacement. Local replacements never touch
// the proxy and do not need go.sum entries, but they must be explicitly
// registered by both module path and exact version.
func (v *Verifier) verifyLocal(req Requirement, rep Replacement, vm *VerifiedModule) (*VerifiedModule, error) {
	key := localKey{path: rep.OldPath, version: rep.OldVersion}
	reg, ok := v.localAllow[key]
	if !ok {
		return nil, &ModuleError{Path: rep.OldPath, Version: rep.OldVersion,
			Err: fmt.Errorf("%w: %s@%s -> %s is not listed in LocalReplacements",
				ErrLocalNotRegistered, rep.OldPath, rep.OldVersion, rep.NewPath)}
	}

	workDir := v.opts.WorkingDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	declared := rep.NewPath
	if !filepath.IsAbs(declared) {
		declared = filepath.Join(workDir, declared)
	}
	declared = filepath.Clean(declared)
	allowedDir := filepath.Clean(reg.Dir)

	if declared != allowedDir {
		return nil, &ModuleError{Path: rep.OldPath, Version: rep.OldVersion,
			Err: fmt.Errorf("%w: replace target %s does not match registered dir %s",
				ErrLocalNotRegistered, declared, allowedDir)}
	}
	info, err := os.Stat(allowedDir)
	if err != nil || !info.IsDir() {
		return nil, &ModuleError{Path: rep.OldPath, Version: rep.OldVersion,
			Err: fmt.Errorf("%w: local replacement dir %s is not a directory",
				ErrInvalidSource, allowedDir)}
	}
	if err := checkNoSymlinkEscape(allowedDir); err != nil {
		return nil, &ModuleError{Path: rep.OldPath, Version: rep.OldVersion, Err: err}
	}

	// The digest uses the original path@version as the zip-style prefix, so
	// local content is bound to the module the build actually imports.
	digest, err := HashDir(allowedDir, rep.OldPath, rep.OldVersion)
	if err != nil {
		return nil, &ModuleError{Path: rep.OldPath, Version: rep.OldVersion,
			Err: fmt.Errorf("hashing local replacement: %w", err)}
	}

	vm.Local = true
	vm.EffectivePath = rep.OldPath
	vm.EffectiveVersion = rep.OldVersion
	vm.Digest = digest
	vm.Source = Source{
		URL:      "file://" + filepath.ToSlash(allowedDir),
		Protocol: "file",
		LocalDir: allowedDir,
	}
	return vm, nil
}

// checkNoSymlinkEscape rejects symlinks inside the replacement directory that
// point outside it, preventing a local path from pulling in unregistered code.
func checkNoSymlinkEscape(dir string) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		target = filepath.Clean(target)
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: symlink %s escapes local replacement root", ErrInvalidSource, p)
		}
		return nil
	})
}
