package netsec

import (
	"fmt"
	"strings"
	"sync"
)

// Built-in ecosystem type aliases.  These are names that appear in SBOMs and
// vulnerability feeds but are not the canonical purl type spelling.
var builtinTypeAliases = map[string]string{
	"crates":     "cargo",
	"crates.io":  "cargo",
	"cratesio":   "cargo",
	"dotnet":     "nuget",
	"dart":       "pub",
	"debian":     "deb",
	"elixir":     "hex",
	"gh":         "github",
	"gems":       "gem",
	"go":         "golang",
	"gradle":     "maven",
	"haskell":    "hackage",
	"homebrew":   "brew",
	"java":       "maven",
	"javascript": "npm",
	"mvn":        "maven",
	"node":       "npm",
	"nodejs":     "npm",
	"packagist":  "composer",
	"php":        "composer",
	"pip":        "pypi",
	"pod":        "cocoapods",
	"python":     "pypi",
	"r":          "cran",
	"redhat":     "rpm",
	"ruby":       "gem",
	"rubygems":   "gem",
	"rust":       "cargo",
	"swiftpm":    "swift",
	"ubuntu":     "deb",
	"vsix":       "vscode-extension",
	"wheel":      "pypi",
}

type aliasRegistry struct {
	mu       sync.RWMutex
	types    map[string]string
	packages map[string]packageAlias
}

type packageAlias struct {
	target *PackageURL
}

var aliases = aliasRegistry{
	types:    make(map[string]string),
	packages: make(map[string]packageAlias),
}

func resolveTypeAlias(typ string) (string, bool) {
	current := typ
	resolved := false
	for i := 0; i < 8; i++ {
		aliases.mu.RLock()
		custom, customOK := aliases.types[current]
		aliases.mu.RUnlock()
		if customOK {
			resolved = true
			if custom == current {
				break
			}
			current = custom
			continue
		}
		target, ok := builtinTypeAliases[current]
		if !ok {
			break
		}
		resolved = true
		if target == current {
			break
		}
		current = target
	}
	return current, resolved
}

// RegisterTypeAlias registers a type spelling alias.  For example,
// RegisterTypeAlias("node", "npm") makes pkg:node/foo identify as
// pkg:npm/foo.  The alias and target are lowercased.
func RegisterTypeAlias(alias, canonical string) error {
	a := asciiLower(strings.TrimSpace(alias))
	c := asciiLower(strings.TrimSpace(canonical))
	if !validType(a) || !validType(c) {
		return fmt.Errorf("invalid type alias %q -> %q", alias, canonical)
	}
	if a == c {
		return nil
	}
	aliases.mu.Lock()
	aliases.types[a] = c
	aliases.mu.Unlock()
	return nil
}

// RegisterPackageAlias registers a package-coordinate alias.  Both arguments
// are purls.  If aliasPURL has no version the mapping is version-independent;
// otherwise it applies only to that exact coordinate.
func RegisterPackageAlias(aliasPURL, canonicalPURL string) error {
	aliasPURL = ensurePURLScheme(aliasPURL)
	canonicalPURL = ensurePURLScheme(canonicalPURL)
	from, err := parsePackageURL(aliasPURL, parseOptions{resolveAliases: false})
	if err != nil {
		return fmt.Errorf("invalid alias purl: %w", err)
	}
	to, err := parsePackageURL(canonicalPURL, parseOptions{resolveAliases: false})
	if err != nil {
		return fmt.Errorf("invalid canonical purl: %w", err)
	}
	key := packageAliasKey(from, from.Version != "")
	aliases.mu.Lock()
	aliases.packages[key] = packageAlias{
		target: to,
	}
	aliases.mu.Unlock()
	return nil
}

func ensurePURLScheme(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "pkg:") {
		return s
	}
	if strings.Contains(s, "/") {
		return "pkg:" + s
	}
	return s
}

// RegisterAlias registers either a type alias (when given bare type names) or
// a package-coordinate alias (when given purls).
func RegisterAlias(aliasPURL, canonicalPURL string) error {
	a := strings.TrimSpace(aliasPURL)
	c := strings.TrimSpace(canonicalPURL)
	if !strings.HasPrefix(strings.ToLower(a), "pkg:") && !strings.Contains(a, "/") &&
		!strings.HasPrefix(strings.ToLower(c), "pkg:") && !strings.Contains(c, "/") {
		return RegisterTypeAlias(a, c)
	}
	return RegisterPackageAlias(aliasPURL, canonicalPURL)
}

// ResetAliases removes aliases added through the registration functions.  The
// built-in type aliases remain in effect.
func ResetAliases() {
	aliases.mu.Lock()
	aliases.types = make(map[string]string)
	aliases.packages = make(map[string]packageAlias)
	aliases.mu.Unlock()
}

func packageAliasKey(p *PackageURL, includeVersion bool) string {
	var b strings.Builder
	b.WriteString(p.Type)
	b.WriteByte('/')
	b.WriteString(p.Namespace)
	b.WriteByte('/')
	b.WriteString(p.Name)
	if includeVersion {
		b.WriteByte('@')
		b.WriteString(p.Version)
	}
	return b.String()
}

func packageAliasesFor(p *PackageURL) (packageAlias, bool) {
	aliases.mu.RLock()
	defer aliases.mu.RUnlock()
	if p.Version != "" {
		if a, ok := aliases.packages[packageAliasKey(p, true)]; ok {
			return a, true
		}
	}
	a, ok := aliases.packages[packageAliasKey(p, false)]
	return a, ok
}

// ResolveAlias maps a package coordinate through the package alias registry.
// It returns a copy, leaving the input unchanged.
func ResolveAlias(p *PackageURL) *PackageURL {
	if p == nil {
		return nil
	}
	return resolvePackageAliases(p, 0)
}

func resolvePackageAliases(p *PackageURL, depth int) *PackageURL {
	if p == nil || depth > 8 {
		return p
	}
	if target, ok, err := p.npmAliasTarget(); err == nil && ok {
		target.Raw = p.Raw
		target.Aliased = true
		target.AliasOf = p.Identity()
		return resolvePackageAliases(target, depth+1)
	}
	mapping, ok := packageAliasesFor(p)
	if !ok {
		return p
	}
	next := mapping.target.Clone()
	next.Raw = p.Raw
	next.Aliased = true
	next.AliasOf = p.Identity()
	if next.Version == "" {
		next.Version = p.Version
		next.Range = p.Range
		next.IsRange = p.IsRange
	}
	if len(p.Qualifiers) > 0 {
		merged := cloneStringMap(p.Qualifiers)
		for k, v := range next.Qualifiers {
			merged[k] = v
		}
		next.Qualifiers = merged
	}
	if next.Subpath == "" {
		next.Subpath = p.Subpath
	}
	return resolvePackageAliases(&next, depth+1)
}

func (p *PackageURL) npmAliasTarget() (*PackageURL, bool, error) {
	if p == nil || p.Type != "npm" || p.Version == "" {
		return nil, false, nil
	}
	v := p.Version
	if !strings.HasPrefix(v, "npm:") {
		return nil, false, nil
	}
	target := strings.TrimPrefix(v, "npm:")
	if target == "" {
		return nil, true, fmt.Errorf("empty npm alias target")
	}
	raw := "pkg:npm/" + target
	parsed, err := parsePackageURL(raw, parseOptions{resolveAliases: false})
	if err != nil {
		return nil, true, err
	}
	parsed.Qualifiers = cloneStringMap(p.Qualifiers)
	parsed.Subpath = p.Subpath
	return parsed, true, nil
}
