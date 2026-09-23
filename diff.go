package gsbdiff

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrBaselineRequired: Apply was called without a baseline document.
	// We refuse rather than guess a default empty object.
	ErrBaselineRequired = errors.New("gsbdiff: baseline document required")
	// ErrBaselineMismatch: the supplied baseline does not match the
	// summary stored in the diff (length equal but content differs also
	// fails here via the hash).
	ErrBaselineMismatch = errors.New("gsbdiff: baseline summary mismatch")
	// ErrNotDocument: document root is not an object or array.
	ErrNotDocument = errors.New("gsbdiff: document root must be object or array")
	// ErrInvalidOp: an operation cannot be applied to this baseline.
	ErrInvalidOp = errors.New("gsbdiff: invalid operation")
	// ErrNilDiff: Apply was called with a nil diff.
	ErrNilDiff = errors.New("gsbdiff: nil diff")
)

// OpKind identifies the kind of a diff operation.
type OpKind byte

const (
	OpSet       OpKind = 1 // set object key / replace value at path
	OpDel       OpKind = 2 // delete object key (distinct from setting null)
	OpArrayEdit OpKind = 3 // apply a keep/delete/insert script to an array
)

// EditKind identifies one step of an array edit script.
type EditKind byte

const (
	EditKeep   EditKind = 1 // copy N elements from the old array
	EditDelete EditKind = 2 // drop N elements from the old array
	EditInsert EditKind = 3 // insert the given values
)

// Edit is one step of an array edit script.
type Edit struct {
	Kind   EditKind
	N      int   // for EditKeep / EditDelete
	Values []any // for EditInsert
}

// Seg is one path segment: an object key or an array index.
type Seg struct {
	Key     string
	Index   int
	IsIndex bool
}

// Op is a single diff operation.
type Op struct {
	Kind  OpKind
	Path  []Seg
	Value any    // for OpSet
	Edits []Edit // for OpArrayEdit
}

// Diff is a changeset bound to a baseline summary (length + SHA-256 of the
// canonical baseline encoding).
type Diff struct {
	BaseLen  uint64
	BaseHash [32]byte
	Ops      []Op
}

func summaryOf(canon []byte) (uint64, [32]byte) {
	return uint64(len(canon)), sha256.Sum256(canon)
}

// Compute builds the diff that transforms oldDoc into newDoc.
func Compute(oldDoc, newDoc any) (*Diff, error) {
	oldN, err := Normalize(oldDoc)
	if err != nil {
		return nil, err
	}
	newN, err := Normalize(newDoc)
	if err != nil {
		return nil, err
	}
	if !isDocument(oldN) || !isDocument(newN) {
		return nil, ErrNotDocument
	}
	canon, err := canonical(oldN)
	if err != nil {
		return nil, err
	}
	d := &Diff{}
	d.BaseLen, d.BaseHash = summaryOf(canon)
	d.Ops = diffValue(nil, oldN, newN)
	return d, nil
}

func appendSeg(path []Seg, key string) []Seg {
	p := make([]Seg, len(path)+1)
	copy(p, path)
	p[len(path)] = Seg{Key: key}
	return p
}

func diffValue(path []Seg, a, b any) []Op {
	if valuesEqual(a, b) {
		return nil
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		var ops []Op
		keys := make([]string, 0, len(am)+len(bm))
		for k := range am {
			keys = append(keys, k)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			av, inA := am[k]
			bv, inB := bm[k]
			switch {
			case inA && !inB:
				ops = append(ops, Op{Kind: OpDel, Path: appendSeg(path, k)})
			case !inA && inB:
				ops = append(ops, Op{Kind: OpSet, Path: appendSeg(path, k), Value: deepCopy(bv)})
			default:
				ops = append(ops, diffValue(appendSeg(path, k), av, bv)...)
			}
		}
		return ops
	}
	as, aok := a.([]any)
	bs, bok := b.([]any)
	if aok && bok {
		return []Op{{Kind: OpArrayEdit, Path: append([]Seg(nil), path...), Edits: arrayEdits(as, bs)}}
	}
	return []Op{{Kind: OpSet, Path: append([]Seg(nil), path...), Value: deepCopy(b)}}
}

