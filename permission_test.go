package netsec

import (
	"errors"
	"testing"
)

func TestWildcardStarMatchesOnlyImmediateChildren(t *testing.T) {
	t.Parallel()

	domain := NewProtectionDomain(
		"plugin",
		NewFilePermission("/dir/*", Read),
	)
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "direct child", path: "/dir/a", want: true},
		{name: "direct child with extension", path: "/dir/file.txt", want: true},
		{name: "clean direct child", path: "/dir//a", want: true},
		{name: "directory itself", path: "/dir", want: false},
		{name: "directory with trailing slash", path: "/dir/", want: false},
		{name: "grandchild", path: "/dir/a/b", want: false},
		{name: "same-prefix sibling directory", path: "/dir2/a", want: false},
		{name: "same-prefix sibling at root", path: "/directory/a", want: false},
		{name: "same-prefix sibling with hyphen", path: "/dir-backup/a", want: false},
		{name: "escape then re-enter", path: "/dir/../dir/a", want: true},
		{name: "escape to outside", path: "/dir/../secret", want: false},
		{name: "unrelated path", path: "/other/a", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.Allows(NewFilePermission(tt.path, Read)); got != tt.want {
				t.Fatalf("Allows(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestWildcardRecursiveMatchesAllDescendants(t *testing.T) {
	t.Parallel()

	domain := NewProtectionDomain(
		"plugin",
		NewFilePermission("/dir/-", Read),
	)
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "direct child", path: "/dir/a", want: true},
		{name: "grandchild", path: "/dir/a/b", want: true},
		{name: "deep descendant", path: "/dir/a/b/c", want: true},
		{name: "directory itself", path: "/dir", want: false},
		{name: "directory with trailing slash", path: "/dir/", want: false},
		{name: "same-prefix sibling directory", path: "/dir2/a", want: false},
		{name: "same-prefix sibling at root", path: "/directory/a", want: false},
		{name: "same-prefix sibling with hyphen", path: "/dir-backup/a", want: false},
		{name: "escape to outside", path: "/dir/a/../../dir2/x", want: false},
		{name: "normalized descendant", path: "/dir/a/../b", want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.Allows(NewFilePermission(tt.path, Read)); got != tt.want {
				t.Fatalf("Allows(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExactPermissionAndRootWildcards(t *testing.T) {
	t.Parallel()

	exact := NewProtectionDomain(
		"exact",
		NewFilePermission("/dir/file", Read),
	)
	for _, path := range []string{"/dir/file", "/dir//file"} {
		if !exact.Allows(NewFilePermission(path, Read)) {
			t.Fatalf("exact grant should allow %q", path)
		}
	}
	for _, path := range []string{"/dir", "/dir/file/child", "/dir2/file", "/dir/other"} {
		if exact.Allows(NewFilePermission(path, Read)) {
			t.Fatalf("exact grant should not allow %q", path)
		}
	}

	rootStar := NewProtectionDomain("root-star", NewFilePermission("/*", Read))
	if !rootStar.Allows(NewFilePermission("/child", Read)) {
		t.Fatal("root /* should allow a direct child")
	}
	for _, path := range []string{"/", "/child/grandchild"} {
		if rootStar.Allows(NewFilePermission(path, Read)) {
			t.Fatalf("root /* should not allow %q", path)
		}
	}

	rootRecursive := NewProtectionDomain("root-recursive", NewFilePermission("/-", Read))
	for _, path := range []string{"/child", "/child/grandchild"} {
		if !rootRecursive.Allows(NewFilePermission(path, Read)) {
			t.Fatalf("root /- should allow %q", path)
		}
	}
	if rootRecursive.Allows(NewFilePermission("/", Read)) {
		t.Fatal("root /- should not allow the root itself")
	}
}

func TestDomainGrantActionsAreAdditive(t *testing.T) {
	t.Parallel()

	domain := NewProtectionDomain(
		"additive",
		NewFilePermission("/dir/file", Read),
		NewFilePermission("/dir/file", Write),
	)
	if !domain.Allows(NewFilePermission("/dir/file", Read, Write)) {
		t.Fatal("separate read and write grants should combine within one domain")
	}

	if !NewProtectionDomain("comma", NewFilePermission("/dir/file", "read,write")).
		Allows(NewFilePermission("/dir/file", Read, Write)) {
		t.Fatal("comma-separated grant actions should be accepted")
	}
}

func TestInvalidPermissionsFailClosed(t *testing.T) {
	t.Parallel()

	domain := NewProtectionDomain("valid", NewFilePermission("/dir/-", Read))
	requests := []FilePermission{
		{Path: "/dir/file"},
		{Path: "", Actions: []Action{Read}},
		{Path: "/dir/file", Actions: []Action{"execute"}},
		{Path: "/dir/\x00file", Actions: []Action{Read}},
	}
	for _, request := range requests {
		if domain.Allows(request) {
			t.Fatalf("invalid request %#v should be denied", request)
		}
	}

	if err := (FilePermission{Path: "/dir/file"}).Validate(); !errors.Is(err, ErrNoActions) {
		t.Fatalf("empty actions error = %v, want ErrNoActions", err)
	}
	if err := (FilePermission{Path: "/dir/file", Actions: []Action{"execute"}}).Validate(); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid action error = %v, want ErrInvalidAction", err)
	}
	if err := (FilePermission{Actions: []Action{Read}}).Validate(); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("empty path error = %v, want ErrEmptyPath", err)
	}

	malformedDomain := NewProtectionDomain("malformed", NewFilePermission("", Read))
	if malformedDomain.Allows(NewFilePermission("/dir/file", Read)) {
		t.Fatal("malformed grants must not match anything")
	}

	malformedDomain = NewProtectionDomain(
		"malformed-action",
		NewFilePermission("/dir/file", Read, "execute"),
	)
	if malformedDomain.Allows(NewFilePermission("/dir/file", Read)) {
		t.Fatal("a grant with an unknown action must be ignored as a whole")
	}
}
