package netsec

import (
	"errors"
	"math"
	"testing"
)

type q0048Child struct {
	label  string
	target *q0048Node
	// delta shifts the target offset. Tests use it to point into a node.
	delta uint64
}

type q0048Node struct {
	terminal []byte
	children []q0048Child
}

func q0048AddrTerminal(address uint64) []byte {
	terminal := []byte{byte(FlagAddress)}
	return appendULEB(terminal, address)
}

func q0048ForwardTerminal(module uint64, name string) []byte {
	terminal := []byte{byte(FlagForward)}
	terminal = appendULEB(terminal, module)
	terminal = append(terminal, name...)
	return append(terminal, 0)
}

func q0048NodeSize(node *q0048Node, offsets map[*q0048Node]uint64) uint64 {
	size := uint64(len(appendULEB(nil, uint64(len(node.terminal)))))
	size += uint64(len(node.terminal))
	size++ // childCount
	for _, child := range node.children {
		size += uint64(len(child.label)) + 1
		size += uint64(len(appendULEB(nil, offsets[child.target]+child.delta)))
	}
	return size
}

// q0048EncodeGraph lays nodes out in the supplied order. The first node is the
// root. Offsets are iterated to a fixed point because changing an absolute
// offset can change the width of its ULEB128 encoding.
func q0048EncodeGraph(nodes []*q0048Node) []byte {
	if len(nodes) == 0 {
		return nil
	}
	offsets := make(map[*q0048Node]uint64, len(nodes))
	for _, node := range nodes {
		offsets[node] = 0
	}

	var total uint64
	for iteration := 0; iteration < 64; iteration++ {
		next := make(map[*q0048Node]uint64, len(nodes))
		var offset uint64
		for _, node := range nodes {
			next[node] = offset
			offset += q0048NodeSize(node, offsets)
		}
		total = offset

		same := true
		for _, node := range nodes {
			if next[node] != offsets[node] {
				same = false
				break
			}
		}
		offsets = next
		if same {
			break
		}
	}

	table := make([]byte, int(total))
	for _, node := range nodes {
		pos := offsets[node]
		encoded := appendULEB(nil, uint64(len(node.terminal)))
		copy(table[pos:], encoded)
		pos += uint64(len(encoded))

		copy(table[pos:], node.terminal)
		pos += uint64(len(node.terminal))

		table[pos] = byte(len(node.children))
		pos++
		for _, child := range node.children {
			copy(table[pos:], child.label)
			pos += uint64(len(child.label))
			table[pos] = 0
			pos++

			encoded = appendULEB(nil, offsets[child.target]+child.delta)
			copy(table[pos:], encoded)
			pos += uint64(len(encoded))
		}
	}
	return table
}

func TestDiamondSharingEnumeratesEveryPrefix(t *testing.T) {
	shared := &q0048Node{terminal: q0048AddrTerminal(0x100)}
	leaf := &q0048Node{terminal: q0048AddrTerminal(0x101)}
	shared.children = []q0048Child{{label: "c", target: leaf}}
	root := &q0048Node{children: []q0048Child{
		{label: "a", target: shared},
		{label: "b", target: shared},
	}}

	table := q0048EncodeGraph([]*q0048Node{root, shared, leaf})
	parsed, err := ParseExportTable(table, Limits{})
	if err != nil {
		t.Fatalf("ParseExportTable: %v", err)
	}
	if got, want := len(parsed.Nodes), 3; got != want {
		t.Fatalf("node count = %d, want %d", got, want)
	}
	if parsed.Root.Children[0].Target != parsed.Root.Children[1].Target {
		t.Fatal("shared child did not resolve to the same node")
	}
	if parsed.Root.Children[0].Target.End != parsed.Root.Children[1].Target.End {
		t.Fatal("shared node has different boundaries")
	}

	exports, err := LoadTableWithRanges(
		0, table, []Range{{Start: 0x100, End: 0x200}}, nil, Limits{})
	if err != nil {
		t.Fatalf("LoadTableWithRanges: %v", err)
	}
	want := map[string]uint64{
		"a":  0x100,
		"b":  0x100,
		"ac": 0x101,
		"bc": 0x101,
	}
	if len(exports) != len(want) {
		t.Fatalf("exports = %v, want %d entries", exports, len(want))
	}
	for name, address := range want {
		export, ok := exports.Lookup(name)
		if !ok {
			t.Fatalf("missing export %q in %v", name, exports)
		}
		if export.Module != 0 || export.Address != address {
			t.Fatalf("export %q = %+v, want module 0 address %#x", name, export, address)
		}
	}
}

