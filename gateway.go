package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Code is a stable machine-readable result code returned by the gateway.
type Code string

const (
	CodeAllow                 Code = "allow"
	CodeInvalidEvent          Code = "invalid_event"
	CodeInvalidLibraryName    Code = "invalid_library_name"
	CodeUnregisteredLibrary   Code = "unregistered_library"
	CodeDuplicateLibrary      Code = "duplicate_library"
	CodeRelativePath          Code = "relative_path"
	CodePathTraversal         Code = "path_traversal"
	CodePathOutsideDirectory  Code = "path_outside_directory"
	CodeSymlinkEscape         Code = "symlink_escape"
	CodePathResolutionFailed  Code = "path_resolution_failed"
	CodeInvalidLibraryPath    Code = "invalid_library_path"
	CodeLibraryNotLoaded      Code = "library_not_loaded"
	CodeAddressOutsideExports Code = "address_outside_exports"
	CodeInvalidExportRange    Code = "invalid_export_range"
	CodeInvalidCallback       Code = "invalid_callback"
	CodeDuplicateCallback     Code = "duplicate_callback"
	CodeCallbackNotRegistered Code = "callback_not_registered"
	CodeCallbackNotActive     Code = "callback_not_active"
	CodeUseAfterFree          Code = "use_after_free"
	CodeInvalidThread         Code = "invalid_thread"
	CodeUnregisteredThread    Code = "unregistered_thread"
)

// PolicyError describes a rejected policy registration or an invalid policy.
type PolicyError struct {
	Code   Code
	Detail string
}

func (e *PolicyError) Error() string {
	if e.Detail == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Detail
}

// CodeOf returns the policy code carried by err, or the empty Code if err is
// not a PolicyError.
func CodeOf(err error) Code {
	var policyErr *PolicyError
	if errors.As(err, &policyErr) {
		return policyErr.Code
	}
	return ""
}

