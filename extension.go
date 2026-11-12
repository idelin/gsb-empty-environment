package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Role is the database role requesting an extension operation.
//
// Superuser and IsSuperuser are aliases. Either one being true grants
// superuser privileges to the request. Keeping both names makes the type easy
// to construct from SQL catalog data and from simple policy adapters.
type Role struct {
	Name        string
	Superuser   bool
	IsSuperuser bool
}

// IsSuperuserRole reports whether the role has superuser privileges.
func (r Role) IsSuperuserRole() bool {
	return r.Superuser || r.IsSuperuser
}

// Extension describes the security-relevant fields of a PostgreSQL extension
// control file.
//
// For a control file which represents a superuser-only extension, set either
// Superuser or SuperuserOnly. Both are accepted so metadata can be mapped from
// different catalog shapes without losing the security decision.
type Extension struct {
	Name string

	// Trusted is the control file's trusted flag. Only trusted extensions may
	// be installed by a non-superuser.
	Trusted bool

	// IsTrusted is an alternate spelling for Trusted.
	IsTrusted bool

	// Superuser is the control file's superuser flag.
	Superuser bool

	// SuperuserOnly is an alternate spelling for Superuser.
	SuperuserOnly bool

	// SuperuserRequired is another alternate spelling for Superuser.
	SuperuserRequired bool

	// RequiresSuperuser is another alternate spelling for Superuser.
	RequiresSuperuser bool

	// Requires is the direct dependency list from the control file.
	Requires []string

	// ModulePathname is the control file's module_pathname, if present. It is
	// subject to the same allowlist as LOAD and C-function libraries.
	ModulePathname string
}

// ControlFile, ControlMetadata, and ExtensionMetadata are aliases for
// Extension so callers can name the input after their source format.
type ControlFile = Extension
type ControlMetadata = Extension
type ExtensionMetadata = Extension
type Control = Extension

// InstalledExtensions maps an extension name to its installed metadata. The
// presence of a key means the extension is installed. The metadata is required
// to verify the trusted flag of dependencies.
type InstalledExtensions = map[string]Extension

// ExtensionCatalog is a read-only source of installed extension metadata.
type ExtensionCatalog interface {
	LookupExtension(name string) (Extension, bool)
}

// EnumerableExtensionCatalog is an ExtensionCatalog which can also enumerate
// all installed extensions. The admission helpers use this form when it is
// supplied directly as a catalog argument.
type EnumerableExtensionCatalog interface {
	ExtensionCatalog
	ExtensionMap() map[string]Extension
}

// Catalog is an alias for ExtensionCatalog.
type Catalog = ExtensionCatalog

// InstalledCatalog adapts a map to ExtensionCatalog.
type InstalledCatalog map[string]Extension

// LookupExtension implements ExtensionCatalog.
func (c InstalledCatalog) LookupExtension(name string) (Extension, bool) {
	ext, ok := c[name]
	return ext, ok
}

// ExtensionMap implements EnumerableExtensionCatalog.
func (c InstalledCatalog) ExtensionMap() map[string]Extension {
	return map[string]Extension(c)
}

var (
	// ErrInvalidExtension indicates that no unambiguous extension metadata was
	// supplied to an admission helper.
	ErrInvalidExtension = errors.New("invalid extension metadata")

	// ErrExtensionNotTrusted indicates that a non-superuser attempted to
	// install an extension whose trusted flag is false.
	ErrExtensionNotTrusted = errors.New("extension is not trusted")

	// ErrSuperuserRequired indicates that the requested extension is
	// superuser-only.
	ErrSuperuserRequired = errors.New("extension requires superuser")

	// ErrDependencyNotInstalled indicates that a required extension is absent
	// from the installed catalog.
	ErrDependencyNotInstalled = errors.New("required extension is not installed")

	// ErrDependencyNotTrusted indicates that an installed dependency is not
	// trusted.
	ErrDependencyNotTrusted = errors.New("required extension is not trusted")

	// ErrDependencyRequiresSuperuser indicates that an installed dependency is
	// itself superuser-only.
	ErrDependencyRequiresSuperuser = errors.New("required extension requires superuser")

	// ErrInvalidDependencyName indicates an empty or malformed dependency entry.
	ErrInvalidDependencyName = errors.New("invalid extension dependency name")
)

