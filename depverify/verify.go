package depverify

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Options configures a Verifier.
type Options struct {
	// AllowedProtocols lists URL schemes permitted for remote module
	// sources and every redirect hop. Defaults to https only.
	AllowedProtocols []string

	// LocalReplacements is the explicit allow list of filesystem
	// replacements, matched by module path and exact version.
	LocalReplacements []LocalReplacement

	// AllowWildcardReplace opts in to path-only replace directives. It
	// defaults to false; wildcard replacements are rejected.
	AllowWildcardReplace bool

	// WorkingDir resolves relative local replacement directories. Defaults
	// to the process working directory.
	WorkingDir string
}

// Report is the outcome of a verification run.
type Report struct {
	Modules map[string]*VerifiedModule
}

func newReport() *Report {
	return &Report{Modules: map[string]*VerifiedModule{}}
}

// replacementTarget returns the effective path and version after applying a
// replacement to a requirement. A path-only (wildcard) replace swaps the
// source path but keeps the version required by the build.
func replacementTarget(req Requirement, rep Replacement) (path, version string) {
	if rep.Wildcard {
		return rep.NewPath, req.Version
	}
	return rep.NewPath, rep.NewVersion
}

// Paths returns the verified module paths in deterministic order.
func (r *Report) Paths() []string {
	paths := make([]string, 0, len(r.Modules))
	for p := range r.Modules {
		paths = append(paths, p)
	}
	sortStrings(paths)
	return paths
}

// Verifier enforces that every effective module is checksum-verified and
// fetched from an allowed source before its content is used by a build.
type Verifier struct {
	opts       Options
	allowed    map[string]bool
	localAllow map[localKey]LocalReplacement
}

type localKey struct {
	path    string
	version string
}

// New constructs a Verifier with the given options.
func New(opts Options) *Verifier {
	allowed := map[string]bool{}
	for _, p := range opts.AllowedProtocols {
		allowed[strings.ToLower(p)] = true
	}
	if len(allowed) == 0 {
		allowed["https"] = true
	}
	workDir := opts.WorkingDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	local := map[localKey]LocalReplacement{}
	for _, lr := range opts.LocalReplacements {
		dir := lr.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workDir, dir)
		}
		lr.Dir = filepath.Clean(dir)
		local[localKey{path: lr.Path, version: lr.Version}] = lr
	}
	return &Verifier{opts: opts, allowed: allowed, localAllow: local}
}

// Verify parses go.mod/go.sum, resolves every require through replace and
// exclude, fetches and hashes the effective content and binds it to a source.
// It always returns a report containing the modules verified so far; a
// non-nil error is a MultiError listing every failure.
func (v *Verifier) Verify(ctx context.Context, gomod, gosum string, fetcher Fetcher) (*Report, error) {
	report := newReport()
	var failures []error
	fail := func(err error) { failures = append(failures, err) }

	mod, err := ParseGoMod(gomod)
	if err != nil {
		return report, &MultiError{Errors: []error{err}}
	}
	sum, err := ParseGoSum(gosum)
	if err != nil {
		return report, &MultiError{Errors: []error{err}}
	}
	if fetcher == nil {
		return report, &MultiError{Errors: []error{fmt.Errorf("depverify: nil Fetcher")}}
	}

	excludes := map[localKey]bool{}
	for _, ex := range mod.Excludes {
		excludes[localKey{path: ex.Path, version: ex.Version}] = true
	}

	// Wildcard replaces are rejected up front, and competing versioned
	// replaces for one module are treated as a hard failure.
	replaceByPath := map[string][]Replacement{}
	wildcardRejected := map[string]bool{}
	for _, rep := range mod.Replacements {
		if rep.Wildcard && !v.opts.AllowWildcardReplace {
			fail(&ModuleError{Path: rep.OldPath, Version: rep.OldVersion,
				Err: fmt.Errorf("%w: %s (line %d)", ErrWildcardReplace, rep.OldPath, rep.Line)})
			wildcardRejected[rep.OldPath] = true
			continue
		}
		replaceByPath[rep.OldPath] = append(replaceByPath[rep.OldPath], rep)
	}
	for path, reps := range replaceByPath {
		if len(reps) > 1 {
			versions := make([]string, 0, len(reps))
			for _, rep := range reps {
				versions = append(versions, rep.OldVersion)
			}
			fail(&ModuleError{Path: path,
				Err: fmt.Errorf("%w: %s targets versions %s", ErrCompetingReplace, path,
					strings.Join(versions, ", "))})
		}
	}

	// A require must resolve to a single path@version; duplicate requires at
	// different versions are ambiguous and fail.
	seenRequire := map[string]Requirement{}
	for _, req := range mod.Requires {
		if prev, ok := seenRequire[req.Path]; ok && prev.Version != req.Version {
			fail(&ModuleError{Path: req.Path, Version: req.Version,
				Err: fmt.Errorf("%w: %s required at %s and %s",
					ErrDuplicateRequire, req.Path, prev.Version, req.Version)})
			continue
		}
		seenRequire[req.Path] = req
	}

	for _, req := range seenRequire {
		if excludes[localKey{path: req.Path, version: req.Version}] {
			fail(&ModuleError{Path: req.Path, Version: req.Version,
				Err: fmt.Errorf("%w: %s@%s", ErrExcluded, req.Path, req.Version)})
			continue
		}
		if wildcardRejected[req.Path] {
			continue
		}
		if len(replaceByPath[req.Path]) > 1 {
			continue // competing replace already recorded as a failure
		}
		if reps := replaceByPath[req.Path]; len(reps) == 1 && reps[0].Local {
			if _, ok := v.localAllow[localKey{path: reps[0].OldPath, version: reps[0].OldVersion}]; !ok {
				fail(&ModuleError{Path: req.Path, Version: req.Version,
					Err: fmt.Errorf("%w: %s@%s -> %s is not listed in LocalReplacements",
						ErrLocalNotRegistered, reps[0].OldPath, reps[0].OldVersion, reps[0].NewPath)})
				continue
			}
		} else {
			effPath, effVersion := req.Path, req.Version
			if reps := replaceByPath[req.Path]; len(reps) == 1 {
				effPath, effVersion = replacementTarget(req, reps[0])
			}
			// Checksum presence is enforced before any network activity so a
			// missing go.sum never reaches the proxy (or an attacker).
			if _, ok := sum.Lookup(effPath, effVersion, false); !ok {
				fail(&ModuleError{Path: req.Path, Version: req.Version,
					Err: fmt.Errorf("%w: no hash for %s@%s (module)",
						ErrChecksumMissing, effPath, effVersion)})
				continue
			}
		}
		vm, err := v.verifyModule(ctx, req, replaceByPath[req.Path], excludes, sum, fetcher)
		if err != nil {
			fail(err)
			continue
		}
		report.Modules[req.Path] = vm
	}

	if len(failures) > 0 {
		return report, &MultiError{Errors: failures}
	}
	return report, nil
}

