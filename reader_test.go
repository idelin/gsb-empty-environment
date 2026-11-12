package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"testing"
)

// Test table format (dir type): [size uint16][nrefs uint16][refs...],
// each ref is 14 bytes: version uint32, offset uint64, type uint16.
// Leaf type: [size uint16], no references.

const (
	typeDir  TableType = 1
	typeLeaf TableType = 2
)

const refLen = 14

func dirSize(nrefs, pad int) int { return 4 + refLen*nrefs + pad }

type tableSpec struct {
	off  int
	typ  TableType
	refs []TableID
	pad  int
}

func buildImage(version uint32, specs ...tableSpec) []byte {
	var size int
	for _, s := range specs {
		end := s.off + dirSize(len(s.refs), s.pad)
		if end > size {
			size = end
		}
	}
	img := make([]byte, size)
	for _, s := range specs {
		binary.LittleEndian.PutUint16(img[s.off:], uint16(dirSize(len(s.refs), s.pad)))
		binary.LittleEndian.PutUint16(img[s.off+2:], uint16(len(s.refs)))
		p := s.off + 4
		for _, ref := range s.refs {
			binary.LittleEndian.PutUint32(img[p:], ref.Version)
			binary.LittleEndian.PutUint64(img[p+4:], ref.Offset)
			binary.LittleEndian.PutUint16(img[p+12:], uint16(ref.Type))
			p += refLen
		}
	}
	return img
}

func parseTestTable(id TableID, r io.ReaderAt) (Descriptor, error) {
	var hdr [4]byte
	if _, err := r.ReadAt(hdr[:], int64(id.Offset)); err != nil {
		return Descriptor{}, err
	}
	size := binary.LittleEndian.Uint16(hdr[0:2])
	if id.Type == typeLeaf {
		return Descriptor{Size: uint64(size)}, nil
	}
	n := int(binary.LittleEndian.Uint16(hdr[2:4]))
	desc := Descriptor{Size: uint64(size)}
	if n == 0 {
		return desc, nil
	}
	buf := make([]byte, refLen*n)
	if _, err := r.ReadAt(buf, int64(id.Offset)+4); err != nil {
		return Descriptor{}, err
	}
	for i := 0; i < n; i++ {
		b := buf[i*refLen:]
		desc.Refs = append(desc.Refs, TableID{
			Version: binary.LittleEndian.Uint32(b[0:4]),
			Offset:  binary.LittleEndian.Uint64(b[4:12]),
			Type:    TableType(binary.LittleEndian.Uint16(b[12:14])),
		})
	}
	return desc, nil
}

type countParser struct {
	counts map[TableID]int
}

func newCountParser() *countParser { return &countParser{counts: make(map[TableID]int)} }

func (c *countParser) parse(id TableID, r io.ReaderAt) (Descriptor, error) {
	c.counts[id]++
	return parseTestTable(id, r)
}

type memSource struct{ *bytes.Reader }

// shortSource reports a declared size larger than its real data.
type shortSource struct {
	data     []byte
	declared int64
}

func (s shortSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(p, off)
}

func (s shortSource) Size() int64 { return s.declared }

const testVersion = 7

func TestSharedTableParsedOnce(t *testing.T) {
	x := TableID{testVersion, 64, typeDir}
	b := TableID{testVersion, 46, typeDir}
	root := TableID{testVersion, 0, typeDir}
	img := buildImage(testVersion,
		tableSpec{off: 0, typ: typeDir, refs: []TableID{b, x, x}},
		tableSpec{off: 46, typ: typeDir, refs: []TableID{x}},
		tableSpec{off: 64, typ: typeDir},
	)
	cp := newCountParser()
	r := NewReader(memSource{bytes.NewReader(img)}, cp.parse, Options{Version: testVersion})

	node, err := r.Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := cp.counts[x]; got != 1 {
		t.Fatalf("shared table parsed %d times, want 1", got)
	}
	if node.Children[1] != node.Children[2] {
		t.Fatal("duplicate references to one table did not resolve to the same node")
	}
	if node.Children[0].Children[0] != node.Children[1] {
		t.Fatal("nested share did not resolve to the memoized node")
	}
}