func policyError(code Code, format string, args ...any) *PolicyError {
	return &PolicyError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Decision is the audit result for one JNA event.
type Decision struct {
	Allowed bool
	Code    Code
	Reason  string
}

func (d Decision) String() string {
	verdict := "deny"
	if d.Allowed {
		verdict = "allow"
	}
	if d.Reason == "" {
		return verdict + " [" + string(d.Code) + "]"
	}
	return verdict + " [" + string(d.Code) + "]: " + d.Reason
}

func allow(format string, args ...any) Decision {
	return Decision{Allowed: true, Code: CodeAllow, Reason: fmt.Sprintf(format, args...)}
}

func deny(code Code, format string, args ...any) Decision {
	return Decision{Allowed: false, Code: code, Reason: fmt.Sprintf(format, args...)}
}

// EventKind identifies the JNA event being evaluated.
type EventKind uint8

const (
	EventInvalid EventKind = iota
	// EventLibraryLoad reports a Native.load call. LibraryName is the caller
	// supplied name and Path is the resolved file path JNA is about to load.
	EventLibraryLoad
	// EventFunctionLookup reports Function.getFunction. LibraryName is optional
	// when it is unambiguous; Address is the resolved native function pointer.
	EventFunctionLookup
	// EventCallbackRegister reports that a callback object was handed to native
	// code. CallbackID is the stable identifier used by later events.
	EventCallbackRegister
	// EventCallbackUnregister reports that native code no longer owns the
	// callback. It is distinct from reclamation.
	EventCallbackUnregister
	// EventCallbackReclaim reports that the host reclaimed the callback object
	// (for example, after a GC or explicit dispose).
	EventCallbackReclaim
	// EventCallbackTrigger reports a native invocation. ThreadID identifies the
	// native thread and CallbackID identifies the target callback.
	EventCallbackTrigger
	// EventNativeThreadInit registers a native thread before it calls host
	// sensitive methods.
	EventNativeThreadInit
	// EventHostCall reports that a native thread is calling a host sensitive
	// method. HostMethod is optional audit metadata.
	EventHostCall
)

func (k EventKind) String() string {
	switch k {
	case EventLibraryLoad:
		return "library_load"
	case EventFunctionLookup:
		return "function_get_function"
	case EventCallbackRegister:
		return "callback_register"
	case EventCallbackUnregister:
		return "callback_unregister"
	case EventCallbackReclaim:
		return "callback_reclaim"
	case EventCallbackTrigger:
		return "callback_trigger"
	case EventNativeThreadInit:
		return "native_thread_init"
	case EventHostCall:
		return "host_call"
	default:
		return "invalid"
	}
}

// Event is the common envelope for all JNA subscription events.
type Event struct {
	Kind EventKind

	// LibraryName is the exact caller-registered library name. It must not be
	// a path and is never interpreted as one.
	LibraryName string
	// Path is the resolved path supplied by JNA for a library load event.
	Path string
	// Address is the resolved Function.getFunction address.
	Address uint64
	// CallbackID identifies a callback registration.
	CallbackID uint64
	// ThreadID identifies the native thread for trigger and host-call events.
	ThreadID string
	// HostMethod is optional audit metadata for a host sensitive call.
	HostMethod string
}

// ExportRange is a trusted exported-symbol address range for a registered
// library. End is exclusive. Trusted callers should obtain these ranges from
// the library's own symbol table, never from plugin input.
type ExportRange struct {
	Symbol string
	Start  uint64
	End    uint64
}

// LibraryRegistration is a complete trusted library policy entry.
type LibraryRegistration struct {
	Name      string
	Directory string
	Exports   []ExportRange
}

type callbackPhase uint8

const (
	callbackActive callbackPhase = iota + 1
	callbackRetired
	callbackReclaimed
)

func (p callbackPhase) String() string {
	switch p {
	case callbackActive:
		return "active"
	case callbackRetired:
		return "unregistered"
	case callbackReclaimed:
		return "reclaimed"
	default:
		return "unknown"
	}
}

type callbackRecord struct {
	phase callbackPhase
}

type libraryRecord struct {
	dir      string
	exports  []ExportRange
	loaded   bool
	loadPath string
}

// Gateway is a concurrency-safe JNA permission and callback-lifecycle audit
// point. Registrations represent trusted host policy; events represent
// untrusted plugin/native input.
type Gateway struct {
	mu        sync.Mutex
	libraries map[string]*libraryRecord
	threads   map[string]struct{}
	callbacks map[uint64]*callbackRecord
}

// NewGateway creates an empty gateway. Libraries and native threads must be
// registered by trusted host code before JNA events are accepted.
func NewGateway() *Gateway {
	return &Gateway{
		libraries: make(map[string]*libraryRecord),
		threads:   make(map[string]struct{}),
		callbacks: make(map[uint64]*callbackRecord),
	}
}

// New is an alias for NewGateway.
func New() *Gateway {
	return NewGateway()
}

// RegisterLibrary adds a trusted library name rooted at directory. The
// directory is canonicalized through symlinks at registration time. Optional
// exports may be supplied here or later with RegisterExport(s).
func (g *Gateway) RegisterLibrary(name, directory string, exports ...ExportRange) error {
	return g.RegisterLibraryPolicy(LibraryRegistration{
		Name:      name,
		Directory: directory,
		Exports:   exports,
	})
}

// RegisterLibraryPolicy adds a trusted library policy entry atomically.
func (g *Gateway) RegisterLibraryPolicy(registration LibraryRegistration) error {
	if err := validateLibraryName(registration.Name); err != nil {
		return err
	}
	dir, err := canonicalDirectory(registration.Directory)
	if err != nil {
		return err
	}
	exports, err := validateExports(registration.Exports)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.libraries[registration.Name]; exists {
		return policyError(CodeDuplicateLibrary, "library %q is already registered", registration.Name)
	}
	g.libraries[registration.Name] = &libraryRecord{
		dir:     dir,
		exports: exports,
	}
	return nil
}

// RegisterExports appends trusted export ranges to an already registered
// library. It may be called before or after the library load event.
func (g *Gateway) RegisterExports(libraryName string, exports []ExportRange) error {
	if err := validateLibraryName(libraryName); err != nil {
		return err
	}
	validated, err := validateExports(exports)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	library, exists := g.libraries[libraryName]
	if !exists {
		return policyError(CodeUnregisteredLibrary, "library %q is not registered", libraryName)
	}
	library.exports = append(library.exports, validated...)
	return nil
}

// RegisterExport appends one trusted export range. End is exclusive.
func (g *Gateway) RegisterExport(libraryName, symbol string, start, end uint64) error {
	return g.RegisterExports(libraryName, []ExportRange{{
		Symbol: symbol,
		Start:  start,
		End:    end,
	}})
}

// RegisterNativeThread records a native thread during initialization.
func (g *Gateway) RegisterNativeThread(threadID string) Decision {
	if strings.TrimSpace(threadID) == "" {
		return deny(CodeInvalidThread, "native thread identifier is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.registerThreadLocked(threadID)
}

func (g *Gateway) registerThreadLocked(threadID string) Decision {
	if _, exists := g.threads[threadID]; exists {
		return allow("native thread %q was already initialized", threadID)
	}
	g.threads[threadID] = struct{}{}
	return allow("native thread %q initialized", threadID)
}

// HostCall checks whether a native thread may invoke a host sensitive method.
func (g *Gateway) HostCall(threadID, method string) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hostCallLocked(threadID, method)
}

func (g *Gateway) hostCallLocked(threadID, method string) Decision {
	if strings.TrimSpace(threadID) == "" {
		return deny(CodeUnregisteredThread, "host method %q called without a native thread identity", method)
	}
	if _, exists := g.threads[threadID]; !exists {
		return deny(CodeUnregisteredThread, "host method %q called by unregistered native thread %q", method, threadID)
	}
	return allow("host method %q authorized for native thread %q", method, threadID)
}

// RegisterCallback records a callback object that native code may invoke.
func (g *Gateway) RegisterCallback(callbackID uint64) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.registerCallbackLocked(callbackID)
}

func (g *Gateway) registerCallbackLocked(callbackID uint64) Decision {
	if callbackID == 0 {
		return deny(CodeInvalidCallback, "callback identifier 0 is reserved")
	}
	if record, exists := g.callbacks[callbackID]; exists {
		return deny(CodeDuplicateCallback, "callback %d is already registered (phase %s)", callbackID, record.phase)
	}
	g.callbacks[callbackID] = &callbackRecord{phase: callbackActive}
	return allow("callback %d registered", callbackID)
}

// UnregisterCallback records that native code no longer owns the callback.
// A later trigger is reported as use-after-free.
func (g *Gateway) UnregisterCallback(callbackID uint64) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.unregisterCallbackLocked(callbackID)
}

