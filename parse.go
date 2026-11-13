package netsec

import "bytes"

type intervalNode struct {
	start  uint64
	end    uint64
	height int
	left   *intervalNode
	right  *intervalNode
}

type nodeStore struct {
	data     []byte
	nodes    map[uint64]*Node
	interval *intervalNode
}

func newnodeStore(data []byte) *nodeStore {
	return &nodeStore{
		data:  data,
		nodes: make(map[uint64]*Node),
	}
}

func intervalHeight(node *intervalNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func updateIntervalHeight(node *intervalNode) {
	left := intervalHeight(node.left)
	right := intervalHeight(node.right)
	if left > right {
		node.height = left + 1
	} else {
		node.height = right + 1
	}
}

func rotateIntervalLeft(root *intervalNode) *intervalNode {
	right := root.right
	root.right = right.left
	right.left = root
	updateIntervalHeight(root)
	updateIntervalHeight(right)
	return right
}

func rotateIntervalRight(root *intervalNode) *intervalNode {
	left := root.left
	root.left = left.right
	left.right = root
	updateIntervalHeight(root)
	updateIntervalHeight(left)
	return left
}

func rebalanceIntervals(root *intervalNode) *intervalNode {
	updateIntervalHeight(root)
	balance := intervalHeight(root.left) - intervalHeight(root.right)
	if balance > 1 {
		if intervalHeight(root.left.left) < intervalHeight(root.left.right) {
			root.left = rotateIntervalLeft(root.left)
		}
		return rotateIntervalRight(root)
	}
	if balance < -1 {
		if intervalHeight(root.right.right) < intervalHeight(root.right.left) {
			root.right = rotateIntervalRight(root.right)
		}
		return rotateIntervalLeft(root)
	}
	return root
}

func insertInterval(root, inserted *intervalNode) *intervalNode {
	if root == nil {
		return inserted
	}
	if inserted.start < root.start {
		root.left = insertInterval(root.left, inserted)
	} else {
		root.right = insertInterval(root.right, inserted)
	}
	return rebalanceIntervals(root)
}

func floorInterval(root *intervalNode, offset uint64) *intervalNode {
	var found *intervalNode
	for root != nil {
		if root.start <= offset {
			found = root
			root = root.right
		} else {
			root = root.left
		}
	}
	return found
}

func lowerBoundInterval(root *intervalNode, start uint64) *intervalNode {
	var found *intervalNode
	for root != nil {
		if root.start >= start {
			found = root
			root = root.left
		} else {
			root = root.right
		}
	}
	return found
}

// containing returns the start of the already known node whose byte interval
// contains offset, if any.
func (s *nodeStore) containing(offset uint64) (uint64, bool) {
	node := floorInterval(s.interval, offset)
	if node != nil && offset < node.end {
		return node.start, true
	}
	return 0, false
}

func (s *nodeStore) insert(start, end uint64) error {
	if start >= end {
		return invalidf("node at offset %d has an empty byte interval", start)
	}

	var predecessor *intervalNode
	if start > 0 {
		predecessor = floorInterval(s.interval, start-1)
	}
	if predecessor != nil && predecessor.end > start {
		return invalidf("node at offset %d overlaps node at offset %d", start, predecessor.start)
	}
	successor := lowerBoundInterval(s.interval, start)
	if successor != nil && end > successor.start {
		return invalidf("node at offset %d overlaps node at offset %d", start, successor.start)
	}

	newNode := &intervalNode{start: start, end: end, height: 1}
	s.interval = insertInterval(s.interval, newNode)
	return nil
}

// nodeAt parses the node starting at offset. A node is cached only after its
// complete byte interval is known. Reaching the same start again therefore
// reuses exactly the same interval, while reaching any byte inside that
// interval is rejected as a partial overlap.
func (s *nodeStore) nodeAt(offset uint64) (*Node, bool, error) {
	if node := s.nodes[offset]; node != nil {
		return node, false, nil
	}
	if start, ok := s.containing(offset); ok {
		return nil, false, invalidf(
			"child offset %d points inside node at offset %d", offset, start)
	}
	if offset >= uint64(len(s.data)) {
		return nil, false, invalidf("node offset %d is outside the table", offset)
	}

	start := offset
	terminalSize, pos, err := readULEB(s.data, offset)
	if err != nil {
		return nil, false, err
	}
	if terminalSize > uint64(len(s.data))-pos {
		return nil, false, invalidf(
			"terminal area at offset %d is outside the table", start)
	}
	terminalEnd := pos + terminalSize

	var terminal *Terminal
	if terminalSize != 0 {
		terminal, err = parseTerminal(s.data, pos, terminalEnd, start)
		if err != nil {
			return nil, false, err
		}
	}

	if terminalEnd >= uint64(len(s.data)) {
		return nil, false, invalidf("node at offset %d is missing childCount", start)
	}
	childCount := s.data[terminalEnd]
	pos = terminalEnd + 1

	children := make([]Edge, 0, int(childCount))
	for i := uint16(0); i < uint16(childCount); i++ {
		label, next, err := readLabel(s.data, pos)
		if err != nil {
			return nil, false, err
		}
		target, next, err := readULEB(s.data, next)
		if err != nil {
			return nil, false, err
		}
		children = append(children, Edge{Label: label, Offset: target})
		pos = next
	}
	end := pos

	if err := s.insert(start, end); err != nil {
		return nil, false, err
	}
	node := &Node{
		Offset:   start,
		End:      end,
		Terminal: terminal,
		Children: children,
	}
	s.nodes[start] = node
	return node, true, nil
}

func parseTerminal(data []byte, start, end, nodeOffset uint64) (*Terminal, error) {
	flags := Flags(data[start])
	switch flags {
	case FlagAddress:
		address, next, err := readULEBEnd(data, start+1, end)
		if err != nil {
			return nil, err
		}
		if next != end {
			return nil, invalidf(
				"address terminal at node %d has %d trailing bytes",
				nodeOffset, end-next)
		}
		return &Terminal{Flags: FlagAddress, Address: address}, nil

	case FlagForward:
		module, next, err := readULEBEnd(data, start+1, end)
		if err != nil {
			return nil, err
		}
		if next >= end {
			return nil, invalidf(
				"forward terminal at node %d is missing its NUL-terminated name",
				nodeOffset)
		}
		index := bytes.IndexByte(data[next:end], 0)
		if index < 0 {
			return nil, invalidf(
				"forward terminal at node %d has an unterminated name",
				nodeOffset)
		}
		nameEnd := next + uint64(index)
		if nameEnd+1 != end {
			return nil, invalidf(
				"forward terminal at node %d has %d trailing bytes",
				nodeOffset, end-(nameEnd+1))
		}
		return &Terminal{
			Flags:       FlagForward,
			Module:      module,
			ForwardName: string(data[next:nameEnd]),
		}, nil

	default:
		return nil, invalidf(
			"terminal at node %d has unsupported flags 0x%x", nodeOffset, byte(flags))
	}
}

func readLabel(data []byte, offset uint64) (string, uint64, error) {
	if offset >= uint64(len(data)) {
		return "", offset, invalidf("child label at offset %d is truncated", offset)
	}
	index := bytes.IndexByte(data[offset:], 0)
	if index < 0 {
		return "", offset, invalidf("child label at offset %d is not NUL terminated", offset)
	}
	if index == 0 {
		return "", offset, invalidf("child label at offset %d is empty", offset)
	}
	label := data[offset : offset+uint64(index)]
	for _, b := range label {
		if b == 0 || b > 0x7f {
			return "", offset, invalidf(
				"child label at offset %d contains a non-ASCII byte 0x%x", offset, b)
		}
	}
	return string(label), offset + uint64(index) + 1, nil
}

func parseExportTable(data []byte) (*ParsedTable, error) {
	store := newnodeStore(data)
	root, _, err := store.nodeAt(0)
	if err != nil {
		return nil, err
	}

	queue := []*Node{root}
	for head := 0; head < len(queue); head++ {
		node := queue[head]
		for i := range node.Children {
			edge := &node.Children[i]
			child, isNew, err := store.nodeAt(edge.Offset)
			if err != nil {
				return nil, err
			}
			edge.Target = child
			if isNew {
				queue = append(queue, child)
			}
		}
	}

	if err := detectNodeCycle(root); err != nil {
		return nil, err
	}
	return &ParsedTable{Root: root, Nodes: store.nodes}, nil
}

func detectNodeCycle(root *Node) error {
	type frame struct {
		node *Node
		next int
	}
	const (
		active = 1
		done   = 2
	)
	state := map[*Node]uint8{root: active}
	stack := []frame{{node: root}}

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next >= len(top.node.Children) {
			state[top.node] = done
			stack = stack[:len(stack)-1]
			continue
		}
		child := top.node.Children[top.next].Target
		top.next++
		switch state[child] {
		case active:
			return invalidf("export node graph contains a cycle through offset %d", child.Offset)
		case done:
			continue
		default:
			state[child] = active
			stack = append(stack, frame{node: child})
		}
	}
	return nil
}

