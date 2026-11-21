package admission

import "sort"

// This file is an intentionally independent whole-tree interpreter.
//
// Instead of the mutable node tree used by Tree.Apply, it models the
// configuration as a flat map of stored absolute paths to terminal entries
// (literals or references). Replay executes every batch operation from
// scratch over that map, performs its own permission and trust checks with
// its own prefix logic, and materializes the final state with a separate
// recursive resolver. CrossCheckApply compares its per-leaf output with the
// incremental engine so a shared implementation bug cannot mask itself.

type interpKind uint8

const (
	interpLiteral interpKind = iota
	interpRef
)

type interpEntry struct {
	kind   interpKind
	lit    Literal
	source Source
	target Path
}

type interpreter struct {
	policy *Policy
	store  map[string]interpEntry
	tables map[string]struct{}
}

func newInterpreter(policy *Policy) *interpreter {
	return &interpreter{
		policy: policy,
		store:  map[string]interpEntry{},
		tables: map[string]struct{}{},
	}
}

// seedFromTree initializes the flat store from an existing incremental
// snapshot, so replay semantics match applying a batch on top of a prior
// tree rather than always starting from empty.
func (it *interpreter) seedFromTree(root *node) {
	var walk func(n *node, stored Path)
	walk = func(n *node, stored Path) {
		if len(stored) > 0 {
			it.tables[encodePath(stored[:len(stored)-1])] = struct{}{}
		}
		switch n.kind {
		case nodeTable:
			if len(stored) > 0 {
				it.tables[encodePath(stored)] = struct{}{}
			}
			for key, child := range n.children {
				walk(child, append(clonePath(stored), key))
			}
		case nodeLiteral:
			it.store[encodePath(stored)] = interpEntry{
				kind:   interpLiteral,
				lit:    n.lit,
				source: n.source,
			}
		case nodeRef:
			it.store[encodePath(stored)] = interpEntry{
				kind:   interpRef,
				source: n.source,
				target: clonePath(n.target),
			}
		}
	}
	walk(root, nil)
	delete(it.tables, encodePath(nil))
}

// replay applies an entire batch onto the flat store.
func (it *interpreter) replay(ops []Op) (error, []interpLeaf) {
	if len(ops) > MaxOpsPerBatch {
		return newError(ErrLimitExceeded, -1, nil,
			"batch has %d operations, limit is %d", len(ops), MaxOpsPerBatch), nil
	}
	distinct := map[string]struct{}{}
	for _, op := range ops {
		distinct[encodePath(op.Path)] = struct{}{}
		if op.Kind == OpLink {
			distinct[encodePath(op.Target)] = struct{}{}
		}
	}
	if len(distinct) > MaxPathsPerBatch {
		return newError(ErrLimitExceeded, -1, nil,
			"batch touches %d distinct paths, limit is %d",
			len(distinct), MaxPathsPerBatch), nil
	}

	for i, op := range ops {
		if err := validateOp(op); err != nil {
			return withOpIndex(err, i), nil
		}
		if err := it.authorize(op, i); err != nil {
			return err, nil
		}
		if err := it.mutate(op); err != nil {
			return withOpIndex(err, i), nil
		}
	}

	leaves, err := it.materialize()
	if err != nil {
		return err, nil
	}
	if err := it.enforceTrust(leaves); err != nil {
		return err, nil
	}
	return nil, leaves
}

// authorize independently computes every stored terminal ultimately
// affected by the operation and checks each against the policy.
func (it *interpreter) authorize(op Op, opIndex int) *Error {
	paths := it.affectedPaths(op)
	for _, path := range paths {
		if !it.canWrite(op.Principal, path) {
			return newError(ErrDenied, opIndex, path,
				"interpreter: principal %q cannot write affected leaf %v",
				op.Principal, []string(path))
		}
	}
	return nil
}

