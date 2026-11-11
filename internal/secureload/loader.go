package secureload

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrDigestMismatch is returned when the streamed content of an opened object
// does not match the digest pinned in the manifest. It is never wrapped
// around a callback invocation: a mismatching object is closed and discarded.
var ErrDigestMismatch = errors.New("secureload: digest mismatch")

// LoadCallback receives an authenticated module. The *os.File is the exact
// object whose streamed content was verified; it is positioned at offset zero
// and remains open for the duration of the call. The loader closes it after
// the callback returns, so implementations must not retain it. A returned
// error aborts the whole load.
type LoadCallback func(module string, spec ModuleSpec, file *os.File) error

// PostOpenHook, when set, runs after the object has been opened but before any
// of its bytes are verified. It exists as a test seam for reproducing
// post-open races (rename-and-swap, in-place mutation). Production code
// leaves it nil.
type PostOpenHook func(module string, spec ModuleSpec, file *os.File) error

// Loader resolves signed module layers from a [Manifest].
type Loader struct {
	manifest *Manifest
	opener   Opener

	// PostOpen is an optional test seam; nil in production.
	PostOpen PostOpenHook
}

// NewLoader builds a Loader with the default [SecureOpener].
func NewLoader(m *Manifest) (*Loader, error) {
	opener, err := NewSecureOpener(m)
	if err != nil {
		return nil, err
	}
	return &Loader{manifest: m, opener: opener}, nil
}

// NewLoaderWithOpener builds a Loader with a substituted [Opener]. It is
// primarily intended for adversarial tests; production callers use
// [NewLoader].
func NewLoaderWithOpener(m *Manifest, opener Opener) (*Loader, error) {
	if opener == nil {
		return nil, errors.New("secureload: loader requires an opener")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &Loader{manifest: m, opener: opener}, nil
}

// Close releases resources held by the default opener. Loaders built with
// [NewLoaderWithOpener] do not own their opener and return nil.
func (l *Loader) Close() error {
	if secure, ok := l.opener.(*SecureOpener); ok {
		return secure.Close()
	}
	return nil
}

// Load authenticates entry and every transitive dependency layer, invoking
// callback once per module in dependency order (dependencies first). On
// failure it returns the first error and guarantees that no unverified object
// has reached the callback.
func (l *Loader) Load(entry string, callback LoadCallback) error {
	if callback == nil {
		return errors.New("secureload: load requires a callback")
	}
	if _, ok := l.manifest.Modules[entry]; !ok {
		return fmt.Errorf("secureload: unknown entry module %q", entry)
	}

	state := make(map[string]int)
	const (
		stateVisiting = 1
		stateDone     = 2
	)

	var resolve func(name string) error
	resolve = func(name string) error {
		switch state[name] {
		case stateDone:
			return nil
		case stateVisiting:
			return fmt.Errorf("secureload: dependency cycle detected at module %q", name)
		}
		state[name] = stateVisiting

		spec := l.manifest.Modules[name]
		// Authenticate every lower layer first; the callback for this module
		// only runs once its whole subtree has been verified.
		for _, dep := range spec.Deps {
			if err := resolve(dep); err != nil {
				return err
			}
		}
		if err := l.verifyAndHandOff(name, spec, callback); err != nil {
			return err
		}
		state[name] = stateDone
		return nil
	}

	return resolve(entry)
}

// verifyAndHandOff is the security-critical path. There is exactly one open
// operation per module: the returned object is hashed in place and, only if
// the digest matches, handed to the callback. Nothing else ever reaches it.
func (l *Loader) verifyAndHandOff(module string, spec ModuleSpec, callback LoadCallback) (err error) {
	file, err := l.opener.OpenAuthenticated(spec.RootName, spec.RelPath)
	if err != nil {
		return err
	}
	defer func() {
		// The object is always closed by the loader, including on every
		// failure path. A failed verification must not leak the fd.
		_ = file.Close()
	}()

	if l.PostOpen != nil {
		if err := l.PostOpen(module, spec, file); err != nil {
			return err
		}
	}

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("secureload: module %q: cannot stat opened object: %w", module, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secureload: module %q: opened object is not a regular file (mode %s)", module, info.Mode().Type())
	}

	if err := verifyDigest(file, spec.Digest); err != nil {
		return fmt.Errorf("secureload: module %q: %w", module, err)
	}

	// Same object, rewound for the consumer: no re-open, no re-resolution.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("secureload: module %q: cannot rewind verified object: %w", module, err)
	}
	return callback(module, spec, file)
}

// verifyDigest streams the opened object and compares its SHA-256 digest with
// the pinned value. The comparison runs in constant time and never touches a
// path on disk.
func verifyDigest(file *os.File, want Digest) error {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("cannot hash opened object: %w", err)
	}
	if !want.matches(hasher.Sum(nil)) {
		return ErrDigestMismatch
	}
	return nil
}
