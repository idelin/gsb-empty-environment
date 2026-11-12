package netsec

// SinkKind categorizes a dangerous capability reachable from the sandbox.
type SinkKind int

const (
	// SinkDebug covers the debug library and every debug.* entry.
	SinkDebug SinkKind = iota + 1
	// SinkIO covers the io library and every io.* entry.
	SinkIO
	// SinkOSExecute is the os.execute entry specifically.
	SinkOSExecute
	// SinkLoad covers load and its variants.
	SinkLoad
	// SinkLoadFile covers loadfile.
	SinkLoadFile
	// SinkDoFile covers dofile.
	SinkDoFile
	// SinkRequire covers require.
	SinkRequire
	// SinkStringDump covers string.dump.
	SinkStringDump
)

// String returns the canonical identifier rendered in findings.
func (s SinkKind) String() string {
	switch s {
	case SinkDebug:
		return "debug.*"
	case SinkIO:
		return "io.*"
	case SinkOSExecute:
		return "os.execute"
	case SinkLoad:
		return "load"
	case SinkLoadFile:
		return "loadfile"
	case SinkDoFile:
		return "dofile"
	case SinkRequire:
		return "require"
	case SinkStringDump:
		return "string.dump"
	default:
		return "unknown"
	}
}

// Well-known builtin identifiers. A graph extractor marks nodes with these
// values via Node.Builtin so classification does not rely solely on the
// surrounding field names.
const (
	BuiltinDebug        = "debug"
	BuiltinDebugTrace   = "debug.traceback"
	BuiltinDebugGetUp   = "debug.getupvalue"
	BuiltinDebugSetUp   = "debug.setupvalue"
	BuiltinDebugGetInfo = "debug.getinfo"
	BuiltinIO           = "io"
	BuiltinIOOpen       = "io.open"
	BuiltinIOLines      = "io.lines"
	BuiltinOS           = "os"
	BuiltinOSExecute    = "os.execute"
	BuiltinLoad         = "load"
	BuiltinLoadFile     = "loadfile"
	BuiltinDoFile       = "dofile"
	BuiltinRequire      = "require"
	BuiltinString       = "string"
	BuiltinStringDump   = "string.dump"
	BuiltinPackage      = "package"
)

// classifyBuiltin maps a Node.Builtin identifier to a sink. It returns
// false for identifiers that are not themselves dangerous, for example
// "string" or "os".
func classifyBuiltin(builtin string) (SinkKind, bool) {
	switch builtin {
	case BuiltinDebug,
		BuiltinDebugTrace,
		BuiltinDebugGetUp,
		BuiltinDebugSetUp,
		BuiltinDebugGetInfo:
		// Any other debug.* identifier matches by prefix below.
		return SinkDebug, true
	case BuiltinIO, BuiltinIOOpen, BuiltinIOLines:
		return SinkIO, true
	case BuiltinOSExecute:
		return SinkOSExecute, true
	case BuiltinLoad:
		return SinkLoad, true
	case BuiltinLoadFile:
		return SinkLoadFile, true
	case BuiltinDoFile:
		return SinkDoFile, true
	case BuiltinRequire:
		return SinkRequire, true
	case BuiltinStringDump:
		return SinkStringDump, true
	}
	// Structural identifiers follow the "library.entry" convention; use it
	// to recognize debug.* entries that the extractor did not enumerate.
	if len(builtin) > len(BuiltinDebug)+1 &&
		builtin[:len(BuiltinDebug)+1] == BuiltinDebug+"." {
		return SinkDebug, true
	}
	return 0, false
}

// classifyStructural is the field-name fallback used when a node carries no
// builtin identifier. parentRole records the library table the node was
// reached through so that os.execute and string.dump can be recognized.
func classifyStructural(e Edge, parentRole libRole) (SinkKind, bool) {
	switch e.Kind {
	case EdgeField:
		switch e.Key {
		case "debug":
			return SinkDebug, true
		case "io":
			return SinkIO, true
		case "load":
			return SinkLoad, true
		case "loadfile":
			return SinkLoadFile, true
		case "dofile":
			return SinkDoFile, true
		case "require":
			return SinkRequire, true
		case "execute":
			if parentRole == roleOS {
				return SinkOSExecute, true
			}
		case "dump":
			if parentRole == roleString {
				return SinkStringDump, true
			}
		}
	case EdgeIndex:
		// An __index that forwards into a dangerous table is reported when
		// the forwarded node is itself classified; nothing to do here.
	}
	return 0, false
}

// libRole tags which well-known library table a node represents, so nested
// field lookups can be classified without relying on builtin markers.
type libRole int

const (
	roleNone libRole = iota
	roleDebug
	roleIO
	roleOS
	roleString
)

// roleForBuiltin maps a builtin identifier to its library role.
func roleForBuiltin(builtin string) libRole {
	switch builtin {
	case BuiltinDebug:
		return roleDebug
	case BuiltinIO:
		return roleIO
	case BuiltinOS:
		return roleOS
	case BuiltinString:
		return roleString
	}
	return roleNone
}

// roleForField maps a field name leading into a library table to a role.
func roleForField(key string) libRole {
	switch key {
	case "debug":
		return roleDebug
	case "io":
		return roleIO
	case "os":
		return roleOS
	case "string":
		return roleString
	}
	return roleNone
}