func (g *Gateway) unregisterCallbackLocked(callbackID uint64) Decision {
	if callbackID == 0 {
		return deny(CodeInvalidCallback, "callback identifier 0 is reserved")
	}
	record, exists := g.callbacks[callbackID]
	if !exists {
		return deny(CodeCallbackNotRegistered, "callback %d is not registered", callbackID)
	}
	if record.phase != callbackActive {
		return deny(CodeCallbackNotActive, "callback %d is already %s", callbackID, record.phase)
	}
	record.phase = callbackRetired
	return allow("callback %d unregistered", callbackID)
}

// ReclaimCallback records that the host reclaimed the callback object. A
// later native trigger is reported as use-after-free.
func (g *Gateway) ReclaimCallback(callbackID uint64) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reclaimCallbackLocked(callbackID)
}

func (g *Gateway) reclaimCallbackLocked(callbackID uint64) Decision {
	if callbackID == 0 {
		return deny(CodeInvalidCallback, "callback identifier 0 is reserved")
	}
	record, exists := g.callbacks[callbackID]
	if !exists {
		return deny(CodeCallbackNotRegistered, "callback %d is not registered", callbackID)
	}
	record.phase = callbackReclaimed
	return allow("callback %d reclaimed", callbackID)
}

// TriggerCallback validates a native callback invocation.
func (g *Gateway) TriggerCallback(threadID string, callbackID uint64) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.triggerCallbackLocked(threadID, callbackID)
}

