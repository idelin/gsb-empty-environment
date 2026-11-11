// Package secureload locates signed native modules for the extension host and
// hands them to a loader callback only after the on-disk object has been
// authenticated.
//
// The security model is:
//
//   - Every dependency layer is described by a signed manifest. A manifest
//     entry names the directory that is allowed to contain that layer; nothing
//     outside an authorized directory may ever be opened.
//   - Each module pins a digest. The digest is computed by reading the file
//     through the same open object that is later handed to the callback, so a
//     rename, symlink swap or content replacement between lookup and use cannot
//     cause a different object to be loaded.
//   - An object that fails any check produces an error and is never passed to
//     the [LoadCallback].
package secureload

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

// DigestAlgoSHA256 is the supported digest algorithm, encoded as
// "sha256:" followed by the lowercase hexadecimal digest.
const DigestAlgoSHA256 = "sha256"

// Digest is a parsed module digest of the form "algorithm:hex-encoded-value".
type Digest struct {
	hexValue string
	raw      string
}

// digestLenSHA256 is the byte length of a SHA-256 digest.
const digestLenSHA256 = 32

// ParseDigest parses a digest string such as "sha256:<64 hex digits>".
func ParseDigest(s string) (Digest, error) {
	algo, value, ok := strings.Cut(s, ":")
	if !ok || algo == "" || value == "" {
		return Digest{}, fmt.Errorf("secureload: digest %q must be algorithm:hex", s)
	}
	if algo != DigestAlgoSHA256 {
		return Digest{}, fmt.Errorf("secureload: unsupported digest algorithm %q", algo)
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return Digest{}, fmt.Errorf("secureload: digest %q has malformed hex: %w", s, err)
	}
	if len(raw) != digestLenSHA256 {
		return Digest{}, fmt.Errorf("secureload: sha256 digest %q must be 32 bytes, got %d", s, len(raw))
	}
	return Digest{hexValue: value, raw: s}, nil
}

// String returns the original digest encoding.
func (d Digest) String() string { return d.raw }

// equal reports whether two digests match in constant time. A zero Digest
// never matches.
func (d Digest) equal(other Digest) bool {
	if d.raw == "" || other.raw == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(d.raw), []byte(other.raw)) == 1
}

// matches reports whether rawBytes hash to this digest. The comparison is
// constant time and a zero digest never matches.
func (d Digest) matches(rawBytes []byte) bool {
	if d.raw == "" || len(rawBytes) != digestLenSHA256 {
		return false
	}
	want, err := hex.DecodeString(d.hexValue)
	if err != nil || subtle.ConstantTimeCompare(want, rawBytes) != 1 {
		return false
	}
	return true
}

// ModuleSpec describes one signed native module and its dependency edges.
type ModuleSpec struct {
	// RootName selects the manifest-authorized directory under which RelPath
	// is resolved.
	RootName string

	// RelPath is the module location relative to that root. It must be a
	// clean, rootless slash-separated path.
	RelPath string

	// Digest pins the file content. Only an open object whose streamed
	// content hashes to this value may reach the callback.
	Digest Digest

	// Deps lists module names that must be authenticated before this one.
	Deps []string
}

// Manifest is the signed description of a dependency set.
type Manifest struct {
	// Roots maps a logical root name to an absolute, clean, symlink-free
	// directory. Every module path is resolved inside one of these roots.
	Roots map[string]string

	// Modules maps a module name to its specification.
	Modules map[string]ModuleSpec
}

// Validate checks structural manifest invariants. It does not touch the
// filesystem; directory existence is checked when the roots are opened.
func (m *Manifest) Validate() error {
	if len(m.Roots) == 0 {
		return errors.New("secureload: manifest declares no authorized roots")
	}
	for name, dir := range m.Roots {
		if name == "" {
			return errors.New("secureload: manifest contains an empty root name")
		}
		if dir == "" {
			return fmt.Errorf("secureload: manifest root %q has an empty directory", name)
		}
		if !path.IsAbs(dir) {
			return fmt.Errorf("secureload: manifest root %q directory %q must be absolute", name, dir)
		}
		if clean := path.Clean(dir); clean != dir {
			return fmt.Errorf("secureload: manifest root %q directory %q must be clean (use %q)", name, dir, clean)
		}
	}
	if len(m.Modules) == 0 {
		return errors.New("secureload: manifest declares no modules")
	}
	for name, spec := range m.Modules {
		if name == "" {
			return errors.New("secureload: manifest contains an empty module name")
		}
		if err := validateRelPath(spec.RelPath); err != nil {
			return fmt.Errorf("secureload: module %q: %w", name, err)
		}
		if spec.Digest.raw == "" {
			return fmt.Errorf("secureload: module %q is missing a digest", name)
		}
		if spec.RootName == "" {
			return fmt.Errorf("secureload: module %q is missing an authorized root", name)
		}
		if _, ok := m.Roots[spec.RootName]; !ok {
			return fmt.Errorf("secureload: module %q references unknown root %q", name, spec.RootName)
		}
		seen := make(map[string]bool, len(spec.Deps))
		for _, dep := range spec.Deps {
			if dep == "" {
				return fmt.Errorf("secureload: module %q has an empty dependency", name)
			}
			if dep == name {
				return fmt.Errorf("secureload: module %q depends on itself", name)
			}
			if _, ok := m.Modules[dep]; !ok {
				return fmt.Errorf("secureload: module %q depends on unknown module %q", name, dep)
			}
			if seen[dep] {
				return fmt.Errorf("secureload: module %q lists dependency %q more than once", name, dep)
			}
			seen[dep] = true
		}
	}
	return nil
}

func validateRelPath(p string) error {
	if p == "" {
		return errors.New("module path is empty")
	}
	if path.IsAbs(p) {
		return fmt.Errorf("module path %q must be relative", p)
	}
	if clean := path.Clean(p); clean != p {
		return fmt.Errorf("module path %q must be clean (use %q)", p, clean)
	}
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return fmt.Errorf("module path %q must not escape its root", p)
	}
	return nil
}