func TestCycleShortestEvidence(t *testing.T) {
	a := TableID{testVersion, 0, typeDir}
	b := TableID{testVersion, 18, typeDir}
	c := TableID{testVersion, 36, typeDir}
	img := buildImage(testVersion,
		tableSpec{off: 0, typ: typeDir, refs: []TableID{b}},
		tableSpec{off: 18, typ: typeDir, refs: []TableID{c}},
		tableSpec{off: 36, typ: typeDir, refs: []TableID{b}},
	)
	r := NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{Version: testVersion})

	_, err := r.Read(a)
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want CycleError", err)
	}
	want := []TableID{b, c, b}
	if !reflect.DeepEqual(cyc.Path, want) {
		t.Fatalf("cycle path = %v, want shortest evidence %v", cyc.Path, want)
	}
}

func TestSelfCycle(t *testing.T) {
	a := TableID{testVersion, 0, typeDir}
	img := buildImage(testVersion, tableSpec{off: 0, typ: typeDir, refs: []TableID{a}})
	r := NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{Version: testVersion})

	_, err := r.Read(a)
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want CycleError", err)
	}
	if want := []TableID{a, a}; !reflect.DeepEqual(cyc.Path, want) {
		t.Fatalf("cycle path = %v, want %v", cyc.Path, want)
	}
}

func TestSameOffsetDifferentType(t *testing.T) {
	// A dir table at offset 0 references a leaf table at the same offset.
	// Identity includes type, so this is sharing-free and cycle-free.
	leaf := TableID{testVersion, 0, typeLeaf}
	root := TableID{testVersion, 0, typeDir}
	img := buildImage(testVersion, tableSpec{off: 0, typ: typeDir, refs: []TableID{leaf}})
	cp := newCountParser()
	r := NewReader(memSource{bytes.NewReader(img)}, cp.parse, Options{Version: testVersion})

	node, err := r.Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	child := node.Children[0]
	if child.ID == node.ID {
		t.Fatal("same-offset different-type table aliased its referrer")
	}
	if child.ID.Offset != node.ID.Offset || child.ID.Type != typeLeaf {
		t.Fatalf("child identity = %v, want offset 0 type %d", child.ID, typeLeaf)
	}
	if cp.counts[root] != 1 || cp.counts[leaf] != 1 {
		t.Fatalf("parse counts = %v, want exactly one parse per identity", cp.counts)
	}
}

func TestShortRead(t *testing.T) {
	// Declared image size is 200, real data is 100: the table at offset
	// 90 passes the bounds check but its extent cannot be fully read.
	root := TableID{testVersion, 90, typeDir}
	img := buildImage(testVersion, tableSpec{off: 90, typ: typeDir})
	if len(img) != 94 {
		t.Fatalf("test image len = %d, want 94", len(img))
	}
	src := shortSource{data: img, declared: 200}
	// Grow the declared size past the table end so bounds succeeds.
	binary.LittleEndian.PutUint16(img[90:], 18)
	r := NewReader(src, parseTestTable, Options{Version: testVersion})

	_, err := r.Read(root)
	var sr *ShortReadError
	if !errors.As(err, &sr) {
		t.Fatalf("err = %v, want ShortReadError", err)
	}
	if sr.ID != root || sr.Want != 18 {
		t.Fatalf("ShortReadError = %+v, want table %v of 18 bytes", sr, root)
	}
}

func TestBoundsCheckedBeforeRefs(t *testing.T) {
	child := TableID{testVersion, 8, typeDir}
	root := TableID{testVersion, 0, typeDir}
	img := buildImage(testVersion,
		tableSpec{off: 0, typ: typeDir, refs: []TableID{child}},
		tableSpec{off: 8, typ: typeDir},
	)
	// Root declares an extent far beyond the image.
	binary.LittleEndian.PutUint16(img[0:], 500)
	cp := newCountParser()
	r := NewReader(memSource{bytes.NewReader(img)}, cp.parse, Options{Version: testVersion})

	_, err := r.Read(root)
	var be *BoundsError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BoundsError", err)
	}
	if be.ID != root {
		t.Fatalf("BoundsError table = %v, want %v", be.ID, root)
	}
	if cp.counts[child] != 0 {
		t.Fatal("reference was followed before bounds validation")
	}
}