func TestChildOffsetIntoParentIsRejected(t *testing.T) {
	// Root: terminalSize=0, childCount=1, "x\0", target offset 1.
	table := []byte{0, 1, 'x', 0, 1}
	_, err := ParseExportTable(table, Limits{})
	if !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("error = %v, want ErrInvalidTable", err)
	}
}

func TestSiblingIntervalsCannotPartiallyOverlap(t *testing.T) {
	inside := &q0048Node{} // terminalSize=0, childCount=0: exactly two bytes
	root := &q0048Node{children: []q0048Child{
		{label: "a", target: inside},
		{label: "b", target: inside, delta: 1},
	}}
	table := q0048EncodeGraph([]*q0048Node{root, inside})

	_, err := ParseExportTable(table, Limits{})
	if !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("error = %v, want ErrInvalidTable", err)
	}
}

func TestIntervalIndexUsesHalfOpenBounds(t *testing.T) {
	store := newnodeStore(nil)
	if err := store.insert(10, 20); err != nil {
		t.Fatalf("insert [10,20): %v", err)
	}
	if err := store.insert(5, 15); !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("overlap error = %v, want ErrInvalidTable", err)
	}

	if start, ok := store.containing(10); !ok || start != 10 {
		t.Fatalf("containing(10) = %d, %v", start, ok)
	}
	if start, ok := store.containing(19); !ok || start != 10 {
		t.Fatalf("containing(19) = %d, %v", start, ok)
	}
	if start, ok := store.containing(20); ok {
		t.Fatalf("containing(20) = %d, want false at half-open end", start)
	}

	if err := store.insert(20, 30); err != nil {
		t.Fatalf("insert adjacent [20,30): %v", err)
	}
	if err := store.insert(0, 10); err != nil {
		t.Fatalf("insert adjacent [0,10): %v", err)
	}
}

func TestGraphCycleIsRejected(t *testing.T) {
	root := &q0048Node{}
	child := &q0048Node{}
	root.children = []q0048Child{{label: "a", target: child}}
	child.children = []q0048Child{{label: "b", target: root}}

	table := q0048EncodeGraph([]*q0048Node{root, child})
	_, err := ParseExportTable(table, Limits{})
	if !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("error = %v, want ErrInvalidTable", err)
	}
}

func TestNodesCanBeParsedInDescendingOffsetOrder(t *testing.T) {
	high := &q0048Node{terminal: q0048AddrTerminal(0x20)}
	low := &q0048Node{terminal: q0048AddrTerminal(0x10)}
	// Layout places low first but reaches high first, forcing the interval
	// index to insert in descending byte order.
	root := &q0048Node{children: []q0048Child{
		{label: "high", target: high},
		{label: "low", target: low},
	}}
	table := q0048EncodeGraph([]*q0048Node{root, low, high})

	exports, err := LoadTableWithRanges(
		0, table, []Range{{Start: 0, End: 0x100}}, nil, Limits{})
	if err != nil {
		t.Fatalf("LoadTableWithRanges: %v", err)
	}
	if exports["high"].Address != 0x20 || exports["low"].Address != 0x10 {
		t.Fatalf("exports = %v", exports)
	}
}