func (g *Gateway) triggerCallbackLocked(threadID string, callbackID uint64) Decision {
	if strings.TrimSpace(threadID) == "" {
		return deny(CodeUnregisteredThread, "callback %d triggered without a native thread identity", callbackID)
	}
	if _, exists := g.threads[threadID]; !exists {
		return deny(CodeUnregisteredThread, "callback %d triggered by unregistered native thread %q", callbackID, threadID)
	}
	if callbackID == 0 {
		return deny(CodeInvalidCallback, "callback identifier 0 is reserved")
	}

	record, exists := g.callbacks[callbackID]
	if !exists {
		return deny(CodeCallbackNotRegistered, "callback %d was never registered", callbackID)
	}
	if record.phase != callbackActive {
		return deny(CodeUseAfterFree, "callback %d triggered after it was %s", callbackID, record.phase)
	}
	return allow("callback %d triggered by native thread %q", callbackID, threadID)
}

// ResolveLibraryPath validates a library name/path pair without marking the
// library loaded. The returned path is canonical and safe to hand to JNA.
func (g *Gateway) ResolveLibraryPath(libraryName, candidate string) (string, error) {
	if err := validateLibraryName(libraryName); err != nil {
		return "", err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	library, exists := g.libraries[libraryName]
	if !exists {
		return "", policyError(CodeUnregisteredLibrary, "library %q is not registered", libraryName)
	}
	canonical, policyErr := resolvePath(library.dir, candidate)
	if policyErr != nil {
		return "", policyErr
	}
	return canonical, nil
}

// LoadLibrary validates and records a library load event.
func (g *Gateway) LoadLibrary(libraryName, resolvedPath string) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.loadLibraryLocked(libraryName, resolvedPath)
}

func (g *Gateway) loadLibraryLocked(libraryName, resolvedPath string) Decision {
	if err := validateLibraryName(libraryName); err != nil {
		return deny(err.Code, "%s", err.Detail)
	}
	library, exists := g.libraries[libraryName]
	if !exists {
		return deny(CodeUnregisteredLibrary, "library %q is not registered", libraryName)
	}

	canonical, err := resolvePath(library.dir, resolvedPath)
	if err != nil {
		return deny(err.Code, "%s", err.Detail)
	}

	library.loaded = true
	library.loadPath = canonical
	return allow("library %q loaded from %q", libraryName, canonical)
}

// FunctionAddress validates a Function.getFunction result. If libraryName is
// empty, every currently loaded registered library is considered.
func (g *Gateway) FunctionAddress(libraryName string, address uint64) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.functionAddressLocked(libraryName, address)
}

func (g *Gateway) functionAddressLocked(libraryName string, address uint64) Decision {
	if libraryName != "" {
		if err := validateLibraryName(libraryName); err != nil {
			return deny(err.Code, "%s", err.Detail)
		}
		library, exists := g.libraries[libraryName]
		if !exists {
			return deny(CodeUnregisteredLibrary, "library %q is not registered", libraryName)
		}
		if !library.loaded {
			return deny(CodeLibraryNotLoaded, "library %q has not been loaded", libraryName)
		}
		if withinExports(library.exports, address) {
			return allow("address %#x is inside a registered export of library %q", address, libraryName)
		}
		return deny(CodeAddressOutsideExports, "address %#x is outside the registered exports of library %q", address, libraryName)
	}

	var loaded bool
	for name, library := range g.libraries {
		if !library.loaded {
			continue
		}
		loaded = true
		if withinExports(library.exports, address) {
			return allow("address %#x is inside a registered export of library %q", address, name)
		}
	}
	if !loaded {
		return deny(CodeLibraryNotLoaded, "no registered library has been loaded")
	}
	return deny(CodeAddressOutsideExports, "address %#x is outside every loaded library export", address)
}

func withinExports(exports []ExportRange, address uint64) bool {
	for _, export := range exports {
		if address >= export.Start && address < export.End {
			return true
		}
	}
	return false
}

