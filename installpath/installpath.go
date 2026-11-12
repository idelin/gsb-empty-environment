// Package installpath resolves Composer-style package installation paths in a
// deterministic, containment-safe way.
//
// The installer places a package under the project root using a path template
// that may contain only the {$vendor}, {$name} and {$type} placeholders. A
// package's extra.installer-name, if present, overrides {$name}. All inputs are
// validated so that a crafted package can never escape the project root, even
// through existing symbolic links, and two distinct packages can never claim
// the same target path.
package installpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Sentinel errors returned by the resolver. Errors are wrapped with context,
// so callers should use errors.Is rather than comparing directly.
var (
	// ErrTemplate indicates an empty or malformed path template.
	ErrTemplate = errors.New("installpath: invalid path template")
	// ErrPackage indicates an invalid vendor, name or type value.
	ErrPackage = errors.New("installpath: invalid package")
	// ErrInstallerName indicates an invalid extra.installer-name value.
	ErrInstallerName = errors.New("installpath: invalid installer-name")
	// ErrReservedName indicates a path component that collides with a Windows
	// reserved device name such as CON, NUL or COM1.
	ErrReservedName = errors.New("installpath: reserved name")
	// ErrEscape indicates a target path that leaves the project root, either
	// lexically or while following existing symbolic links.
	ErrEscape = errors.New("installpath: path escapes project root")
	// ErrConflict indicates that two different packages resolve to the same
	// normalized path.
	ErrConflict = errors.New("installpath: conflicting installation path")
)

const (
	openBrace  = "{"
	closeBrace = "}"
	separator  = string(filepath.Separator)
)

// Package describes the inputs that determine an installation path.
type Package struct {
	// Vendor is the vendor portion of the package name (vendor/name).
	Vendor string
	// Name is the name portion of the package name (vendor/name).
	Name string
	// Type is the Composer package type.
	Type string
	// InstallerName is the optional extra.installer-name override.
	InstallerName string
}

// Resolver renders a single path template for many packages while guaranteeing
// that every resolved path stays inside one project root and that no two
// distinct packages collide. A Resolver is safe for concurrent use.
type Resolver struct {
	root     string
	template string

	mu   sync.Mutex
	seen map[string]string
}

// NewResolver validates root and template and returns a resolver bound to them.
// root must be an existing directory. The template is a relative path whose
// only supported placeholders are {$vendor}, {$name} and {$type}.
func NewResolver(root, template string) (*Resolver, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot resolve project root %q: %v", ErrTemplate, root, err)
	}
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot stat project root %q: %v", ErrTemplate, cleanRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: project root %q is not a directory", ErrTemplate, cleanRoot)
	}

	if err := validateTemplate(template); err != nil {
		return nil, err
	}

	return &Resolver{
		root:     cleanRoot,
		template: template,
		seen:     make(map[string]string),
	}, nil
}

// Root returns the absolute, cleaned project root the resolver is bound to.
func (r *Resolver) Root() string {
	return r.root
}

// Resolve validates pkg, renders the template and returns the absolute path the
// package must be installed to. The returned path is guaranteed to remain
// inside the project root after existing symbolic links are followed, and no
// previously resolved package may occupy the same normalized path.
func (r *Resolver) Resolve(pkg Package) (string, error) {
	vendor := strings.ToLower(pkg.Vendor)
	name := strings.ToLower(pkg.Name)

	if err := validateNativeComponent("vendor", vendor); err != nil {
		return "", err
	}
	if err := validateNativeComponent("name", name); err != nil {
		return "", err
	}
	if err := validateType(pkg.Type); err != nil {
		return "", err
	}

	effectiveName := name
	if pkg.InstallerName != "" {
		if err := validateInstallerName(pkg.InstallerName); err != nil {
			return "", err
		}
		effectiveName = pkg.InstallerName
	}

	relPath, err := r.render(vendor, effectiveName, pkg.Type)
	if err != nil {
		return "", err
	}

	target := filepath.Join(r.root, relPath)
	if !withinRoot(r.root, target) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrEscape, target, r.root)
	}

	realRoot, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		return "", fmt.Errorf("%w: cannot evaluate project root: %v", ErrEscape, err)
	}
	realTarget, err := evalExistingSymlinks(r.root, target)
	if err != nil {
		return "", fmt.Errorf("%w: cannot evaluate target path: %v", ErrEscape, err)
	}
	if !insideOrEqual(realRoot, realTarget) {
		return "", fmt.Errorf("%w: %q follows a symbolic link outside %q", ErrEscape, target, r.root)
	}

	key := conflictKey(r.root, realTarget)
	identity := pkg.Vendor + "/" + pkg.Name

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.seen[key]; ok {
		// Only the exact same package identity may re-resolve to a path it
		// already owns. Two different packages that collapse onto the same
		// key (including case-only name differences) are conflicts.
		if existing != identity {
			return "", fmt.Errorf("%w: %q already claimed by %q", ErrConflict, realTarget, existing)
		}
		return target, nil
	}
	r.seen[key] = identity

	return target, nil
}