// ParseExportTable parses and validates the compressed export table rooted at
// byte zero. It also enumerates names (using module index zero as the owner)
// so duplicate full names and caller budgets are checked before the table is
// returned. Forward targets are not resolved by this function.
func ParseExportTable(data []byte, limit ...Limits) (*ParsedTable, error) {
	table, err := parseExportTable(data)
	if err != nil {
		return nil, err
	}
	exports, err := table.Enumerate(0, firstLimits(limit))
	if err != nil {
		return nil, err
	}
	table.rawExports = exports
	return table, nil
}

// ParseTable is an alias for ParseExportTable.
func ParseTable(data []byte, limit ...Limits) (*ParsedTable, error) {
	return ParseExportTable(data, limit...)
}

// ParseExportTrie is an alias for ParseExportTable.
func ParseExportTrie(data []byte, limit ...Limits) (*ParsedTable, error) {
	return ParseExportTable(data, limit...)
}

type budgetState struct {
	limits    Limits
	steps     uint64
	nameBytes uint64
	exports   uint64
}

func newBudgetState(limits Limits) *budgetState {
	return &budgetState{limits: limits.normalized()}
}

func enumerateExports(root *Node, module uint64, limits Limits) ([]RawExport, error) {
	return enumerateExportsWithBudget(root, module, newBudgetState(limits))
}

