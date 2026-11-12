package netsec

import (
	"strings"
	"testing"
)

func mustView(t *testing.T, directives ...Directive) *View {
	t.Helper()
	v, err := NewView(directives...)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	return v
}

func assertAccess(t *testing.T, v *View, path string, want Access) {
	t.Helper()
	if got := v.Access(path); got != want {
		t.Errorf("Access(%q) = %v, want %v", path, got, want)
	}
}

// A ReadWritePaths hole opened before a later ReadOnlyPaths on a parent
// is closed again: directives act in unit order, last match wins.
func TestHoleThenReadOnly(t *testing.T) {
	v := mustView(t,
		Directive{KindReadWritePaths, "/var/lib/app"},
		Directive{KindReadOnlyPaths, "/var/lib"},
	)
	assertAccess(t, v, "/var/lib/app", ReadOnly)
	assertAccess(t, v, "/var/lib/app/data.db", ReadOnly)
	assertAccess(t, v, "/var/lib", ReadOnly)
	assertAccess(t, v, "/var/log", ReadWrite)
}

// The reverse order keeps the hole open.
func TestReadOnlyThenHole(t *testing.T) {
	v := mustView(t,
		Directive{KindReadOnlyPaths, "/var/lib"},
		Directive{KindReadWritePaths, "/var/lib/app"},
	)
	assertAccess(t, v, "/var/lib/app", ReadWrite)
	assertAccess(t, v, "/var/lib/app/data.db", ReadWrite)
	assertAccess(t, v, "/var/lib/other", ReadOnly)
}

// InaccessiblePaths hides all subpaths and beats every other directive
// regardless of order, including a later ReadWritePaths hole.
func TestInaccessibleSubpaths(t *testing.T) {
	v := mustView(t,
		Directive{KindInaccessiblePaths, "/secrets"},
		Directive{KindReadWritePaths, "/secrets/keys"},
	)
	assertAccess(t, v, "/secrets", Inaccessible)
	assertAccess(t, v, "/secrets/keys", Inaccessible)
	assertAccess(t, v, "/secrets/keys/token.pem", Inaccessible)
	assertAccess(t, v, "/secrets-other", ReadWrite)
	assertAccess(t, v, "/etc", ReadWrite)
}

// A BindPaths target inherits the read-only state of its location in
// the view: under ProtectSystem=strict it is read-only until a
// ReadWritePaths hole is opened on the target.
func TestBindTargetInheritsView(t *testing.T) {
	v := mustView(t,
		Directive{KindProtectSystem, "strict"},
		Directive{KindBindPaths, "/data:/srv/data"},
	)
	binds := v.Binds()
	if len(binds) != 1 || binds[0].Source != "/data" || binds[0].Target != "/srv/data" {
		t.Fatalf("Binds() = %+v", binds)
	}
	assertAccess(t, v, "/srv/data", ReadOnly)
	assertAccess(t, v, "/srv/data/upload", ReadOnly)

	v2 := mustView(t,
		Directive{KindProtectSystem, "strict"},
		Directive{KindBindPaths, "/data:/srv/data"},
		Directive{KindReadWritePaths, "/srv/data"},
	)
	assertAccess(t, v2, "/srv/data", ReadWrite)
	assertAccess(t, v2, "/srv/data/upload", ReadWrite)
}

// ProtectSystem=strict makes the whole root read-only; ReadWritePaths
// punches a writable hole into it.
func TestStrictWithHole(t *testing.T) {
	v := mustView(t,
		Directive{KindProtectSystem, "strict"},
		Directive{KindReadWritePaths, "/var/lib/app"},
	)
	assertAccess(t, v, "/", ReadOnly)
	assertAccess(t, v, "/etc/nginx/nginx.conf", ReadOnly)
	assertAccess(t, v, "/usr/bin/tool", ReadOnly)
	assertAccess(t, v, "/var/lib/app", ReadWrite)
	assertAccess(t, v, "/var/lib/app/data.db", ReadWrite)
	assertAccess(t, v, "/var/lib/other", ReadOnly)
}

// ProtectSystem=full only covers /usr, /boot and /etc.
func TestFull(t *testing.T) {
	v := mustView(t, Directive{KindProtectSystem, "full"})
	assertAccess(t, v, "/usr/lib/libc.so", ReadOnly)
	assertAccess(t, v, "/boot/vmlinuz", ReadOnly)
	assertAccess(t, v, "/etc/passwd", ReadOnly)
	assertAccess(t, v, "/var/log", ReadWrite)
	assertAccess(t, v, "/", ReadWrite)
}

// ParseUnit consumes unit text and preserves directive order.
func TestParseUnit(t *testing.T) {
	unit := "[Unit]\n" +
		"Description=demo\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=/usr/bin/demo\n" +
		"ProtectSystem=strict\n" +
		"ReadWritePaths=/var/lib/demo /var/cache/demo\n" +
		"InaccessiblePaths=/var/lib/demo/keys\n" +
		"BindPaths=/data:/srv/data\n"
	v, err := ParseUnit(strings.NewReader(unit))
	if err != nil {
		t.Fatalf("ParseUnit: %v", err)
	}
	assertAccess(t, v, "/etc", ReadOnly)
	assertAccess(t, v, "/var/lib/demo", ReadWrite)
	assertAccess(t, v, "/var/cache/demo", ReadWrite)
	assertAccess(t, v, "/var/lib/demo/keys", Inaccessible)
	assertAccess(t, v, "/var/lib/demo/keys/id.pem", Inaccessible)
	assertAccess(t, v, "/srv/data", ReadOnly)
}

func TestUnknownProtectSystemLevel(t *testing.T) {
	if _, err := NewView(Directive{KindProtectSystem, "bogus"}); err == nil {
		t.Fatal("expected error for unknown ProtectSystem level")
	}
}
