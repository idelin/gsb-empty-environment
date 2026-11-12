package netsec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("test library"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireDecision(t *testing.T, got Decision, allowed bool, code Code) {
	t.Helper()
	if got.Allowed != allowed || got.Code != code {
		t.Fatalf("decision = %s, want allowed=%v code=%q", got, allowed, code)
	}
}

func TestCompliantLibraryLoadAndFunctionAddress(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "trusted")
	libraryPath := filepath.Join(trusted, "libplugin.dylib")
	testFile(t, libraryPath)

	gateway := NewGateway()
	if err := gateway.RegisterLibrary("plugin-native", trusted, ExportRange{
		Symbol: "plugin_entry",
		Start:  0x1000,
		End:    0x1100,
	}); err != nil {
		t.Fatalf("register library: %v", err)
	}

	got := gateway.Process(Event{
		Kind:        EventLibraryLoad,
		LibraryName: "plugin-native",
		Path:        libraryPath,
	})
	requireDecision(t, got, true, CodeAllow)

	got = gateway.Process(Event{
		Kind:        EventFunctionLookup,
		LibraryName: "plugin-native",
		Address:     0x1000,
	})
	requireDecision(t, got, true, CodeAllow)

	got = gateway.Process(Event{
		Kind:        EventFunctionLookup,
		LibraryName: "plugin-native",
		Address:     0x10ff,
	})
	requireDecision(t, got, true, CodeAllow)
}

func TestLibraryLoadPathTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "trusted")
	outside := filepath.Join(root, "outside")
	libraryPath := filepath.Join(trusted, "libplugin.dylib")
	outsidePath := filepath.Join(outside, "libevil.dylib")
	nestedPath := filepath.Join(trusted, "nested", "libnested.dylib")

	testFile(t, libraryPath)
	testFile(t, outsidePath)
	testFile(t, nestedPath)

	escapeLink := filepath.Join(trusted, "libescape.dylib")
	if err := os.Symlink(outsidePath, escapeLink); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}
	insideLink := filepath.Join(trusted, "libinside.dylib")
	if err := os.Symlink(nestedPath, insideLink); err != nil {
		t.Fatalf("create internal symlink: %v", err)
	}

	gateway := NewGateway()
	if err := gateway.RegisterLibrary("plugin-native", trusted); err != nil {
		t.Fatalf("register library: %v", err)
	}

	got := gateway.LoadLibrary("plugin-native", libraryPath)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.LoadLibrary("plugin-native", "libplugin.dylib")
	requireDecision(t, got, false, CodeRelativePath)

	got = gateway.LoadLibrary("plugin-native", ".."+string(filepath.Separator)+"libplugin.dylib")
	requireDecision(t, got, false, CodePathTraversal)

	got = gateway.LoadLibrary("plugin-native", outsidePath)
	requireDecision(t, got, false, CodePathOutsideDirectory)

	traversalPath := trusted + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside" + string(filepath.Separator) + "libevil.dylib"
	got = gateway.LoadLibrary("plugin-native", traversalPath)
	requireDecision(t, got, false, CodePathTraversal)

	innerTraversal := trusted + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "libplugin.dylib"
	got = gateway.LoadLibrary("plugin-native", innerTraversal)
	requireDecision(t, got, false, CodePathTraversal)

	got = gateway.LoadLibrary("plugin-native", escapeLink)
	requireDecision(t, got, false, CodeSymlinkEscape)

	got = gateway.LoadLibrary("plugin-native", insideLink)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.LoadLibrary("not-registered", libraryPath)
	requireDecision(t, got, false, CodeUnregisteredLibrary)

	got = gateway.LoadLibrary("../plugin-native", libraryPath)
	requireDecision(t, got, false, CodePathTraversal)
}

