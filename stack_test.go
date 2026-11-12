package netsec

import "testing"

func TestConfusedDeputyRequiresIntersection(t *testing.T) {
	t.Parallel()

	plugin := NewProtectionDomain(
		"plugin",
		NewFilePermission("/plugin/p1/*", Read),
	)
	library := NewProtectionDomain(
		"library",
		NewFilePermission("/-", Read, Write),
	)

	frames := []StackFrame{
		Frame(library),
		Frame(plugin),
	}

	if !CanRead(frames, "/plugin/p1/public.txt") {
		t.Fatal("both domains allow the plugin's own public file")
	}
	if CanWrite(frames, "/plugin/p1/public.txt") {
		t.Fatal("library grant must not add write access to the plugin")
	}
	if CanRead(frames, "/plugin/p1/private/file.txt") {
		t.Fatal("plugin's direct-child grant must not allow a nested file")
	}
	for _, path := range []string{"/data/secret", "/plugin/p2/secret"} {
		if CanRead(frames, path) || CanWrite(frames, path) {
			t.Fatalf("library must not act as a confused deputy for %q", path)
		}
	}
}

func TestDoPrivilegedTruncatesOlderCallers(t *testing.T) {
	t.Parallel()

	library := NewProtectionDomain("library", NewFilePermission("/data/-", Read))
	denyingCaller := NewProtectionDomain("denied-plugin")

	withoutPrivilege := []StackFrame{
		Frame(library),
		Frame(denyingCaller),
	}
	if CanRead(withoutPrivilege, "/data/secret") {
		t.Fatal("ordinary intersection must include the caller and deny access")
	}

	withPrivilege := []StackFrame{
		DoPrivileged(library),
		Frame(denyingCaller),
	}
	if !CanRead(withPrivilege, "/data/secret") {
		t.Fatal("doPrivileged frame must truncate the denying caller")
	}

	if Check(withPrivilege, NewFilePermission("/data/secret", Write)) {
		t.Fatal("privileged frame must still be limited by its own grant actions")
	}
}

func TestFirstPrivilegedFrameDefinesBoundary(t *testing.T) {
	t.Parallel()

	library := NewProtectionDomain("library", NewFilePermission("/data/-", Read))
	denying := NewProtectionDomain("denied")

	frames := []StackFrame{
		DoPrivileged(library),
		DoPrivileged(denying),
		Frame(denying),
	}
	if !CanRead(frames, "/data/secret") {
		t.Fatal("the first privileged frame must hide all older frames")
	}

	frames = []StackFrame{
		Frame(library),
		DoPrivileged(denying),
	}
	if CanRead(frames, "/data/secret") {
		t.Fatal("domains before the privileged boundary still participate")
	}
}

func TestLimitedDoPrivilegedIntersectsLimitedDomain(t *testing.T) {
	t.Parallel()

	library := NewProtectionDomain(
		"library",
		NewFilePermission("/-", Read, Write),
	)
	limited := NewProtectionDomain(
		"limited",
		NewFilePermission("/plugin/p1/*", Read),
	)
	caller := NewProtectionDomain(
		"caller",
		NewFilePermission("/plugin/p1/*", Read),
	)

	frames := []StackFrame{
		DoPrivilegedLimited(library, limited),
		Frame(caller),
	}
	if !CanRead(frames, "/plugin/p1/public.txt") {
		t.Fatal("limited privileged frame should allow the limited direct child")
	}
	if CanWrite(frames, "/plugin/p1/public.txt") {
		t.Fatal("limited domain's read-only grant must not gain write access")
	}
	for _, path := range []string{"/plugin/p1/private/file.txt", "/data/secret"} {
		if CanRead(frames, path) {
			t.Fatalf("limited doPrivileged must not allow %q", path)
		}
	}
}

func TestLimitedDoPrivilegedStillIntersectsFramesAboveBoundary(t *testing.T) {
	t.Parallel()

	library := NewProtectionDomain("library", NewFilePermission("/-", Read))
	pluginFrame := NewProtectionDomain("plugin-frame", NewFilePermission("/plugin/p1/-", Read))
	limited := NewProtectionDomain("limited", NewFilePermission("/plugin/p1/*", Read))

	frames := []StackFrame{
		Frame(pluginFrame),
		DoPrivilegedLimited(library, limited),
		Frame(NewProtectionDomain("older-denied")),
	}
	if !CanRead(frames, "/plugin/p1/public.txt") {
		t.Fatal("all domains above the boundary and the limited domain should allow")
	}
	if CanRead(frames, "/plugin/p1/nested/file.txt") {
		t.Fatal("the limited direct-child domain must narrow the plugin frame's recursive grant")
	}
}

func TestAllFramesMustAllowEachAction(t *testing.T) {
	t.Parallel()

	readOnly := NewProtectionDomain("read", NewFilePermission("/shared/file", Read))
	writeOnly := NewProtectionDomain("write", NewFilePermission("/shared/file", Write))

	frames := []StackFrame{Frame(readOnly), Frame(writeOnly)}
	if Check(frames, NewFilePermission("/shared/file", Read)) {
		t.Fatal("one stack frame lacks read access")
	}
	if Check(frames, NewFilePermission("/shared/file", Write)) {
		t.Fatal("one stack frame lacks write access")
	}
	if Check(frames, NewFilePermission("/shared/file", Read, Write)) {
		t.Fatal("actions may not be split across different domains in the intersection")
	}
}

func TestEmptyStackAndLimitedDomainWithoutPrivilegeFailClosed(t *testing.T) {
	t.Parallel()

	if Check(nil, NewFilePermission("/data/file", Read)) {
		t.Fatal("empty stack must deny access")
	}

	ordinary := StackFrame{
		Domain:        NewProtectionDomain("broad", NewFilePermission("/-", Read)),
		LimitedDomain: &ProtectionDomain{Name: "ignored"},
	}
	if !CanRead([]StackFrame{ordinary}, "/data/file") {
		t.Fatal("LimitedDomain without Privileged must not change ordinary frame semantics")
	}

	emptyDomain := []StackFrame{Frame(ProtectionDomain{Name: "empty"})}
	if CanRead(emptyDomain, "/data/file") {
		t.Fatal("a domain with no grants must deny access")
	}
}
