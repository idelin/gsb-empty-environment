package pdfscan

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
)

// classicBuilder assembles a single-revision PDF using keyword xref tables.
// Bodies are provided as already-formatted object content (without the
// "N G obj" wrapper); refs(n) emits a reference to object n.
type classicBuilder struct {
	rootNum int
	bodies  map[int]string
}

func newClassicBuilder(rootNum int) *classicBuilder {
	return &classicBuilder{rootNum: rootNum, bodies: map[int]string{}}
}

func refs(n int) string { return fmt.Sprintf("%d 0 R", n) }

func (b *classicBuilder) set(num int, body string) {
	b.bodies[num] = body
}

func (b *classicBuilder) build() []byte {
	nums := make([]int, 0, len(b.bodies))
	for n := range b.bodies {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := map[int]int{}
	for _, n := range nums {
		offsets[n] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, b.bodies[n])
	}

	xrefPos := out.Len()
	maxNum := nums[len(nums)-1]
	fmt.Fprintf(&out, "xref\n0 %d\n", maxNum+1)
	fmt.Fprintf(&out, "0000000000 65535 f \n")
	for n := 1; n <= maxNum; n++ {
		off, ok := offsets[n]
		if !ok {
			fmt.Fprintf(&out, "0000000000 65535 f \n")
			continue
		}
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %s >>\nstartxref\n%d\n%%%%EOF\n",
		maxNum+1, refs(b.rootNum), xrefPos)
	return out.Bytes()
}

// appendIncrement appends new/overwritten objects and a fresh xref table.
// Objects present in newBodies supersede earlier definitions because the
// new xref entry overrides the previous revision during the Prev merge.
func appendIncrement(base []byte, rootNum, size int, newBodies map[int]string) []byte {
	nums := make([]int, 0, len(newBodies))
	for n := range newBodies {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	var out bytes.Buffer
	out.Write(base)
	offsets := map[int]int{}
	for _, n := range nums {
		offsets[n] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, newBodies[n])
	}

	xrefPos := out.Len()
	for _, n := range nums {
		fmt.Fprintf(&out, "xref\n%d 1\n%010d 00000 n \n", n, offsets[n])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %s /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
		size, refs(rootNum), findStartXrefOf(base), xrefPos)
	return out.Bytes()
}

func findStartXrefOf(data []byte) int {
	off, _ := lastStartXref(data)
	return off
}

func flateCompress(data []byte) []byte {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	zw.Write(data)
	zw.Close()
	return out.Bytes()
}

// buildCompressedPDF packs catalog and a malicious action into an object
// stream and references them through a cross-reference stream (type 2
// entries), exercising both Flate paths at once.
func buildCompressedPDF() []byte {
	// Object 10: ObjStm containing catalog (inner index 0) and JS action
	// (inner index 1).
	innerCatalog := "<< /Type /Catalog /OpenAction 21 0 R >>"
	innerAction := "<< /S /JavaScript /JS (app.alert(1)) >>"

	var inner bytes.Buffer
	offCatalog := 0
	// Header layout per ISO 32000: all object numbers, then all offsets
	// (offsets are relative to First). The header length depends on the
	// digit count of offAction, which in turn depends on the header length;
	// resolve that fixed point by iteration.
	first := len(fmt.Sprintf("%d %d %d %d ", 20, 21, offCatalog, 0))
	var offAction int
	for {
		offAction = len(innerCatalog) + 1
		next := len(fmt.Sprintf("%d %d %d %d ", 20, 21, offCatalog, offAction))
		if next == first {
			break
		}
		first = next
	}
	inner.WriteString(fmt.Sprintf("%d %d %d %d ", 20, 21, offCatalog, offAction))
	if inner.Len() != first {
		panic("object stream header length mismatch")
	}
	inner.WriteString(innerCatalog)
	inner.WriteString(" ")
	inner.WriteString(innerAction)
	if first+offAction != inner.Len()-len(innerAction) {
		panic("object stream action offset out of range")
	}

	compressed := flateCompress(inner.Bytes())
	objStm := fmt.Sprintf("<< /Type /ObjStm /N 2 /First %d /Length %d /Filter /FlateDecode >>\nstream\n",
		first, len(compressed))

	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	objStmPos := out.Len()
	fmt.Fprintf(&out, "10 0 obj\n%s", objStm)
	out.Write(compressed)
	out.WriteString("\nendstream\nendobj\n")

	// Cross-reference stream (object 1) with type 1 entry for the ObjStm
	// and type 2 entries for the two compressed objects.
	rows := [][]int{
		{0, 0, 0},         // free head
		{1, 0, 0},         // object 1 (xref stream itself); resolved via startxref
		{1, objStmPos, 0}, // object 10 -> ObjStm byte offset
		{2, 10, 0},        // object 20 -> ObjStm 10 index 0
		{2, 10, 1},        // object 21 -> ObjStm 10 index 1
	}
	var xrefBody bytes.Buffer
	for _, r := range rows {
		xrefBody.WriteByte(byte(r[0]))
		xrefBody.Write([]byte{
			byte(r[1] >> 32), byte(r[1] >> 24), byte(r[1] >> 16), byte(r[1] >> 8), byte(r[1]),
			byte(r[2] >> 8), byte(r[2]),
		})
	}
	xrefCompressed := flateCompress(xrefBody.Bytes())
	xrefPos := out.Len()
	fmt.Fprintf(&out, "1 0 obj\n<< /Type /XRef /Size 22 /W [1 5 2] /Index [0 1 1 1 10 1 20 2] /Root 20 0 R /Length %d /Filter /FlateDecode >>\nstream\n",
		len(xrefCompressed))
	out.Write(xrefCompressed)
	out.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", xrefPos)
	return out.Bytes()
}