func TestFunctionAddressMustBeInLoadedExportRange(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "trusted")
	aPath := filepath.Join(trusted, "liba.dylib")
	bPath := filepath.Join(trusted, "libb.dylib")
	testFile(t, aPath)
	testFile(t, bPath)

	gateway := NewGateway()
	if err := gateway.RegisterLibrary("lib-a", trusted, ExportRange{
		Symbol: "entry_a",
		Start:  0x1000,
		End:    0x1100,
	}); err != nil {
		t.Fatalf("register lib-a: %v", err)
	}
	if err := gateway.RegisterLibrary("lib-b", trusted, ExportRange{
		Symbol: "entry_b",
		Start:  0x2000,
		End:    0x2010,
	}); err != nil {
		t.Fatalf("register lib-b: %v", err)
	}

	got := gateway.FunctionAddress("lib-a", 0x1000)
	requireDecision(t, got, false, CodeLibraryNotLoaded)

	got = gateway.LoadLibrary("lib-a", aPath)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.FunctionAddress("lib-b", 0x2000)
	requireDecision(t, got, false, CodeLibraryNotLoaded)

	got = gateway.FunctionAddress("lib-a", 0x1100)
	requireDecision(t, got, false, CodeAddressOutsideExports)

	got = gateway.FunctionAddress("lib-a", 0xdeadbeef)
	requireDecision(t, got, false, CodeAddressOutsideExports)

	got = gateway.FunctionAddress("lib-b", 0xdeadbeef)
	requireDecision(t, got, false, CodeLibraryNotLoaded)

	got = gateway.FunctionAddress("unknown", 0x1000)
	requireDecision(t, got, false, CodeUnregisteredLibrary)

	got = gateway.FunctionAddress("", 0x1000)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.FunctionAddress("", 0x2000)
	requireDecision(t, got, false, CodeAddressOutsideExports)
}

func TestCallbackLifecycleDetectsUseAfterFree(t *testing.T) {
	gateway := NewGateway()
	got := gateway.RegisterNativeThread("native-1")
	requireDecision(t, got, true, CodeAllow)

	got = gateway.RegisterCallback(7)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.TriggerCallback("native-1", 7)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.RegisterCallback(7)
	requireDecision(t, got, false, CodeDuplicateCallback)

	got = gateway.UnregisterCallback(7)
	requireDecision(t, got, true, CodeAllow)

	got = gateway.TriggerCallback("native-1", 7)
	requireDecision(t, got, false, CodeUseAfterFree)

	got = gateway.RegisterCallback(8)
	requireDecision(t, got, true, CodeAllow)
	got = gateway.ReclaimCallback(8)
	requireDecision(t, got, true, CodeAllow)
	got = gateway.TriggerCallback("native-1", 8)
	requireDecision(t, got, false, CodeUseAfterFree)

	got = gateway.TriggerCallback("native-1", 99)
	requireDecision(t, got, false, CodeCallbackNotRegistered)

	got = gateway.TriggerCallback("unknown-native", 7)
	requireDecision(t, got, false, CodeUnregisteredThread)
}

func TestUnregisteredNativeThreadCannotCallHost(t *testing.T) {
	gateway := NewGateway()

	got := gateway.HostCall("native-1", "host.readSecret")
	requireDecision(t, got, false, CodeUnregisteredThread)

	got = gateway.Process(Event{
		Kind:       EventNativeThreadInit,
		ThreadID:   "native-1",
		HostMethod: "host.readSecret",
	})
	requireDecision(t, got, true, CodeAllow)

	got = gateway.HostCall("native-1", "host.readSecret")
	requireDecision(t, got, true, CodeAllow)

	got = gateway.Process(Event{
		Kind:       EventHostCall,
		ThreadID:   "native-2",
		HostMethod: "host.readSecret",
	})
	requireDecision(t, got, false, CodeUnregisteredThread)

	got = gateway.RegisterNativeThread("")
	requireDecision(t, got, false, CodeInvalidThread)
}