// Aliases for callers which use shorter error names.
var (
	ErrNotTrusted          = ErrExtensionNotTrusted
	ErrUntrustedExtension  = ErrExtensionNotTrusted
	ErrSuperuserOnly       = ErrSuperuserRequired
	ErrMissingDependency   = ErrDependencyNotInstalled
	ErrDependencyUntrusted = ErrDependencyNotTrusted
)

// EvaluateInstall applies the extension installation policy.
//
// A superuser may install any extension, subject only to the shared-library
// path allowlist. A non-superuser may install an extension only when:
//   - the extension is trusted;
//   - the extension is not superuser-only; and
//   - every extension in the transitive requires closure is installed, trusted,
//     and not superuser-only.
//
// Cycles in an installed dependency graph are handled without recursion loops.
func EvaluateInstall(role any, ext Extension, installed ...any) error {
	normalizedRole := normalizeRole(role)
	catalog := mergeInstalled(installed...)

	if err := ValidateExtension(ext); err != nil {
		return err
	}

	if normalizedRole.IsSuperuserRole() {
		return nil
	}

	if extSuperuserOnly(ext) {
		return fmt.Errorf("%w: %s", ErrSuperuserRequired, displayName(ext))
	}
	if !extTrusted(ext) {
		return fmt.Errorf("%w: %s", ErrExtensionNotTrusted, displayName(ext))
	}

	if err := checkDependencyClosure(ext.Requires, catalog); err != nil {
		return fmt.Errorf("extension %q: %w", displayName(ext), err)
	}
	return nil
}

// ValidateExtension validates metadata which is independent of the requesting
// role: the extension name and module_pathname.
func ValidateExtension(ext Extension) error {
	if ext.Name != "" && hasControlCharacter(ext.Name) {
		return fmt.Errorf("%w: extension name %q contains a control character", ErrInvalidExtension, ext.Name)
	}
	if ext.ModulePathname != "" {
		if err := ValidateLibraryPath(ext.ModulePathname); err != nil {
			return fmt.Errorf("%w: module_pathname for extension %q: %v", ErrInvalidLibraryPath, displayName(ext), err)
		}
	}
	return nil
}

// ValidateControlFileMetadata is an alias for ValidateExtension.
func ValidateControlFileMetadata(ext Extension) error {
	return ValidateExtension(ext)
}

// CheckExtensionInstall is the main admission entry point. It is
// order-tolerant: it accepts the role, Extension metadata, and installed
// catalog in any order.
//
// role may be a Role, *Role, bool (true means superuser), or a role name
// string. The string "superuser" is recognized as superuser; all other strings
// are treated as non-superuser names.
//
// installed may be omitted, or may contain any combination of
// map[string]Extension, InstalledCatalog, EnumerableExtensionCatalog,
// []Extension, map[string]bool, or []string. In a bool map, true means
// installed and trusted. In a []string, names are treated as installed and
// trusted. Prefer passing full Extension metadata whenever possible.
func CheckExtensionInstall(args ...any) error {
	return CheckInstall(args...)
}

// CheckExtension is an order-tolerant alias for CheckInstall.
func CheckExtension(args ...any) error {
	return CheckInstall(args...)
}

// AdmitExtensionInstall is an alias for CheckExtensionInstall.
func AdmitExtensionInstall(args ...any) error {
	return CheckInstall(args...)
}

// ValidateExtensionInstall is an alias for CheckExtensionInstall.
func ValidateExtensionInstall(args ...any) error {
	return CheckInstall(args...)
}

// ValidateInstall is an order-tolerant alias for CheckInstall.
func ValidateInstall(args ...any) error {
	return CheckInstall(args...)
}

// AuthorizeInstall is an order-tolerant alias for CheckInstall.
func AuthorizeInstall(args ...any) error {
	return CheckInstall(args...)
}

// AuthorizeExtension is an order-tolerant alias for CheckInstall.
func AuthorizeExtension(args ...any) error {
	return CheckInstall(args...)
}

// CheckCreateExtension is an order-tolerant alias for CheckInstall.
func CheckCreateExtension(args ...any) error {
	return CheckInstall(args...)
}

// AuthorizeCreateExtension is an order-tolerant alias for CheckInstall.
func AuthorizeCreateExtension(args ...any) error {
	return CheckInstall(args...)
}

// AdmitCreateExtension is an order-tolerant alias for CheckInstall.
func AdmitCreateExtension(args ...any) error {
	return CheckInstall(args...)
}

// AuthorizeExtensionInstall is an alias for CheckExtensionInstall.
func AuthorizeExtensionInstall(args ...any) error {
	return CheckInstall(args...)
}