func enumerateExportsWithBudget(root *Node, module uint64, budget *budgetState) ([]RawExport, error) {
	limits := budget.limits

	type frame struct {
		node   *Node
		prefix string
		next   int
	}

	exports := make([]RawExport, 0)
	seen := make(map[string]struct{})
	budget.steps++
	if budget.steps > limits.MaxSteps {
		return nil, wrapf(
			ErrIncomplete, "expansion-step budget %d exceeded", limits.MaxSteps)
	}
	stack := []frame{{node: root}}

	appendTerminal := func(node *Node, name string) error {
		if _, exists := seen[name]; exists {
			return wrapf(ErrDuplicateExport, "%q", name)
		}
		if budget.exports >= limits.MaxExports {
			return wrapf(
				ErrIncomplete,
				"export-count budget %d exceeded at %q", limits.MaxExports, name)
		}
		nameLen := uint64(len(name))
		if nameLen > limits.MaxNameBytes-budget.nameBytes {
			return wrapf(
				ErrIncomplete,
				"name-byte budget %d exceeded at %q", limits.MaxNameBytes, name)
		}
		budget.nameBytes += nameLen
		budget.exports++
		seen[name] = struct{}{}

		raw := RawExport{
			Name:   name,
			Module: module,
			Flags:  node.Terminal.Flags,
		}
		if node.Terminal.Flags == FlagForward {
			raw.TargetModule = node.Terminal.Module
			raw.ForwardName = node.Terminal.ForwardName
		} else {
			raw.Address = node.Terminal.Address
		}
		exports = append(exports, raw)
		return nil
	}

	if root.Terminal != nil {
		if err := appendTerminal(root, ""); err != nil {
			return nil, err
		}
	}

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next >= len(top.node.Children) {
			stack = stack[:len(stack)-1]
			continue
		}

		edge := top.node.Children[top.next]
		top.next++
		budget.steps++
		if budget.steps > limits.MaxSteps {
			return nil, wrapf(
				ErrIncomplete, "expansion-step budget %d exceeded", limits.MaxSteps)
		}

		name := top.prefix + edge.Label
		if uint64(len(name)) > limits.MaxNameBytes {
			return nil, wrapf(
				ErrIncomplete,
				"name-byte budget %d exceeded by path %q", limits.MaxNameBytes, name)
		}
		if edge.Target.Terminal != nil {
			if err := appendTerminal(edge.Target, name); err != nil {
				return nil, err
			}
		}
		stack = append(stack, frame{node: edge.Target, prefix: name})
	}

	return exports, nil
}
