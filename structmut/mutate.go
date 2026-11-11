package structmut

import (
	"fmt"
	"math/rand"
)

// Op is a structure-aware body mutation. The interface is sealed: only the
// package constructors Insert, Delete and SetBytes produce valid ops.
type Op interface {
	apply(sch *Schema) error
	sealedOp()
}

// Result is an accepted candidate: the serialized Blob plus the relocated
// Schema that produced it. The schema is a deep copy and never aliases the
// input schema even when Apply receives no ops.
type Result struct {
	Blob   []byte
	Schema *Schema
}

// InsertOp inserts Data into the named section at Offset, before the byte
// currently at Offset. Inserting inside a field's byte range is rejected.
type InsertOp struct {
	Section SectionID
	Offset  int
	Data    []byte
}

func (InsertOp) sealedOp() {}

func (op InsertOp) apply(sch *Schema) error {
	sec := sch.find(op.Section)
	if sec == nil {
		return ErrUnknownSection
	}
	if op.Offset < 0 || op.Offset > len(sec.Body) {
		return ErrInvalidOffset
	}
	for _, f := range sec.Fields {
		if op.Offset > f.Offset && op.Offset < f.Offset+int(f.Width) {
			return ErrFieldOverlap
		}
	}
	grown := make([]byte, 0, len(sec.Body)+len(op.Data))
	grown = append(grown, sec.Body[:op.Offset]...)
	grown = append(grown, op.Data...)
	grown = append(grown, sec.Body[op.Offset:]...)
	sec.Body = grown
	for i := range sec.Fields {
		if sec.Fields[i].Offset >= op.Offset {
			sec.Fields[i].Offset += len(op.Data)
		}
	}
	return nil
}

// DeleteOp removes Length bytes starting at Offset from the named section.
// Deleting any byte covered by a field is rejected; Length zero is a no-op.
type DeleteOp struct {
	Section SectionID
	Offset  int
	Length  int
}

func (DeleteOp) sealedOp() {}

func (op DeleteOp) apply(sch *Schema) error {
	sec := sch.find(op.Section)
	if sec == nil {
		return ErrUnknownSection
	}
	if op.Offset < 0 || op.Length < 0 || op.Offset+op.Length > len(sec.Body) {
		return ErrInvalidOffset
	}
	end := op.Offset + op.Length
	for _, f := range sec.Fields {
		if op.Offset < f.Offset+int(f.Width) && f.Offset < end {
			return ErrFieldOverlap
		}
	}
	shrunk := make([]byte, 0, len(sec.Body)-op.Length)
	shrunk = append(shrunk, sec.Body[:op.Offset]...)
	shrunk = append(shrunk, sec.Body[end:]...)
	sec.Body = shrunk
	for i := range sec.Fields {
		if sec.Fields[i].Offset >= end {
			sec.Fields[i].Offset -= op.Length
		}
	}
	return nil
}

// SetBytesOp replaces the whole body of a field-less section. Sections that
// carry fields cannot be wholesale replaced because field offsets would lose
// their meaning; use Insert/Delete on them instead.
type SetBytesOp struct {
	Section SectionID
	Data    []byte
}

func (SetBytesOp) sealedOp() {}

func (op SetBytesOp) apply(sch *Schema) error {
	sec := sch.find(op.Section)
	if sec == nil {
		return ErrUnknownSection
	}
	if len(sec.Fields) > 0 {
		return ErrFieldOverlap
	}
	sec.Body = append([]byte(nil), op.Data...)
	return nil
}

// Insert constructs an InsertOp.
func Insert(section SectionID, offset int, data []byte) Op {
	return InsertOp{Section: section, Offset: offset, Data: append([]byte(nil), data...)}
}

// Delete constructs a DeleteOp.
func Delete(section SectionID, offset, length int) Op {
	return DeleteOp{Section: section, Offset: offset, Length: length}
}