// CheckInstall is an order-tolerant alias. It finds the Extension and role by
// type, so both CheckInstall(role, ext, installed) and
// CheckInstall(ext, role, installed) are accepted.
func CheckInstall(args ...any) error {
	ext, role, installed, err := splitInstallArgs(args)
	if err != nil {
		return err
	}
	return EvaluateInstall(role, ext, installed)
}

// AdmitInstall is an order-tolerant alias for CheckInstall.
func AdmitInstall(args ...any) error {
	return CheckInstall(args...)
}

// AdmitExtension is an order-tolerant alias for CheckInstall.
func AdmitExtension(args ...any) error {
	return CheckInstall(args...)
}

// CanInstall reports whether the admission policy accepts the supplied request.
func CanInstall(args ...any) bool {
	return CheckInstall(args...) == nil
}

// IsExtensionInstallAllowed reports whether the admission policy accepts the
// supplied request.
func IsExtensionInstallAllowed(args ...any) bool {
	return CanInstall(args...)
}

// IsInstallAllowed reports whether the admission policy accepts the supplied
// request.
func IsInstallAllowed(args ...any) bool {
	return CanInstall(args...)
}

// CanCreateExtension reports whether the admission policy accepts the supplied
// request.
func CanCreateExtension(args ...any) bool {
	return CanInstall(args...)
}

// Admitter binds a role and an installed-extension catalog.
type Admitter struct {
	Role      Role
	Installed InstalledExtensions
}

// Policy is an alias for Admitter.
type Policy = Admitter

// NewAdmitter creates an admission policy for a role and installed catalog.
func NewAdmitter(role any, installed ...any) *Admitter {
	return &Admitter{
		Role:      normalizeRole(role),
		Installed: mergeInstalled(installed...),
	}
}

// NewPolicy creates an admission policy for a role and installed catalog.
func NewPolicy(role any, installed ...any) *Policy {
	return NewAdmitter(role, installed...)
}

// CheckInstall validates an extension installation request.
func (a *Admitter) CheckInstall(ext Extension) error {
	if a == nil {
		return fmt.Errorf("%w: nil admitter", ErrInvalidExtension)
	}
	return EvaluateInstall(a.Role, ext, a.Installed)
}

// EvaluateInstall validates an extension installation request.
func (a *Admitter) EvaluateInstall(ext Extension) error {
	return a.CheckInstall(ext)
}

// AdmitInstall is an alias for Admitter.CheckInstall.
func (a *Admitter) AdmitInstall(ext Extension) error {
	return a.CheckInstall(ext)
}

// ValidateInstall is an alias for Admitter.CheckInstall.
func (a *Admitter) ValidateInstall(ext Extension) error {
	return a.CheckInstall(ext)
}

// CheckExtension is an alias for Admitter.CheckInstall.
func (a *Admitter) CheckExtension(ext Extension) error {
	return a.CheckInstall(ext)
}

// Authorize is an alias for Admitter.CheckInstall.
func (a *Admitter) Authorize(ext Extension) error {
	return a.CheckInstall(ext)
}

// CheckLoad validates a LOAD library reference.
func (a *Admitter) CheckLoad(library string) error {
	return ValidateLoadLibrary(library)
}

// CheckCFunction validates a C-function library reference.
func (a *Admitter) CheckCFunction(library string) error {
	return ValidateCFunctionLibrary(library)
}