// validateTemplate accepts a relative template built from safe literal segments
// and the {$vendor}, {$name}, {$type} placeholders.
func validateTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return fmt.Errorf("%w: empty template", ErrTemplate)
	}
	if strings.ContainsRune(template, '\\') {
		return fmt.Errorf("%w: backslash is not allowed in %q", ErrTemplate, template)
	}
	if filepath.IsAbs(template) {
		return fmt.Errorf("%w: template must be relative, got %q", ErrTemplate, template)
	}

	rest := template
	for rest != "" {
		open := strings.Index(rest, openBrace)
		if open < 0 {
			if strings.Contains(rest, closeBrace) {
				return fmt.Errorf("%w: unmatched '}' in %q", ErrTemplate, template)
			}
			break
		}
		close := strings.Index(rest[open:], closeBrace)
		if close < 0 {
			return fmt.Errorf("%w: unmatched '{' in %q", ErrTemplate, template)
		}
		close += open
		token := rest[open : close+1]
		if token != "{$vendor}" && token != "{$name}" && token != "{$type}" {
			return fmt.Errorf("%w: unsupported placeholder %q (only {$vendor}, {$name}, {$type} are allowed)", ErrTemplate, token)
		}
		rest = rest[close+1:]
	}

	// Check the raw segments: filepath.Clean would otherwise erase a literal
	// traversal such as "{$name}/../x" before it could be inspected.
	for _, segment := range strings.Split(template, separator) {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%w: traversal segment %q in %q", ErrTemplate, segment, template)
		}
	}
	return nil
}

// validateNativeComponent validates the lowercased vendor/name fields. These
// originate from the package identifier and may only use URL-safe word
// characters.
func validateNativeComponent(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s must not be empty", ErrPackage, field)
	}
	if !isSafeWord(value) {
		return fmt.Errorf("%w: %s %q must contain only [a-z0-9._-] and start/end with [a-z0-9]", ErrPackage, field, value)
	}
	if hasWindowsTrailingSpaceOrDot(value) {
		return fmt.Errorf("%w: %s %q must not end with a dot or space", ErrPackage, field, value)
	}
	if isReservedComponent(value) {
		return fmt.Errorf("%w: %s %q is a Windows reserved name", ErrReservedName, field, value)
	}
	return nil
}

// validateType validates the package type, which is also used as a path
// segment. It is checked before any case normalization so it cannot smuggle a
// traversal sequence into the rendered path.
func validateType(pkgType string) error {
	if strings.TrimSpace(pkgType) == "" {
		return fmt.Errorf("%w: type must not be empty", ErrPackage)
	}
	if strings.ContainsAny(pkgType, separator+"/\\") {
		return fmt.Errorf("%w: type %q must not contain path separators", ErrPackage, pkgType)
	}
	if isReservedComponent(pkgType) {
		return fmt.Errorf("%w: type %q is a Windows reserved name", ErrReservedName, pkgType)
	}
	if !isSafeWordASCII(pkgType) {
		return fmt.Errorf("%w: type %q must contain only [a-zA-Z0-9._-] and start/end with an alphanumeric character", ErrPackage, pkgType)
	}
	if hasWindowsTrailingSpaceOrDot(pkgType) {
		return fmt.Errorf("%w: type %q must not end with a dot or space", ErrPackage, pkgType)
	}
	return nil
}