func TestDuplicateFullNameWithDifferentTargetsIsRejected(t *testing.T) {
	first := &q0048Node{terminal: q0048AddrTerminal(0x10)}
	second := &q0048Node{terminal: q0048AddrTerminal(0x20)}
	root := &q0048Node{children: []q0048Child{
		{label: "same", target: first},
		{label: "same", target: second},
	}}
	table := q0048EncodeGraph([]*q0048Node{root, first, second})

	exports, err := LoadTableWithRanges(
		0, table, []Range{{Start: 0, End: 0x100}}, nil, Limits{})
	if !errors.Is(err, ErrDuplicateExport) {
		t.Fatalf("error = %v, want ErrDuplicateExport", err)
	}
	if exports != nil {
		t.Fatalf("partial exports published: %v", exports)
	}
}

func TestBudgetExhaustionReturnsNoPartialSet(t *testing.T) {
	shared := &q0048Node{terminal: q0048AddrTerminal(0x100)}
	leaf := &q0048Node{terminal: q0048AddrTerminal(0x101)}
	shared.children = []q0048Child{{label: "c", target: leaf}}
	root := &q0048Node{children: []q0048Child{
		{label: "a", target: shared},
		{label: "b", target: shared},
	}}
	table := q0048EncodeGraph([]*q0048Node{root, shared, leaf})
	ranges := []Range{{Start: 0x100, End: 0x200}}

	cases := []struct {
		name   string
		limits Limits
	}{
		{name: "steps", limits: Limits{MaxSteps: 2}},
		{name: "name bytes", limits: Limits{MaxNameBytes: 3}},
		{name: "exports", limits: Limits{MaxExports: 3}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			exports, err := LoadTableWithRanges(0, table, ranges, nil, test.limits)
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("error = %v, want ErrIncomplete", err)
			}
			if exports != nil {
				t.Fatalf("partial exports published: %v", exports)
			}
		})
	}
}

func TestULEB128CanonicalAndBounds(t *testing.T) {
	maxEncoding := appendULEB(nil, math.MaxUint64)
	value, next, err := readULEB(maxEncoding, 0)
	if err != nil {
		t.Fatalf("read max ULEB128: %v", err)
	}
	if value != math.MaxUint64 || next != uint64(len(maxEncoding)) {
		t.Fatalf("read max ULEB128 = %#x, %d", value, next)
	}

	invalid := [][]byte{
		{0x80, 0x00},
		{0x80},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02},
	}
	for _, data := range invalid {
		if _, _, err := readULEB(data, 0); !errors.Is(err, ErrInvalidTable) {
			t.Errorf("readULEB(%x) error = %v, want ErrInvalidTable", data, err)
		}
	}
}

func TestTerminalValidation(t *testing.T) {
	badTables := [][]byte{
		// Terminal size 1, unsupported flags, childCount 0.
		{1, 1, 0},
		// Address flag with missing address.
		{1, 0, 0},
		// Address flag with a trailing byte in the terminal area.
		{3, 0, 1, 2, 0},
		// Forward flag with missing module/name.
		{1, 8, 0},
	}
	for _, table := range badTables {
		if _, err := ParseExportTable(table, Limits{}); !errors.Is(err, ErrInvalidTable) {
			t.Errorf("ParseExportTable(%x) error = %v, want ErrInvalidTable", table, err)
		}
	}
}

func FuzzQ0048ParseExportTable(f *testing.F) {
	shared := &q0048Node{terminal: q0048AddrTerminal(0x100)}
	leaf := &q0048Node{terminal: q0048AddrTerminal(0x101)}
	shared.children = []q0048Child{{label: "c", target: leaf}}
	root := &q0048Node{children: []q0048Child{
		{label: "a", target: shared},
		{label: "b", target: shared},
	}}
	f.Add(q0048EncodeGraph([]*q0048Node{root, shared, leaf}))
	f.Add([]byte{0, 1, 'x', 0, 1})
	f.Add([]byte{1, 0, 1, 0})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseExportTable(data, Limits{
			MaxSteps:     512,
			MaxNameBytes: 4096,
			MaxExports:   256,
		})
	})
}