func checkDependencyClosure(requires []string, installed InstalledExtensions) error {
	visited := make(map[string]bool, len(requires))

	var walk func([]string) error
	walk = func(deps []string) error {
		for _, rawName := range deps {
			name := strings.TrimSpace(rawName)
			if name == "" || hasControlCharacter(name) {
				return fmt.Errorf("%w: %q", ErrInvalidDependencyName, rawName)
			}

			dep, ok := lookupInstalled(installed, name)
			if !ok {
				return fmt.Errorf("%w: %q", ErrDependencyNotInstalled, name)
			}
			if extSuperuserOnly(dep) {
				return fmt.Errorf("%w: %q", ErrDependencyRequiresSuperuser, name)
			}
			if !extTrusted(dep) {
				return fmt.Errorf("%w: %q", ErrDependencyNotTrusted, name)
			}

			if visited[name] {
				continue
			}
			visited[name] = true
			if err := walk(dep.Requires); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(requires)
}

func lookupInstalled(installed InstalledExtensions, name string) (Extension, bool) {
	if installed == nil {
		return Extension{}, false
	}
	ext, ok := installed[name]
	return ext, ok
}

func extSuperuserOnly(ext Extension) bool {
	return ext.Superuser || ext.SuperuserOnly || ext.SuperuserRequired || ext.RequiresSuperuser
}

func extTrusted(ext Extension) bool {
	return ext.Trusted || ext.IsTrusted
}

func displayName(ext Extension) string {
	if ext.Name == "" {
		return "unnamed extension"
	}
	return fmt.Sprintf("%q", ext.Name)
}

func hasControlCharacter(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func normalizeRole(value any) Role {
	switch role := value.(type) {
	case nil:
		return Role{}
	case Role:
		return role
	case *Role:
		if role == nil {
			return Role{}
		}
		return *role
	case bool:
		return Role{Superuser: role}
	case string:
		return Role{
			Name:      role,
			Superuser: strings.EqualFold(role, "superuser"),
		}
	default:
		// An unknown role representation is treated as an ordinary,
		// non-superuser role. This is the safe failure mode.
		return Role{}
	}
}

func mergeInstalled(values ...any) InstalledExtensions {
	merged := make(InstalledExtensions)
	add := func(name string, ext Extension) {
		if name == "" {
			name = ext.Name
		}
		if name == "" {
			return
		}
		if _, exists := merged[name]; !exists {
			merged[name] = ext
		}
	}

	for _, value := range values {
		switch catalog := value.(type) {
		case nil:
		case map[string]Extension:
			for name, ext := range catalog {
				add(name, ext)
			}
		case InstalledCatalog:
			for name, ext := range catalog {
				add(name, ext)
			}
		case map[string]*Extension:
			for name, ext := range catalog {
				if ext != nil {
					add(name, *ext)
				}
			}
		case map[string]map[string]string:
			for name, raw := range catalog {
				ext, err := ControlFileFromMap(raw)
				if err == nil {
					add(name, ext)
				}
			}
		case map[string]RawControlFile:
			for name, raw := range catalog {
				ext, err := ControlFileFromMap(raw)
				if err == nil {
					add(name, ext)
				}
			}
		case map[string]bool:
			for name, installedAndTrusted := range catalog {
				if installedAndTrusted {
					add(name, Extension{Name: name, Trusted: true})
				}
			}
		case []Extension:
			for _, ext := range catalog {
				add(ext.Name, ext)
			}
		case []*Extension:
			for _, ext := range catalog {
				if ext != nil {
					add(ext.Name, *ext)
				}
			}
		case []string:
			for _, name := range catalog {
				add(name, Extension{Name: name, Trusted: true})
			}
		case EnumerableExtensionCatalog:
			for name, ext := range catalog.ExtensionMap() {
				add(name, ext)
			}
		}
	}
	return merged
}

func splitInstallArgs(args []any) (Extension, Role, InstalledExtensions, error) {
	var (
		ext       *Extension
		role      Role
		roleSet   bool
		installed []any
	)

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			installed = append(installed, value)
		case Extension:
			extensionValue := value
			ext = &extensionValue
		case *Extension:
			if value != nil {
				ext = value
			}
		case Role:
			role = value
			roleSet = true
		case *Role:
			if value != nil {
				role = *value
				roleSet = true
			}
		case bool:
			role = Role{Superuser: value}
			roleSet = true
		case string:
			role = normalizeRole(value)
			roleSet = true
		case map[string]string:
			parsed, err := ControlFileFromMap(value)
			if err != nil {
				return Extension{}, Role{}, nil, err
			}
			ext = &parsed
		case RawControlFile:
			parsed, err := ControlFileFromMap(value)
			if err != nil {
				return Extension{}, Role{}, nil, err
			}
			ext = &parsed
		default:
			installed = append(installed, value)
		}
	}

	if ext == nil {
		return Extension{}, Role{}, nil, fmt.Errorf("%w: missing Extension argument", ErrInvalidExtension)
	}
	if !roleSet {
		role = Role{}
	}
	return *ext, role, mergeInstalled(installed...), nil
}

// ExtensionMap lets custom catalogs expose their contents to the admission
// helpers without reflection.
func ExtensionMap(catalog ExtensionCatalog) map[string]Extension {
	if catalog == nil {
		return nil
	}
	if direct, ok := catalog.(interface{ ExtensionMap() map[string]Extension }); ok {
		return direct.ExtensionMap()
	}
	return nil
}