// SetBytes constructs a SetBytesOp.
func SetBytes(section SectionID, data []byte) Op {
	return SetBytesOp{Section: section, Data: append([]byte(nil), data...)}
}

// Apply validates the schema, deep-copies it, applies every op in order on
// the copy, re-lays-out the candidate and runs the independent read-back
// verifier. The receiver is never modified: on any error the original schema
// and sample stay byte-identical, and no Result is returned.
func (sch *Schema) Apply(ops ...Op) (*Result, error) {
	if err := sch.validate(); err != nil {
		return nil, err
	}
	candidate := sch.clone()
	for _, op := range ops {
		if op == nil {
			return nil, ErrNilOp
		}
		if err := op.apply(candidate); err != nil {
			return nil, err
		}
	}
	if err := candidate.validate(); err != nil {
		return nil, err
	}
	blob, relocated, err := candidate.serialize()
	if err != nil {
		return nil, err
	}
	if _, err := Read(blob, relocated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVerification, err)
	}
	return &Result{Blob: blob, Schema: relocated}, nil
}

// Materialize serializes the schema unchanged. It is the way to obtain the
// initial valid blob for a seed corpus.
func (sch *Schema) Materialize() (*Result, error) {
	return sch.Apply()
}

// Random builds and applies a batch of at most maxOps random mutations,
// retrying with fresh batches when a candidate is rejected. The receiver is
// never modified. It is the intended entry point inside a fuzz.F function.
func (sch *Schema) Random(rng *rand.Rand, maxOps int) (*Result, error) {
	if err := sch.validate(); err != nil {
		return nil, err
	}
	if maxOps <= 0 {
		maxOps = 1
	}
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		ops, err := sch.randomOps(rng, 1+rng.Intn(maxOps))
		if err != nil {
			lastErr = err
			continue
		}
		res, err := sch.Apply(ops...)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (sch *Schema) randomOps(rng *rand.Rand, count int) ([]Op, error) {
	ops := make([]Op, 0, count)
	for i := 0; i < count; i++ {
		sec := sch.Sections[rng.Intn(len(sch.Sections))]
		switch {
		case len(sec.Fields) == 0 && rng.Intn(3) == 0:
			data := make([]byte, rng.Intn(5))
			rng.Read(data)
			ops = append(ops, SetBytes(sec.ID, data))
		case rng.Intn(2) == 0:
			op, ok := randomInsert(rng, sec)
			if !ok {
				continue
			}
			ops = append(ops, op)
		default:
			op, ok := randomDelete(rng, sec)
			if !ok {
				continue
			}
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		return nil, ErrInvalidOffset
	}
	return ops, nil
}

// freeGaps returns the body byte ranges not covered by fields, in order.
func freeGaps(sec *Section) [][2]int {
	gaps := make([][2]int, 0, len(sec.Fields)+1)
	cursor := 0
	for _, f := range sec.Fields {
		if f.Offset > cursor {
			gaps = append(gaps, [2]int{cursor, f.Offset})
		}
		cursor = f.Offset + int(f.Width)
	}
	if cursor < len(sec.Body) {
		gaps = append(gaps, [2]int{cursor, len(sec.Body)})
	}
	return gaps
}

func randomInsert(rng *rand.Rand, sec *Section) (Op, bool) {
	for tries := 0; tries < 8; tries++ {
		p := rng.Intn(len(sec.Body) + 1)
		inside := false
		for _, f := range sec.Fields {
			if p > f.Offset && p < f.Offset+int(f.Width) {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		data := make([]byte, 1+rng.Intn(4))
		rng.Read(data)
		return Insert(sec.ID, p, data), true
	}
	return nil, false
}

func randomDelete(rng *rand.Rand, sec *Section) (Op, bool) {
	gaps := freeGaps(sec)
	if len(gaps) == 0 {
		return nil, false
	}
	gap := gaps[rng.Intn(len(gaps))]
	gapLen := gap[1] - gap[0]
	length := 1 + rng.Intn(gapLen)
	return Delete(sec.ID, gap[0], length), true
}
