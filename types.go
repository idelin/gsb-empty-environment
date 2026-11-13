package netsec

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrInvalidTable reports a structurally invalid or ambiguous export table.
	ErrInvalidTable = errors.New("netsec: invalid export table")
	// ErrIncomplete reports that a caller-supplied work budget was exhausted.
	ErrIncomplete = errors.New("netsec: export load incomplete")
	// ErrForwardCycle reports a cycle in a chain of forwarded exports.
	ErrForwardCycle = errors.New("netsec: export forward cycle")
	// ErrDuplicateExport reports two terminal nodes with the same full name.
	ErrDuplicateExport = errors.New("netsec: duplicate export name")
	// ErrAddressOutOfRange reports an address outside a module's executable ranges.
	ErrAddressOutOfRange = errors.New("netsec: export address outside executable range")
	// ErrExportNotFound reports a forward to an undefined target export.
	ErrExportNotFound = errors.New("netsec: forwarded export not found")
	// ErrModuleNotFound reports a forward to a module absent from the host table.
	ErrModuleNotFound = errors.New("netsec: forwarded module not found")
)

// Flags is the terminal-kind byte stored in a non-empty terminal area.
type Flags uint8

const (
	FlagAddress Flags = 0
	// FlagRegular is the Mach-O-style name for a direct address terminal.
	FlagRegular Flags = FlagAddress
	FlagForward Flags = 8
	// FlagReexport is the Mach-O-style name for a forwarded terminal.
	FlagReexport Flags = FlagForward
)

// Range is a half-open address interval [Start, End).
type Range struct {
	Start uint64
	End   uint64
}

// Segment is an alias for Range.
type Segment = Range

// Contains reports whether address is in [Start, End).
func (r Range) Contains(address uint64) bool {
	return r.Start <= address && address < r.End
}

// Module describes one host module. Table is that module's compressed export
// table, whose root node is always at byte offset zero. Executable lists all
// address intervals in which an exported address is allowed to land.
type Module struct {
	Table      []byte
	Executable []Range
}

// ModuleTable is a convenient map-backed module host.
type ModuleTable map[uint64]Module

// Module returns the module stored at index.
func (m ModuleTable) Module(index uint64) (Module, bool) {
	module, ok := m[index]
	return module, ok
}

// GetModule returns the module stored at index.
func (m ModuleTable) GetModule(index uint64) (Module, bool) {
	return m.Module(index)
}

// PointerModuleTable is a map-backed module host using pointers.
type PointerModuleTable map[uint64]*Module

// Module returns the module stored at index.
func (m PointerModuleTable) Module(index uint64) (*Module, bool) {
	module, ok := m[index]
	return module, ok
}

// GetModule returns the module stored at index.
func (m PointerModuleTable) GetModule(index uint64) (*Module, bool) {
	return m.Module(index)
}

// ModuleProvider is the smallest host interface needed by Load.
type ModuleProvider interface {
	Module(index uint64) (Module, bool)
}

// Limits bounds work performed by parsing and name enumeration.
//
// A zero field selects the corresponding DefaultLimits value, so callers can
// override only the budgets that are relevant to them.
type Limits struct {
	// MaxSteps limits node expansions while enumerating every path in the
	// shared export DAG. Visiting the same node through a second path costs
	// another step; this is what makes diamond sharing exhaustive.
	MaxSteps uint64
	// MaxNameBytes limits the cumulative bytes of all generated full names.
	MaxNameBytes uint64
	// MaxExports limits the number of distinct terminal export names.
	MaxExports uint64
}

// Budget is an alias for Limits.
type Budget = Limits

const (
	// DefaultMaxSteps is used when Limits.MaxSteps is zero.
	DefaultMaxSteps uint64 = 1 << 20
	// DefaultMaxNameBytes is used when Limits.MaxNameBytes is zero.
	DefaultMaxNameBytes uint64 = 16 << 20
	// DefaultMaxExports is used when Limits.MaxExports is zero.
	DefaultMaxExports uint64 = 1 << 20
)

