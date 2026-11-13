// Package netsec parses and binds the restricted compressed export-table
// format described in README.md.
//
// Parsing and binding are separate phases. ParseExportTable validates node
// intervals, child offsets, terminal encodings, cycles, duplicate names, and
// work budgets without resolving forwards. Load (or Parse) then resolves every
// forward against a host module table before publishing any export. A failed
// or budget-limited load returns a nil export set.
package netsec