// arrayEdits builds a keep/delete/insert script from a common prefix and
// suffix, replacing the differing middle.
func arrayEdits(oldS, newS []any) []Edit {
	p := 0
	for p < len(oldS) && p < len(newS) && valuesEqual(oldS[p], newS[p]) {
		p++
	}
	s := 0
	for s < len(oldS)-p && s < len(newS)-p && valuesEqual(oldS[len(oldS)-1-s], newS[len(newS)-1-s]) {
		s++
	}
	var edits []Edit
	if p > 0 {
		edits = append(edits, Edit{Kind: EditKeep, N: p})
	}
	if d := len(oldS) - p - s; d > 0 {
		edits = append(edits, Edit{Kind: EditDelete, N: d})
	}
	if ins := newS[p : len(newS)-s]; len(ins) > 0 {
		vals := make([]any, len(ins))
		for i, v := range ins {
			vals[i] = deepCopy(v)
		}
		edits = append(edits, Edit{Kind: EditInsert, Values: vals})
	}
	if s > 0 {
		edits = append(edits, Edit{Kind: EditKeep, N: s})
	}
	return edits
}

// Apply applies d to doc atomically: doc is never mutated; on any error the
// returned values are nil and the caller's baseline is untouched. On success
// it returns the new document together with the inverse diff, such that
// applying the inverse to the result restores the original document
// (identity convention: canonical encodings are byte-identical).
func Apply(doc any, d *Diff) (any, *Diff, error) {
	if d == nil {
		return nil, nil, ErrNilDiff
	}
	if doc == nil {
		return nil, nil, ErrBaselineRequired
	}
	norm, err := Normalize(doc)
	if err != nil {
		return nil, nil, err
	}
	if !isDocument(norm) {
		return nil, nil, ErrNotDocument
	}
	canon, err := canonical(norm)
	if err != nil {
		return nil, nil, err
	}
	l, h := summaryOf(canon)
	if l != d.BaseLen || h != d.BaseHash {
		return nil, nil, ErrBaselineMismatch
	}
	work := deepCopy(norm)
	var invOps []Op
	for _, op := range d.Ops {
		nw, inv, err := applyOp(work, op.Path, 0, op)
		if err != nil {
			return nil, nil, err
		}
		work = nw
		invOps = append(invOps, inv)
	}
	resCanon, err := canonical(work)
	if err != nil {
		return nil, nil, err
	}
	inv := &Diff{Ops: make([]Op, len(invOps))}
	for i, o := range invOps {
		inv.Ops[len(invOps)-1-i] = o
	}
	inv.BaseLen, inv.BaseHash = summaryOf(resCanon)
	return work, inv, nil
}

func copyPath(path []Seg) []Seg {
	return append([]Seg(nil), path...)
}