// DefaultLimits returns the budgets used for zero-valued limit fields.
func DefaultLimits() Limits {
	return Limits{
		MaxSteps:     DefaultMaxSteps,
		MaxNameBytes: DefaultMaxNameBytes,
		MaxExports:   DefaultMaxExports,
	}
}

func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxSteps == 0 {
		l.MaxSteps = d.MaxSteps
	}
	if l.MaxNameBytes == 0 {
		l.MaxNameBytes = d.MaxNameBytes
	}
	if l.MaxExports == 0 {
		l.MaxExports = d.MaxExports
	}
	return l
}

// Terminal is the decoded, non-empty terminal area of a node.
type Terminal struct {
	Flags       Flags
	Address     uint64
	Module      uint64
	ForwardName string
}

// Edge is one labelled child edge. Target is resolved during the structural
// parse; Offset is the absolute byte offset read from the table.
type Edge struct {
	Label  string
	Offset uint64
	Target *Node
}

// Node is one fixed byte interval from a parsed export table.
type Node struct {
	Offset   uint64
	End      uint64
	Terminal *Terminal
	Children []Edge
}

// ParsedTable is a structurally validated export DAG. Nodes is keyed by the
// node's absolute start offset. A node can have many parents but one fixed
// interval within a parse.
type ParsedTable struct {
	Root  *Node
	Nodes map[uint64]*Node

	rawExports []RawExport
}

// ExportTrie is an alias for ParsedTable.
type ExportTrie = ParsedTable

// RawExport is an export name before forward targets are bound.
type RawExport struct {
	Name string
	// Module is the module that owns the terminal. Enumerate sets it.
	Module uint64
	Flags  Flags
	// Address is populated for FlagAddress.
	Address uint64
	// TargetModule and ForwardName are populated for FlagForward.
	TargetModule uint64
	ForwardName  string
}

// RawExports returns the names enumerated by ParseExportTable. The returned
// slice is a copy.
func (t *ParsedTable) RawExports() []RawExport {
	if t == nil {
		return nil
	}
	return append([]RawExport(nil), t.rawExports...)
}

// Enumerate walks every root-to-node path and returns all terminal exports.
// This intentionally does not use a global visited set: shared nodes reached
// through different prefixes produce different full names and all of them are
// emitted. Cycles and partially overlapping node intervals are rejected during
// ParseExportTable.
func (t *ParsedTable) Enumerate(module uint64, limit ...Limits) ([]RawExport, error) {
	if t == nil || t.Root == nil {
		return nil, invalidf("missing root node")
	}
	exports, err := enumerateExports(t.Root, module, firstLimits(limit))
	if err != nil {
		return nil, err
	}
	return exports, nil
}

// Target is the final direct address selected by an export.
type Target struct {
	Module  uint64
	Address uint64
}

// Export is one fully bound export in the loaded module.
type Export struct {
	Name    string
	Module  uint64
	Address uint64
}

// Target returns the export's resolved target.
func (e Export) Target() Target {
	return Target{Module: e.Module, Address: e.Address}
}

// Exports is the published export set, keyed by the export's full name.
type Exports map[string]Export

// Lookup returns the named export.
func (e Exports) Lookup(name string) (Export, bool) {
	export, ok := e[name]
	return export, ok
}

// Names returns all export names in lexical order.
func (e Exports) Names() []string {
	names := make([]string, 0, len(e))
	for name := range e {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Targets returns a copy of the resolved name-to-target map.
func (e Exports) Targets() map[string]Target {
	targets := make(map[string]Target, len(e))
	for name, export := range e {
		targets[name] = export.Target()
	}
	return targets
}

func invalidf(format string, args ...any) error {
	return wrapf(ErrInvalidTable, format, args...)
}

func wrapf(sentinel error, format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{sentinel}, args...)...)
}

func firstLimits(limits []Limits) Limits {
	if len(limits) == 0 {
		return Limits{}
	}
	return limits[0]
}