func TestTableBudget(t *testing.T) {
	var specs []tableSpec
	ids := make([]TableID, 5)
	for i := range ids {
		ids[i] = TableID{testVersion, uint64(i * 18), typeDir}
	}
	for i := range ids {
		var refs []TableID
		if i+1 < len(ids) {
			refs = []TableID{ids[i+1]}
		}
		specs = append(specs, tableSpec{off: i * 18, typ: typeDir, refs: refs})
	}
	img := buildImage(testVersion, specs...)
	r := NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{
		Version:   testVersion,
		MaxTables: 3,
	})

	_, err := r.Read(ids[0])
	var be *BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BudgetError", err)
	}
	if be.Kind != BudgetTables || be.Limit != 3 {
		t.Fatalf("BudgetError = %+v, want tables limit 3", be)
	}
}

func TestByteBudget(t *testing.T) {
	var specs []tableSpec
	ids := make([]TableID, 4)
	for i := range ids {
		ids[i] = TableID{testVersion, uint64(i * 18), typeDir}
	}
	for i := range ids {
		var refs []TableID
		if i+1 < len(ids) {
			refs = []TableID{ids[i+1]}
		}
		specs = append(specs, tableSpec{off: i * 18, typ: typeDir, refs: refs})
	}
	img := buildImage(testVersion, specs...)
	r := NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{
		Version:  testVersion,
		MaxBytes: 36, // two 18-byte tables
	})

	_, err := r.Read(ids[0])
	var be *BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BudgetError", err)
	}
	if be.Kind != BudgetBytes || be.Limit != 36 {
		t.Fatalf("BudgetError = %+v, want bytes limit 36", be)
	}
}

func TestWhitelist(t *testing.T) {
	leaf := TableID{testVersion, 32, typeLeaf}
	other := TableID{testVersion, 36, TableType(9)}
	root := TableID{testVersion, 0, typeDir}
	img := buildImage(testVersion,
		tableSpec{off: 0, typ: typeDir, refs: []TableID{leaf, other}},
		tableSpec{off: 32, typ: typeLeaf},
		tableSpec{off: 36, typ: TableType(9)},
	)
	r := NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{
		Version:  testVersion,
		AllowRef: EdgeWhitelist([2]TableType{typeDir, typeLeaf}),
	})

	_, err := r.Read(root)
	var ne *RefNotAllowedError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want RefNotAllowedError", err)
	}
	if ne.From != root || ne.To != other {
		t.Fatalf("RefNotAllowedError = %+v, want %v -> %v", ne, root, other)
	}

	// With the edge whitelisted, the same graph resolves.
	r = NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{
		Version: testVersion,
		AllowRef: EdgeWhitelist(
			[2]TableType{typeDir, typeLeaf},
			[2]TableType{typeDir, TableType(9)},
		),
	})
	node, err := r.Read(root)
	if err != nil {
		t.Fatalf("Read with full whitelist: %v", err)
	}
	if len(node.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(node.Children))
	}
}

func TestVersionMismatch(t *testing.T) {
	root := TableID{testVersion, 0, typeDir}
	alien := TableID{testVersion + 1, 18, typeDir}
	img := buildImage(testVersion,
		tableSpec{off: 0, typ: typeDir, refs: []TableID{alien}},
		tableSpec{off: 18, typ: typeDir},
	)
	r := NewReader(memSource{bytes.NewReader(img)}, parseTestTable, Options{Version: testVersion})

	_, err := r.Read(root)
	var ve *VersionError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want VersionError", err)
	}
	if ve.ID != alien || ve.Want != testVersion {
		t.Fatalf("VersionError = %+v, want id %v want %d", ve, alien, testVersion)
	}
}
