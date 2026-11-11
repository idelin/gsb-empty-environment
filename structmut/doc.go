// Package structmut performs structure-aware mutation of binary samples for
// use with the Go fuzzing framework.
//
// A blob is described by a Schema: an ordered list of Sections. Each section
// has a byte-string body, an alignment requirement, optional explicit layout
// dependencies (Section.After) and a set of integer Fields embedded at fixed
// offsets inside its body. Fields play one of three structural roles:
//
//   - KindLength: stores the byte length of another section,
//   - KindAbsOffset: stores the absolute blob offset of another section,
//   - KindRelOffset: stores the signed offset of another section relative to
//     the field's own position (negative values are supported).
//
// Mutation ops (Insert, Delete, SetBytes) edit section bodies. Fields are
// never corrupted: edits that would land inside a field are rejected, and
// surviving fields are relocated inside their body. A candidate is then
// re-laid-out from scratch in topological order, alignment padding is
// reinserted and every length/offset value is recomputed.
//
// A candidate is rejected as a whole when a length or offset cannot be
// represented in its field width (ErrLengthOverflow / ErrOffsetOverflow),
// when the layout dependency graph contains a cycle (ErrLayoutCycle), or when
// the independent read-back verifier (Read) cannot reconstruct every
// reference. Cycles in the reference graph (fields pointing at each other's
// sections) are explicitly allowed. Apply operates on a deep copy, so a
// rejected candidate never modifies the original schema or sample.
//
// Typical fuzz harness:
//
//	res, err := schema.Random(rand.New(rand.NewSource(seed)), 6)
//	if err != nil {
//	    return // overflow/cycle candidate rejected, input untouched
//	}
//	report, err := structmut.Read(res.Blob, res.Schema)
package structmut