func (v *Verifier) verifyModule(
	ctx context.Context,
	req Requirement,
	reps []Replacement,
	excludes map[localKey]bool,
	sum *GoSumFile,
	fetcher Fetcher,
) (*VerifiedModule, error) {
	vm := &VerifiedModule{Path: req.Path, Version: req.Version}

	if len(reps) > 0 {
		rep := reps[0]
		vm.Replaced = true
		if rep.Local {
			return v.verifyLocal(req, rep, vm)
		}
		vm.EffectivePath, vm.EffectiveVersion = replacementTarget(req, rep)
		if excludes[localKey{path: rep.NewPath, version: vm.EffectiveVersion}] {
			return nil, &ModuleError{Path: req.Path, Version: req.Version,
				Err: fmt.Errorf("%w: replacement %s@%s", ErrExcluded, rep.NewPath, vm.EffectiveVersion)}
		}
	} else {
		vm.EffectivePath = req.Path
		vm.EffectiveVersion = req.Version
	}

	content, err := fetcher.Fetch(ctx, vm.EffectivePath, vm.EffectiveVersion)
	if err != nil {
		return nil, &ModuleError{Path: vm.EffectivePath, Version: vm.EffectiveVersion,
			Err: fmt.Errorf("fetch failed: %w", err)}
	}
	defer content.Zip.Close()

	if err := v.checkSource(content.Source, vm); err != nil {
		return nil, &ModuleError{Path: vm.EffectivePath, Version: vm.EffectiveVersion, Err: err}
	}

	ra, ok := content.Zip.(io.ReaderAt)
	if !ok {
		return nil, &ModuleError{Path: vm.EffectivePath, Version: vm.EffectiveVersion,
			Err: fmt.Errorf("%w: fetcher returned a zip that does not support random access", ErrInvalidSource)}
	}
	digest, err := HashZip(ra, content.ZipSize)
	if err != nil {
		return nil, &ModuleError{Path: vm.EffectivePath, Version: vm.EffectiveVersion,
			Err: fmt.Errorf("hashing content: %w", err)}
	}

	// The hash is always looked up under the effective (post-replace)
	// path@version: a swapped source cannot hide behind the original entry.
	if err := verifyChecksum(sum, vm.EffectivePath, vm.EffectiveVersion, digest, false); err != nil {
		return nil, &ModuleError{Path: req.Path, Version: req.Version, Err: err}
	}
	vm.Digest = digest

	if len(content.GoMod) > 0 {
		if err := verifyChecksum(sum, vm.EffectivePath, vm.EffectiveVersion,
			HashBytes(content.GoMod), true); err != nil {
			return nil, &ModuleError{Path: req.Path, Version: req.Version, Err: err}
		}
		vm.GoModDigest = HashBytes(content.GoMod)
	}

	vm.Source = content.Source
	return vm, nil
}

func verifyChecksum(sum *GoSumFile, path, version, digest string, goMod bool) error {
	recorded, ok := sum.Lookup(path, version, goMod)
	if !ok {
		kind := "module"
		if goMod {
			kind = "go.mod"
		}
		return fmt.Errorf("%w: no hash for %s@%s (%s)", ErrChecksumMissing, path, version, kind)
	}
	if recorded != digest {
		return fmt.Errorf("%w: %s@%s records %s, content hashes to %s",
			ErrChecksumMismatch, path, version, recorded, digest)
	}
	return nil
}

func (v *Verifier) checkSource(src Source, vm *VerifiedModule) error {
	if src.LocalDir != "" || src.Protocol == "file" {
		return fmt.Errorf("%w: local source returned by remote fetcher for %s@%s",
			ErrInvalidSource, vm.EffectivePath, vm.EffectiveVersion)
	}
	if !v.allowed[strings.ToLower(src.Protocol)] {
		return fmt.Errorf("%w: protocol %q for %s", ErrProtocol, src.Protocol, src.URL)
	}
	if err := checkURLScheme(src.URL, v.allowed); err != nil {
		return err
	}
	for _, hop := range src.Redirects {
		if err := checkURLScheme(hop.From, v.allowed); err != nil {
			return err
		}
		if err := checkURLScheme(hop.To, v.allowed); err != nil {
			return err
		}
	}
	return nil
}

func checkURLScheme(raw string, allowed map[string]bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("%w: malformed URL %q", ErrInvalidSource, raw)
	}
	if !allowed[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("%w: %q redirects over disallowed scheme %q", ErrProtocol, raw, u.Scheme)
	}
	return nil
}
