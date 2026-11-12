package ionfilter

import "errors"

// System symbol texts indexed by their well known symbol IDs. SID 0 is the
// anonymous/unknown symbol and SIDs 1-9 are fixed by the Ion specification.
var systemSymbols = []string{
	0: "$0",
	1: "$ion",
	2: "$ion_1_0",
	3: "$ion_symbol_table",
	4: "name",
	5: "version",
	6: "imports",
	7: "symbols",
	8: "max_id",
	9: "$ion_shared_symbol_table",
}

const (
	annSymbolTable       = "$ion_symbol_table"
	annSharedSymbolTable = "$ion_shared_symbol_table"
)

// errUnknownSymbol is returned when a symbol ID cannot be resolved to text,
// either because it is outside every known table or because it denotes an
// explicit placeholder. Scanners translate it into a policy Violation.
var errUnknownSymbol = errors.New("ionfilter: symbol id has no known text")

// symbolTable maps symbol IDs to text for the active local symbol table.
// The slice index is the SID; empty entries are explicit placeholders that
// must not be resolved.
type symbolTable struct {
	symbols []string
}

func newSymbolTable(extra []string) *symbolTable {
	table := &symbolTable{}
	table.reset()
	table.symbols = append(table.symbols, extra...)
	return table
}

// reset drops every local symbol, restoring the system symbol table only.
// This models the semantics of an IVM starting a new encoding group.
func (t *symbolTable) reset() {
	t.symbols = append(t.symbols[:0:0], systemSymbols...)
}

// resolve maps a SID to its text. It fails on placeholders and out of range
// IDs so that an attacker cannot smuggle an unresolvable annotation past the
// allow-list check.
func (t *symbolTable) resolve(sid int) (string, error) {
	if sid <= 0 || sid >= len(t.symbols) {
		return "", errUnknownSymbol
	}
	text := t.symbols[sid]
	if text == "" {
		return "", errUnknownSymbol
	}
	return text, nil
}

// appendLocal applies the "symbols" list of a trusted local symbol table
// directive. Empty strings and nulls are encoded as placeholders.
func (t *symbolTable) appendLocal(names []string) {
	t.symbols = append(t.symbols, names...)
}

// isDirectiveAnnotation reports whether text names a system level symbol
// table directive rather than an ordinary user annotation.
func isDirectiveAnnotation(text string) bool {
	return text == annSymbolTable || text == annSharedSymbolTable
}