func TestRegistrationRejectsInvalidPolicy(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "trusted")
	if err := os.MkdirAll(trusted, 0o755); err != nil {
		t.Fatal(err)
	}

	gateway := NewGateway()
	if err := gateway.RegisterLibrary("lib", "relative"); CodeOf(err) != CodeRelativePath {
		t.Fatalf("relative directory error = %v", err)
	}
	if err := gateway.RegisterLibrary("bad/name", trusted); CodeOf(err) != CodeInvalidLibraryName {
		t.Fatalf("path-like library name error = %v", err)
	}
	if err := gateway.RegisterLibrary("lib", trusted, ExportRange{Start: 0x2000, End: 0x2000}); CodeOf(err) != CodeInvalidExportRange {
		t.Fatalf("empty export range error = %v", err)
	}
	if err := gateway.RegisterLibrary("lib", trusted); err != nil {
		t.Fatalf("register valid library: %v", err)
	}
	if err := gateway.RegisterLibrary("lib", trusted); CodeOf(err) != CodeDuplicateLibrary {
		t.Fatalf("duplicate library error = %v", err)
	}
	if err := gateway.RegisterExport("missing", "entry", 0x3000, 0x3010); CodeOf(err) != CodeUnregisteredLibrary {
		t.Fatalf("unknown library export error = %v", err)
	}
	if err := gateway.RegisterExport("lib", "entry", 0x3000, 0x3010); err != nil {
		t.Fatalf("append export: %v", err)
	}
}

func TestAuditReasonNamesThreadAndCallback(t *testing.T) {
	gateway := NewGateway()
	gateway.RegisterNativeThread("native-1")
	gateway.RegisterCallback(42)
	gateway.UnregisterCallback(42)

	got := gateway.TriggerCallback("native-1", 42)
	if got.Allowed {
		t.Fatal("use-after-free trigger was allowed")
	}
	if !strings.Contains(got.Reason, "42") || !strings.Contains(got.Reason, "unregistered") {
		t.Fatalf("audit reason does not identify callback and state: %q", got.Reason)
	}
}

func TestProcessEventCoverageAndInvalidEvent(t *testing.T) {
	gateway := NewGateway()

	got := gateway.Process(Event{Kind: EventInvalid})
	requireDecision(t, got, false, CodeInvalidEvent)

	got = gateway.Process(Event{Kind: EventNativeThreadInit, ThreadID: "native-1"})
	requireDecision(t, got, true, CodeAllow)

	got = gateway.Process(Event{Kind: EventCallbackRegister, CallbackID: 5})
	requireDecision(t, got, true, CodeAllow)
	got = gateway.Process(Event{Kind: EventCallbackTrigger, ThreadID: "native-1", CallbackID: 5})
	requireDecision(t, got, true, CodeAllow)
	got = gateway.Process(Event{Kind: EventCallbackUnregister, CallbackID: 5})
	requireDecision(t, got, true, CodeAllow)
	got = gateway.Process(Event{Kind: EventCallbackTrigger, ThreadID: "native-1", CallbackID: 5})
	requireDecision(t, got, false, CodeUseAfterFree)

	got = gateway.Process(Event{Kind: EventCallbackRegister, CallbackID: 6})
	requireDecision(t, got, true, CodeAllow)
	got = gateway.Process(Event{Kind: EventCallbackReclaim, CallbackID: 6})
	requireDecision(t, got, true, CodeAllow)
	got = gateway.Process(Event{Kind: EventCallbackTrigger, ThreadID: "native-1", CallbackID: 6})
	requireDecision(t, got, false, CodeUseAfterFree)

	got = gateway.Process(Event{Kind: EventHostCall, ThreadID: "native-1", HostMethod: "host.write"})
	requireDecision(t, got, true, CodeAllow)
}

func TestResolveLibraryPathReturnsCanonicalInsideSymlink(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "trusted")
	nested := filepath.Join(trusted, "nested")
	nestedPath := filepath.Join(nested, "libnested.dylib")
	testFile(t, nestedPath)

	link := filepath.Join(trusted, "liblink.dylib")
	if err := os.Symlink(nestedPath, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	gateway := NewGateway()
	if err := gateway.RegisterLibrary("lib", trusted); err != nil {
		t.Fatal(err)
	}
	got, err := gateway.ResolveLibraryPath("lib", link)
	if err != nil {
		t.Fatalf("resolve internal symlink: %v", err)
	}
	if got != nestedPath {
		t.Fatalf("resolved path = %q, want %q", got, nestedPath)
	}
}
