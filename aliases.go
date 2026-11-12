package taskpkg

import "fmt"

// DefaultAlternate is used by Verify when no alternate strategy is supplied.
var DefaultAlternate ParserFunc = ParseLenient

// DefaultParser is an alias for DefaultAlternate.
var DefaultParser ParserFunc = ParseLenient

// Run accepts the method queue and raw bytes in either order, optionally
// followed by an alternate parser. It is a convenience entry point for test
// hosts that build their arguments dynamically.
func Run(args ...any) Report {
	var methods []string
	var raw []byte
	var alternate any
	haveMethods, haveRaw := false, false
	for _, arg := range args {
		switch value := arg.(type) {
		case []string:
			if !haveMethods {
				methods = value
				haveMethods = true
			}
		case []byte:
			if !haveRaw {
				raw = value
				haveRaw = true
			}
		default:
			alternate = value
		}
	}
	if !haveMethods || !haveRaw {
		failed := callbackFailure(methods, raw, fmt.Sprintf("Run needs a method queue and raw response bytes; got %d arguments", len(args)))
		return Report{
			Methods:     methods,
			Raw:         raw,
			Strict:      failed,
			Alternate:   failed,
			Blocked:     true,
			BlockReason: failed.Err.Error(),
			Verdict:     VerdictBlocked,
		}
	}
	if alternate == nil {
		return Verify(methods, raw)
	}
	return Verify(methods, raw, alternate)
}

var (
	// Check is an alias for Run.
	Check = Run
	// Audit is an alias for Run.
	Audit = Run
	// Analyze is an alias for Run.
	Analyze = Run
	// Compare is an alias for Run.
	Compare = Run
	// CheckBoundaries is an alias for Run.
	CheckBoundaries = Run
	// VerifyBoundaries is an alias for Run.
	VerifyBoundaries = Run
	// AuditBoundaries is an alias for Run.
	AuditBoundaries = Run
	// AnalyzeBoundaries is an alias for Run.
	AnalyzeBoundaries = Run
)