// Process evaluates one event against the trusted policy. Registration and
// lifecycle transitions are applied only when the event is accepted.
func (g *Gateway) Process(event Event) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch event.Kind {
	case EventLibraryLoad:
		return g.loadLibraryLocked(event.LibraryName, event.Path)
	case EventFunctionLookup:
		return g.functionAddressLocked(event.LibraryName, event.Address)
	case EventCallbackRegister:
		return g.registerCallbackLocked(event.CallbackID)
	case EventCallbackUnregister:
		return g.unregisterCallbackLocked(event.CallbackID)
	case EventCallbackReclaim:
		return g.reclaimCallbackLocked(event.CallbackID)
	case EventCallbackTrigger:
		return g.triggerCallbackLocked(event.ThreadID, event.CallbackID)
	case EventNativeThreadInit:
		if strings.TrimSpace(event.ThreadID) == "" {
			return deny(CodeInvalidThread, "native thread identifier is empty")
		}
		return g.registerThreadLocked(event.ThreadID)
	case EventHostCall:
		return g.hostCallLocked(event.ThreadID, event.HostMethod)
	default:
		return deny(CodeInvalidEvent, "unknown event kind %d", event.Kind)
	}
}

func validateLibraryName(name string) *PolicyError {
	if strings.TrimSpace(name) == "" {
		return policyError(CodeInvalidLibraryName, "library name is empty")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return policyError(CodeInvalidLibraryName, "library name contains NUL")
	}
	if hasParentSegment(name) {
		return policyError(CodePathTraversal, "library name %q contains a parent-directory segment", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return policyError(CodeInvalidLibraryName, "library name %q must not contain path separators", name)
	}
	if name == "." {
		return policyError(CodeInvalidLibraryName, "library name must not be %q", ".")
	}
	return nil
}

func canonicalDirectory(directory string) (string, *PolicyError) {
	if strings.TrimSpace(directory) == "" {
		return "", policyError(CodeInvalidLibraryPath, "registered directory is empty")
	}
	if hasParentSegment(directory) {
		return "", policyError(CodePathTraversal, "registered directory %q contains a parent-directory segment", directory)
	}
	if !filepath.IsAbs(directory) {
		return "", policyError(CodeRelativePath, "registered directory %q is not absolute", directory)
	}

	clean := filepath.Clean(directory)
	info, err := os.Stat(clean)
	if err != nil {
		return "", policyError(CodeInvalidLibraryPath, "registered directory %q: %v", directory, err)
	}
	if !info.IsDir() {
		return "", policyError(CodeInvalidLibraryPath, "registered path %q is not a directory", directory)
	}

	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", policyError(CodePathResolutionFailed, "cannot resolve registered directory %q: %v", directory, err)
	}
	return canonical, nil
}

func validateExports(exports []ExportRange) ([]ExportRange, *PolicyError) {
	validated := make([]ExportRange, 0, len(exports))
	for _, export := range exports {
		if export.End <= export.Start {
			return nil, policyError(CodeInvalidExportRange, "export %q has invalid range [%#x, %#x)", export.Symbol, export.Start, export.End)
		}
		validated = append(validated, export)
	}
	return validated, nil
}

func resolvePath(directory, candidate string) (string, *PolicyError) {
	if strings.TrimSpace(candidate) == "" {
		return "", policyError(CodeInvalidLibraryPath, "library path is empty")
	}
	if hasParentSegment(candidate) {
		return "", policyError(CodePathTraversal, "library path %q contains a parent-directory segment", candidate)
	}
	if !filepath.IsAbs(candidate) {
		return "", policyError(CodeRelativePath, "library path %q is not absolute", candidate)
	}

	clean := filepath.Clean(candidate)
	if !within(directory, clean) || clean == directory {
		return "", policyError(CodePathOutsideDirectory, "library path %q is outside registered directory %q", candidate, directory)
	}

	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", policyError(CodePathResolutionFailed, "cannot resolve library path %q: %v", candidate, err)
	}
	if !within(directory, canonical) || canonical == directory {
		return "", policyError(CodeSymlinkEscape, "library path %q resolves outside registered directory %q", candidate, directory)
	}

	info, err := os.Stat(canonical)
	if err != nil {
		return "", policyError(CodePathResolutionFailed, "cannot stat library path %q: %v", candidate, err)
	}
	if !info.Mode().IsRegular() {
		return "", policyError(CodeInvalidLibraryPath, "library path %q is not a regular file", candidate)
	}
	return canonical, nil
}

func hasParentSegment(value string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(value), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func within(directory, candidate string) bool {
	rel, err := filepath.Rel(directory, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if filepath.IsAbs(rel) || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
