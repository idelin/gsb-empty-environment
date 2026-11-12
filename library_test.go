package netsec

import (
	"errors"
	"testing"
)

func TestValidateLibraryPathAllowed(t *testing.T) {
	tests := []string{
		"$libdir/foo",
		"$libdir/libfoo.so",
		"$libdir/foo.so.1",
		"$libdir/foo_bar-baz",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			if err := ValidateLibraryPath(path); err != nil {
				t.Fatalf("valid library path rejected: %v", err)
			}
		})
	}
}

func TestValidateLibraryPathRejected(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"absolute", "/usr/lib/libevil.so"},
		{"relative", "libevil.so"},
		{"nested", "$libdir/sub/libevil.so"},
		{"double slash", "$libdir//libevil.so"},
		{"traversal up", "$libdir/../../tmp/libevil.so"},
		{"traversal inside", "$libdir/foo/../libevil.so"},
		{"dot dot substring", "$libdir/foo..bar"},
		{"dot basename", "$libdir/."},
		{"empty basename", "$libdir/"},
		{"prefix only", "$libdir"},
		{"backslash traversal", `$libdir/foo\..\bar`},
		{"windows absolute", `C:\libevil.dll`},
		{"windows drive under libdir", `$libdir/C:\libevil.dll`},
		{"file url", "file:///tmp/libevil.so"},
		{"control character", "$libdir/foo\x00bar"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateLibraryPath(test.path)
			if err == nil {
				t.Fatalf("unsafe library path %q accepted", test.path)
			}
			if !errors.Is(err, ErrInvalidLibraryPath) {
				t.Fatalf("error = %v, want ErrInvalidLibraryPath", err)
			}
		})
	}
}

func TestLoadAndCFunctionUseSameAllowlist(t *testing.T) {
	if err := ValidateLoadLibrary("$libdir/libfoo.so"); err != nil {
		t.Fatalf("LOAD rejected: %v", err)
	}
	if err := ValidateCFunctionLibrary("$libdir/libfoo.so"); err != nil {
		t.Fatalf("C function rejected: %v", err)
	}
	if err := ValidateLoadLibrary("$libdir/../../tmp/evil"); err == nil {
		t.Fatal("LOAD traversal accepted")
	}
	if err := ValidateCFunctionLibrary("/tmp/evil.so"); err == nil {
		t.Fatal("C-function absolute path accepted")
	}
}

func TestTraversalErrorsAreSpecific(t *testing.T) {
	if err := ValidateLibraryPath("$libdir/../../tmp/evil"); !errors.Is(err, ErrLibraryTraversal) {
		t.Fatalf("error = %v, want ErrLibraryTraversal", err)
	}
	if err := ValidateLibraryPath("/tmp/evil.so"); !errors.Is(err, ErrAbsoluteLibraryPath) {
		t.Fatalf("error = %v, want ErrAbsoluteLibraryPath", err)
	}
}

func TestModulePathnameOptional(t *testing.T) {
	if err := ValidateModulePathname(""); err != nil {
		t.Fatalf("empty optional module_pathname rejected: %v", err)
	}
	if err := ValidateModulePathname("$libdir/foo"); err != nil {
		t.Fatalf("valid module_pathname rejected: %v", err)
	}
	if err := ValidateModulePathname("/tmp/foo"); err == nil {
		t.Fatal("absolute module_pathname accepted")
	}
}
