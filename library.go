package netsec

import (
	"errors"
	"fmt"
	"strings"
)

const libdirPrefix = "$libdir/"

// ErrInvalidLibraryPath indicates a shared-library reference which is not
// exactly "$libdir/<basename>".
var ErrInvalidLibraryPath = errors.New("invalid library path")

// ErrLibraryTraversal indicates a library path containing "..".
var ErrLibraryTraversal = fmt.Errorf("%w: path traversal contains %q", ErrInvalidLibraryPath, "..")

// ErrAbsoluteLibraryPath indicates a library path which is absolute or
// otherwise tries to escape the $libdir allowlist.
var ErrAbsoluteLibraryPath = fmt.Errorf("%w: absolute path", ErrInvalidLibraryPath)

// Aliases for callers which use shorter path-error names.
var (
	ErrPathTraversal  = ErrLibraryTraversal
	ErrAbsolutePath   = ErrAbsoluteLibraryPath
	ErrInvalidLibrary = ErrInvalidLibraryPath
	ErrLibraryPath    = ErrInvalidLibraryPath
	ErrInvalidPath    = ErrInvalidLibraryPath
)

// ValidateLibraryPath validates the library-name syntax used by LOAD and by C
// functions.
//
// The only accepted form is:
//
//	$libdir/<basename>
//
// where <basename> is non-empty, contains no forward or backward slash, is not
// an absolute path, and does not contain the substring "..". This rejects
// absolute paths, nested paths, and path traversal such as
// "$libdir/../../tmp/evil".
func ValidateLibraryPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: library name is empty", ErrInvalidLibraryPath)
	}
	if hasControlCharacter(path) {
		return fmt.Errorf("%w: library name contains a control character", ErrInvalidLibraryPath)
	}

	if !strings.HasPrefix(path, libdirPrefix) {
		if strings.Contains(path, "..") {
			return fmt.Errorf("%w: %q", ErrLibraryTraversal, path)
		}
		if isAbsoluteish(path) {
			return fmt.Errorf("%w: %q", ErrAbsoluteLibraryPath, path)
		}
		return fmt.Errorf("%w: %q must start with %q", ErrInvalidLibraryPath, path, libdirPrefix)
	}

	base := path[len(libdirPrefix):]
	if base == "" {
		return fmt.Errorf("%w: %q has an empty basename", ErrInvalidLibraryPath, path)
	}
	if strings.Contains(base, "..") {
		return fmt.Errorf("%w: %q", ErrLibraryTraversal, path)
	}
	if strings.ContainsAny(base, `/\`) {
		if strings.HasPrefix(base, "/") || strings.HasPrefix(base, `\`) {
			return fmt.Errorf("%w: %q", ErrAbsoluteLibraryPath, path)
		}
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidLibraryPath, path)
	}
	if base == "." {
		return fmt.Errorf("%w: %q has an invalid basename", ErrInvalidLibraryPath, path)
	}
	if strings.ContainsRune(base, ' ') {
		return fmt.Errorf("%w: %q contains whitespace", ErrInvalidLibraryPath, path)
	}
	// A Windows drive prefix is absolute even without a slash after it.
	if len(base) >= 2 && base[1] == ':' && isASCIILetter(base[0]) {
		return fmt.Errorf("%w: %q", ErrAbsoluteLibraryPath, path)
	}
	if strings.ContainsRune(base, ':') {
		return fmt.Errorf("%w: %q contains a drive or stream separator", ErrInvalidLibraryPath, path)
	}
	return nil
}

// ValidateLoadLibrary validates the library name in a LOAD statement.
func ValidateLoadLibrary(path string) error {
	return ValidateLibraryPath(path)
}

// ValidateCFunctionLibrary validates the library name attached to a C function.
func ValidateCFunctionLibrary(path string) error {
	return ValidateLibraryPath(path)
}

// ValidateModulePathname validates a control file module_pathname. An empty
// module_pathname is allowed because the field is optional; any non-empty
// value must satisfy the shared-library allowlist.
func ValidateModulePathname(path string) error {
	if path == "" {
		return nil
	}
	return ValidateLibraryPath(path)
}

// CheckLibraryPath is an alias for ValidateLibraryPath.
func CheckLibraryPath(path string) error {
	return ValidateLibraryPath(path)
}

// CheckLoad is an alias for ValidateLoadLibrary.
func CheckLoad(path string) error {
	return ValidateLoadLibrary(path)
}

// ValidateLoad is an alias for ValidateLoadLibrary.
func ValidateLoad(path string) error {
	return ValidateLoadLibrary(path)
}

// ValidateLoadPath is an alias for ValidateLoadLibrary.
func ValidateLoadPath(path string) error {
	return ValidateLoadLibrary(path)
}

// CheckCFunction is an alias for ValidateCFunctionLibrary.
func CheckCFunction(path string) error {
	return ValidateCFunctionLibrary(path)
}

// ValidateCFunction is an alias for ValidateCFunctionLibrary.
func ValidateCFunction(path string) error {
	return ValidateCFunctionLibrary(path)
}

// ValidateCFunctionPath is an alias for ValidateCFunctionLibrary.
func ValidateCFunctionPath(path string) error {
	return ValidateCFunctionLibrary(path)
}

// ValidateLibrary is an alias for ValidateLibraryPath.
func ValidateLibrary(path string) error {
	return ValidateLibraryPath(path)
}

// ValidateLibraryName is an alias for ValidateLibraryPath.
func ValidateLibraryName(path string) error {
	return ValidateLibraryPath(path)
}

// CheckLibraryName is an alias for ValidateLibraryPath.
func CheckLibraryName(path string) error {
	return ValidateLibraryPath(path)
}

// IsLibraryPathAllowed reports whether a LOAD or C-function library reference
// satisfies the allowlist.
func IsLibraryPathAllowed(path string) bool {
	return ValidateLibraryPath(path) == nil
}

// IsLoadAllowed reports whether a LOAD library reference is allowed.
func IsLoadAllowed(path string) bool {
	return ValidateLoadLibrary(path) == nil
}

// IsCFunctionAllowed reports whether a C-function library reference is allowed.
func IsCFunctionAllowed(path string) bool {
	return ValidateCFunctionLibrary(path) == nil
}

// IsSafeLibraryPath reports whether a library path satisfies the allowlist.
func IsSafeLibraryPath(path string) bool {
	return ValidateLibraryPath(path) == nil
}

func isAbsoluteish(path string) bool {
	if strings.HasPrefix(path, `/`) || strings.HasPrefix(path, `\`) {
		return true
	}
	if len(path) >= 2 && path[1] == ':' && isASCIILetter(path[0]) {
		return true
	}
	// file:// and similar URL-style references are not valid $libdir names.
	return strings.Contains(path, "://")
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