// validateInstallerName enforces the exact extra.installer-name contract:
// only [a-z0-9._-], with "." and ".." explicitly rejected.
func validateInstallerName(installerName string) error {
	if installerName == "" {
		return fmt.Errorf("%w: installer-name must not be empty", ErrInstallerName)
	}
	if installerName == "." || installerName == ".." {
		return fmt.Errorf("%w: installer-name %q is not allowed", ErrInstallerName, installerName)
	}
	if strings.ContainsAny(installerName, separator+"/\\") {
		return fmt.Errorf("%w: installer-name %q must not contain path separators", ErrInstallerName, installerName)
	}
	if isReservedComponent(installerName) {
		return fmt.Errorf("%w: installer-name %q is a Windows reserved name", ErrReservedName, installerName)
	}
	if !isSafeWord(installerName) {
		return fmt.Errorf("%w: installer-name %q must contain only [a-z0-9._-] and start/end with [a-z0-9]", ErrInstallerName, installerName)
	}
	if hasWindowsTrailingSpaceOrDot(installerName) {
		return fmt.Errorf("%w: installer-name %q must not end with a dot or space", ErrInstallerName, installerName)
	}
	return nil
}

// isSafeWord reports whether value is a non-empty word made of [a-z0-9._-]
// that starts and ends with an alphanumeric character.
func isSafeWord(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
			if index == 0 || index == len(value)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isSafeWordASCII is the uppercase-tolerant variant of isSafeWord used for
// package types. Casing differences are still detected as conflicts because
// the conflict key is lowercased.
func isSafeWordASCII(value string) bool {
	if value == "" {
		return false
	}
	// Reject a Windows drive-letter prefix (e.g. "C:") even on POSIX, where
	// the colon would otherwise be an ordinary file-name character.
	if len(value) >= 2 && value[1] == ':' &&
		((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) {
		return false
	}
	for index, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
			if index == 0 || index == len(value)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isReservedComponent reports whether a path component collides with a Windows
// reserved device name. Windows reserves the bare name case-insensitively,
// including when an extension or alternate data stream is appended
// (e.g. CON.txt or NUL:stream).
func isReservedComponent(component string) bool {
	stem := component
	if dot := strings.IndexAny(stem, ".:"); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.ToUpper(stem)

	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 {
		prefix, digit := stem[:3], stem[3]
		if (prefix == "COM" || prefix == "LPT") && digit >= '1' && digit <= '9' {
			return true
		}
	}
	return false
}

// hasWindowsTrailingSpaceOrDot reports whether component ends in a space or
// period. Windows strips both from file names, so the component would map to a
// different, possibly colliding name on that platform.
func hasWindowsTrailingSpaceOrDot(component string) bool {
	return strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ")
}

// render substitutes the validated placeholders and re-checks every rendered
// segment so no value can introduce traversal or an absolute path.
func (r *Resolver) render(vendor, name, pkgType string) (string, error) {
	rendered := strings.ReplaceAll(r.template, "{$vendor}", vendor)
	rendered = strings.ReplaceAll(rendered, "{$name}", name)
	rendered = strings.ReplaceAll(rendered, "{$type}", pkgType)

	if filepath.IsAbs(rendered) {
		return "", fmt.Errorf("%w: rendered path must be relative, got %q", ErrEscape, rendered)
	}
	cleaned := filepath.Clean(rendered)
	for _, segment := range strings.Split(cleaned, separator) {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: traversal segment %q in rendered path %q", ErrEscape, segment, rendered)
		}
	}
	return cleaned, nil
}

// withinRoot performs the lexical containment check: after cleaning, target
// must be root itself or a strict descendant of it.
func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+separator) &&
		!filepath.IsAbs(rel)
}

// evalExistingSymlinks walks target from the root component by component.
// Every component that exists on disk is resolved through symbolic links;
// non-existent components (the package is about to be installed there) keep
// their literal name under the already resolved parent. This yields the real
// path the installation would occupy without requiring any directory to be
// created up front.
func evalExistingSymlinks(root, target string) (string, error) {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}

	current := root
	for _, component := range strings.Split(relative, separator) {
		if component == "." || component == "" {
			continue
		}
		candidate := filepath.Join(current, component)
		if _, statErr := os.Lstat(candidate); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				current = candidate
				continue
			}
			return "", statErr
		}
		resolved, evalErr := filepath.EvalSymlinks(candidate)
		if evalErr != nil {
			return "", evalErr
		}
		current = resolved
	}
	return current, nil
}

// insideOrEqual reports whether target is root or located below root on a
// fully symlink-resolved, cleaned path.
func insideOrEqual(root, target string) bool {
	if target == root {
		return true
	}
	return strings.HasPrefix(target, root+separator)
}

// conflictKey collapses a real target path to lowercase so names differing
// only in case conflict even on case-insensitive file systems.
func conflictKey(root, realTarget string) string {
	rel, err := filepath.Rel(root, realTarget)
	if err != nil {
		rel = realTarget
	}
	return strings.ToLower(filepath.ToSlash(rel))
}