// applyOp applies op at path[at:] below node, returning the (possibly
// replaced) node and the inverse op carrying the full path.
func applyOp(node any, path []Seg, at int, op Op) (any, Op, error) {
	if at == len(path) {
		switch op.Kind {
		case OpSet:
			nv, err := Normalize(op.Value)
			if err != nil {
				return nil, Op{}, err
			}
			inv := Op{Kind: OpSet, Path: copyPath(path), Value: deepCopy(node)}
			return deepCopy(nv), inv, nil
		case OpArrayEdit:
			arr, ok := node.([]any)
			if !ok {
				return nil, Op{}, fmt.Errorf("%w: array edit on non-array", ErrInvalidOp)
			}
			out, invEdits, err := applyEdits(arr, op.Edits)
			if err != nil {
				return nil, Op{}, err
			}
			return out, Op{Kind: OpArrayEdit, Path: copyPath(path), Edits: invEdits}, nil
		}
		return nil, Op{}, fmt.Errorf("%w: op kind %d at leaf", ErrInvalidOp, op.Kind)
	}
	seg := path[at]
	last := at == len(path)-1
	switch n := node.(type) {
	case map[string]any:
		if seg.IsIndex {
			return nil, Op{}, fmt.Errorf("%w: array index into object", ErrInvalidOp)
		}
		if last && op.Kind == OpDel {
			old, existed := n[seg.Key]
			if !existed {
				return nil, Op{}, fmt.Errorf("%w: delete of missing key %q", ErrInvalidOp, seg.Key)
			}
			delete(n, seg.Key)
			return n, Op{Kind: OpSet, Path: copyPath(path), Value: deepCopy(old)}, nil
		}
		child, existed := n[seg.Key]
		if !existed {
			if last && op.Kind == OpSet {
				nv, err := Normalize(op.Value)
				if err != nil {
					return nil, Op{}, err
				}
				n[seg.Key] = deepCopy(nv)
				return n, Op{Kind: OpDel, Path: copyPath(path)}, nil
			}
			return nil, Op{}, fmt.Errorf("%w: missing key %q", ErrInvalidOp, seg.Key)
		}
		nchild, inv, err := applyOp(child, path, at+1, op)
		if err != nil {
			return nil, Op{}, err
		}
		n[seg.Key] = nchild
		return n, inv, nil
	case []any:
		if !seg.IsIndex {
			return nil, Op{}, fmt.Errorf("%w: object key into array", ErrInvalidOp)
		}
		if seg.Index < 0 || seg.Index >= len(n) {
			return nil, Op{}, fmt.Errorf("%w: index %d out of range", ErrInvalidOp, seg.Index)
		}
		if last && op.Kind == OpDel {
			return nil, Op{}, fmt.Errorf("%w: delete of array element; use array edit", ErrInvalidOp)
		}
		nchild, inv, err := applyOp(n[seg.Index], path, at+1, op)
		if err != nil {
			return nil, Op{}, err
		}
		n[seg.Index] = nchild
		return n, inv, nil
	}
	return nil, Op{}, fmt.Errorf("%w: path into scalar", ErrInvalidOp)
}

// applyEdits runs a keep/delete/insert script over old, returning the new
// array and the inverse script.
func applyEdits(old []any, edits []Edit) ([]any, []Edit, error) {
	var out []any
	var inv []Edit
	i := 0
	for _, e := range edits {
		switch e.Kind {
		case EditKeep:
			if e.N < 0 || i+e.N > len(old) {
				return nil, nil, fmt.Errorf("%w: keep overruns array", ErrInvalidOp)
			}
			out = append(out, old[i:i+e.N]...)
			i += e.N
			inv = append(inv, Edit{Kind: EditKeep, N: e.N})
		case EditDelete:
			if e.N < 0 || i+e.N > len(old) {
				return nil, nil, fmt.Errorf("%w: delete overruns array", ErrInvalidOp)
			}
			vals := make([]any, e.N)
			for k := 0; k < e.N; k++ {
				vals[k] = deepCopy(old[i+k])
			}
			i += e.N
			inv = append(inv, Edit{Kind: EditInsert, Values: vals})
		case EditInsert:
			vals := make([]any, len(e.Values))
			for k, v := range e.Values {
				nv, err := Normalize(v)
				if err != nil {
					return nil, nil, err
				}
				vals[k] = deepCopy(nv)
			}
			out = append(out, vals...)
			inv = append(inv, Edit{Kind: EditDelete, N: len(vals)})
		default:
			return nil, nil, fmt.Errorf("%w: unknown edit kind %d", ErrInvalidOp, e.Kind)
		}
	}
	if i != len(old) {
		return nil, nil, fmt.Errorf("%w: edit script does not consume the whole array", ErrInvalidOp)
	}
	return out, inv, nil
}