// canWrite is the interpreter's own grant/denial prefix implementation.
func (it *interpreter) canWrite(principal string, path Path) bool {
	denied := false
	for _, prefix := range it.policy.Denials[principal] {
		if hasPrefix(path, prefix) {
			denied = true
			break
		}
	}
	if denied {
		return false
	}
	for _, prefix := range it.policy.Grants[principal] {
		if hasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (it *interpreter) affectedPaths(op Op) []Path {
	var paths []Path
	switch op.Kind {
	case OpSet, OpLink:
		paths = append(paths, it.terminalsUnder(op.Path)...)
		paths = append(paths, clonePath(op.Path))
	case OpReplace:
		paths = append(paths, it.terminalsUnder(op.Path)...)
		for _, term := range op.Table.terminalsInOrder() {
			paths = append(paths, joinPath(op.Path, term.path))
		}
	case OpDelete:
		paths = append(paths, it.terminalsUnder(op.Path)...)
	}
	return dedupPaths(paths)
}

// terminalsUnder scans the flat store for stored terminals at path or below
// it. An entry whose path has a terminal ancestor is impossible because
// mutation removes descendants when a terminal is written above them.
func (it *interpreter) terminalsUnder(path Path) []Path {
	var out []Path
	for key, entry := range it.store {
		_ = entry
		stored := decodePath(key)
		if hasPrefix(stored, path) {
			out = append(out, stored)
		}
	}
	sort.Slice(out, func(i, j int) bool { return comparePath(out[i], out[j]) < 0 })
	return out
}

func (it *interpreter) mutate(op Op) error {
	switch op.Kind {
	case OpSet:
		if err := it.rejectTerminalAncestor(op.Path); err != nil {
			return err
		}
		it.clearUnder(op.Path)
		it.ensureParentTables(op.Path)
		it.store[encodePath(op.Path)] = interpEntry{
			kind:   interpLiteral,
			lit:    op.Literal,
			source: op.Source,
		}
	case OpLink:
		if err := it.rejectTerminalAncestor(op.Path); err != nil {
			return err
		}
		it.clearUnder(op.Path)
		it.ensureParentTables(op.Path)
		it.store[encodePath(op.Path)] = interpEntry{
			kind:   interpRef,
			source: op.Source,
			target: clonePath(op.Target),
		}
	case OpReplace:
		if err := it.rejectTerminalAncestor(op.Path); err != nil {
			return err
		}
		it.clearUnder(op.Path)
		it.tables[encodePath(op.Path)] = struct{}{}
		it.ensureParentTables(op.Path)
		for _, term := range op.Table.terminalsInOrder() {
			full := joinPath(op.Path, term.path)
			it.store[encodePath(full)] = interpEntry{
				kind:   interpLiteral,
				lit:    term.lit,
				source: term.source,
			}
		}
	case OpDelete:
		it.clearUnder(op.Path)
		delete(it.tables, encodePath(op.Path))
	}
	return nil
}

// rejectTerminalAncestor mirrors the tree engine: writing beneath an
// existing terminal is invalid rather than an implicit subtree replacement.
func (it *interpreter) rejectTerminalAncestor(path Path) error {
	for i := 1; i < len(path); i++ {
		if _, ok := it.store[encodePath(path[:i])]; ok {
			return newError(ErrInvalidInput, -1, path,
				"interpreter: an ancestor of %v is a terminal and cannot hold children",
				[]string(path))
		}
	}
	return nil
}

// ensureParentTables records every ancestor of path as an existing table so
// references targeting table-only locations resolve correctly.
func (it *interpreter) ensureParentTables(path Path) {
	for i := 1; i < len(path); i++ {
		it.tables[encodePath(path[:i])] = struct{}{}
	}
}

// clearUnder removes every stored terminal at path or below it, matching
// the tree semantics where writing at a location replaces its subtree.
func (it *interpreter) clearUnder(path Path) {
	for key := range it.store {
		if hasPrefix(decodePath(key), path) {
			delete(it.store, key)
		}
	}
	for key := range it.tables {
		if hasPrefix(decodePath(key), path) {
			delete(it.tables, key)
		}
	}
}

// interpLeaf is the interpreter's own provenance record, structurally
// converted to EffectiveLeaf only after full materialization.
type interpLeaf struct {
	vantage  Path
	lit      Literal
	terminal Source
	hops     []ChainHop
	closure  []Source
}

// materialize expands every stored entry against the final flat store.
func (it *interpreter) materialize() ([]interpLeaf, *Error) {
	keys := make([]string, 0, len(it.store))
	for key := range it.store {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var leaves []interpLeaf
	for _, key := range keys {
		entry := it.store[key]
		stored := decodePath(key)
		switch entry.kind {
		case interpLiteral:
			leaves = append(leaves, interpLeaf{
				vantage:  stored,
				lit:      entry.lit,
				terminal: entry.source,
				closure:  []Source{entry.source},
			})
		case interpRef:
			expanded, err := it.resolve(stored, stored, stored, nil, map[string]bool{})
			if err != nil {
				return nil, err
			}
			leaves = append(leaves, expanded...)
		}
	}
	sort.Slice(leaves, func(i, j int) bool {
		return comparePath(leaves[i].vantage, leaves[j].vantage) < 0
	})
	return leaves, nil
}

// resolve follows the reference stored at storedPath. targetPath is where
// the reference points; vantage is where the result is observed. visited
// holds stored reference paths on the current chain (not vantage paths), so
// shared subgraphs remain legal.
func (it *interpreter) resolve(storedPath, targetPath, vantage Path,
	hops []ChainHop, visited map[string]bool) ([]interpLeaf, *Error) {

	key := encodePath(storedPath)
	if visited[key] {
		return nil, newError(ErrCycle, -1, vantage,
			"interpreter: reference cycle through stored path %v",
			[]string(storedPath))
	}
	entry, ok := it.store[key]
	if !ok {
		return nil, newError(ErrDangling, -1, vantage,
			"interpreter: reference at %v dangles", []string(vantage))
	}
	if entry.kind != interpRef {
		return nil, newError(ErrInvalidInput, -1, vantage,
			"interpreter: expected reference at %v", []string(vantage))
	}

	nextVisited := map[string]bool{}
	for k := range visited {
		nextVisited[k] = true
	}
	nextVisited[key] = true
	nextHops := append(append([]ChainHop(nil), hops...), ChainHop{
		At:     clonePath(vantage),
		Source: entry.source,
	})

	return it.expandTarget(entry.target, vantage, nextHops, nextVisited)
}

// expandTarget materializes the node at targetPath as observed at vantage.
// It mirrors the incremental engine's stored/vantage distinction.
func (it *interpreter) expandTarget(targetPath, vantage Path, hops []ChainHop,
	visited map[string]bool) ([]interpLeaf, *Error) {

	if exact, ok := it.store[encodePath(targetPath)]; ok {
		switch exact.kind {
		case interpLiteral:
			return []interpLeaf{{
				vantage:  clonePath(vantage),
				lit:      exact.lit,
				terminal: exact.source,
				hops:     cloneHops(hops),
				closure:  closureSources(hops, exact.source),
			}}, nil
		case interpRef:
			return it.resolve(targetPath, targetPath, vantage, hops, visited)
		}
	}

	// Table target: find every stored terminal strictly under targetPath.
	var tableKeys []string
	for key := range it.store {
		stored := decodePath(key)
		if len(stored) > len(targetPath) && hasPrefix(stored, targetPath) {
			tableKeys = append(tableKeys, key)
		}
	}
	if len(tableKeys) == 0 {
		if _, isTable := it.tables[encodePath(targetPath)]; isTable {
			return nil, nil
		}
		return nil, newError(ErrDangling, -1, vantage,
			"interpreter: reference target %v does not exist",
			[]string(targetPath))
	}
	sort.Strings(tableKeys)

	var leaves []interpLeaf
	for _, key := range tableKeys {
		entry := it.store[key]
		stored := decodePath(key)
		suffix := stored[len(targetPath):]
		childVantage := joinPath(vantage, suffix)
		switch entry.kind {
		case interpLiteral:
			leaves = append(leaves, interpLeaf{
				vantage:  childVantage,
				lit:      entry.lit,
				terminal: entry.source,
				hops:     cloneHops(hops),
				closure:  closureSources(hops, entry.source),
			})
		case interpRef:
			expanded, err := it.resolve(stored, stored, childVantage, hops, visited)
			if err != nil {
				return nil, err
			}
			leaves = append(leaves, expanded...)
		}
	}
	return leaves, nil
}

// enforceTrust applies the longest-prefix trust rules independently.
func (it *interpreter) enforceTrust(leaves []interpLeaf) *Error {
	for _, leaf := range leaves {
		allowed := it.trustedPrincipals(leaf.vantage)
		if allowed == nil {
			continue
		}
		ok := map[string]bool{}
		for _, name := range allowed {
			ok[name] = true
		}
		for _, src := range leaf.closure {
			if !ok[src.Principal] {
				return newError(ErrUntrusted, -1, leaf.vantage,
					"interpreter: protected leaf %v has untrusted source %q",
					[]string(leaf.vantage), src.Principal)
			}
		}
	}
	return nil
}

func (it *interpreter) trustedPrincipals(vantage Path) []string {
	var best []string
	bestLen := -1
	for _, rule := range it.policy.Trusted {
		if hasPrefix(vantage, rule.Path) && len(rule.Path) > bestLen {
			best = rule.Principals
			bestLen = len(rule.Path)
		}
	}
	return best
}

func toEffectiveLeaves(in []interpLeaf) []EffectiveLeaf {
	out := make([]EffectiveLeaf, 0, len(in))
	for _, leaf := range in {
		out = append(out, EffectiveLeaf{
			Path:     clonePath(leaf.vantage),
			Value:    leaf.lit,
			Terminal: leaf.terminal,
			Chain:    cloneHops(leaf.hops),
			Sources:  append([]Source(nil), leaf.closure...),
		})
	}
	return out
}

// hasPrefix reports whether full starts with prefix; equal paths match.
func hasPrefix(full, prefix Path) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if full[i] != prefix[i] {
			return false
		}
	}
	return true
}

func dedupPaths(paths []Path) []Path {
	seen := map[string]bool{}
	out := make([]Path, 0, len(paths))
	for _, path := range paths {
		key := encodePath(path)
		if !seen[key] {
			seen[key] = true
			out = append(out, path)
		}
	}
	return out
}

// decodePath reverses encodePath.
func decodePath(key string) Path {
	data := []byte(key)
	var out Path
	for len(data) > 0 {
		n := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
		data = data[4:]
		out = append(out, string(data[:n]))
		data = data[n:]
	}
	return out
}
