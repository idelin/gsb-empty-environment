package blockstore

// Editor stages copy-on-write edits on a working extent list. All temp
// allocations are tracked in one transaction: Cancel (or any failed
// operation) rolls the whole edit back, Commit publishes a new slice.
type Editor struct {
	st     *Store
	exts   []uint64
	length int
	txn    *txn
	closed bool
}

func newEditor(st *Store, exts []uint64, length int) *Editor {
	copied := make([]uint64, len(exts))
	copy(copied, exts)
	return &Editor{st: st, exts: copied, length: length, txn: st.beginTxn()}
}

// fail rolls the whole editor back and closes it.
func (e *Editor) fail(err error) error {
	e.txn.rollback()
	e.closed = true
	return err
}

// splice replaces the byte range [off, off+delN) of the working content
// with data. Whole untouched blocks are shared by reference; only the
// boundary blocks that mix old and new bytes are physically copied
// (copy-on-write), and those copies are temp allocations counted in the
// ledger until Commit.
func (e *Editor) splice(off, delN int, data []byte) error {
	if off < 0 || delN < 0 || off+delN > e.length {
		return e.fail(ErrOutOfRange)
	}
	st := e.st
	bs := st.bs
	src := e.exts
	srcLen := e.length

	out := make([]uint64, 0, len(src)+len(data)/bs+2)
	var buf []byte // partial output block, len < bs

	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		id, err := e.txn.alloc()
		if err != nil {
			return err
		}
		copy(st.blocks[id].data, buf)
		out = append(out, id)
		buf = buf[:0]
		return nil
	}

	// feedOld streams old content [oldOff, oldOff+n) into the output.
	// Block-aligned whole blocks are referenced, never copied.
	feedOld := func(oldOff, n int) error {
		for n > 0 {
			if len(buf) == 0 && oldOff%bs == 0 && n >= bs {
				out = append(out, src[oldOff/bs])
				oldOff += bs
				n -= bs
				continue
			}
			k := bs - len(buf)
			if k > n {
				k = n
			}
			// Copy k bytes out of old blocks (COW); may span old blocks.
			rem, oo := k, oldOff
			for rem > 0 {
				b := st.blocks[src[oo/bs]]
				inner := oo % bs
				c := bs - inner
				if c > rem {
					c = rem
				}
				buf = append(buf, b.data[inner:inner+c]...)
				st.copied += int64(c)
				oo += c
				rem -= c
			}
			oldOff += k
			n -= k
			if len(buf) == bs {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// feedData streams new payload bytes into the output.
	feedData := func(d []byte) error {
		for len(d) > 0 {
			if len(buf) == 0 && len(d) >= bs {
				id, err := e.txn.alloc()
				if err != nil {
					return err
				}
				copy(st.blocks[id].data, d[:bs])
				out = append(out, id)
				st.written += int64(bs)
				d = d[bs:]
				continue
			}
			k := bs - len(buf)
			if k > len(d) {
				k = len(d)
			}
			buf = append(buf, d[:k]...)
			st.written += int64(k)
			d = d[k:]
			if len(buf) == bs {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := feedOld(0, off); err != nil {
		return e.fail(err)
	}
	if err := feedData(data); err != nil {
		return e.fail(err)
	}
	if err := feedOld(off+delN, srcLen-(off+delN)); err != nil {
		return e.fail(err)
	}
	if err := flush(); err != nil {
		return e.fail(err)
	}
	e.exts = out
	e.length = srcLen - delN + len(data)
	return nil
}

// readRange reads n bytes from the working content at off.
func (e *Editor) readRange(off, n int) ([]byte, error) {
	if off < 0 || n < 0 || off+n > e.length {
		return nil, ErrOutOfRange
	}
	out := make([]byte, 0, n)
	bs := e.st.bs
	for n > 0 {
		b := e.st.blocks[e.exts[off/bs]]
		inner := off % bs
		k := bs - inner
		if k > n {
			k = n
		}
		out = append(out, b.data[inner:inner+k]...)
		off += k
		n -= k
	}
	return out, nil
}

// Overwrite stages a replacement of the len(data) bytes at off.
func (e *Editor) Overwrite(off int, data []byte) error {
	if e.closed {
		return ErrEditorClosed
	}
	if off < 0 || off+len(data) > e.length {
		return e.fail(ErrOutOfRange)
	}
	return e.splice(off, len(data), data)
}

// Insert stages an insertion of data at off.
func (e *Editor) Insert(off int, data []byte) error {
	if e.closed {
		return ErrEditorClosed
	}
	if off < 0 || off > e.length {
		return e.fail(ErrOutOfRange)
	}
	return e.splice(off, 0, data)
}

// Delete stages a removal of n bytes at off.
func (e *Editor) Delete(off, n int) error {
	if e.closed {
		return ErrEditorClosed
	}
	return e.splice(off, n, nil)
}

// Move stages a move of the n bytes at src to position dst (a position in
// the sequence after the removal). The moved bytes are snapshotted from
// the pre-operation content before any mutation, so overlapping source
// and destination ranges read the old bytes.
func (e *Editor) Move(dst, src, n int) error {
	if e.closed {
		return ErrEditorClosed
	}
	if n < 0 || src < 0 || src+n > e.length || dst < 0 || dst > e.length-n {
		return e.fail(ErrOutOfRange)
	}
	if n == 0 {
		return nil
	}
	moved, err := e.readRange(src, n) // rvalue: pre-operation content
	if err != nil {
		return e.fail(err)
	}
	if err := e.splice(src, n, nil); err != nil {
		return err // splice already rolled back
	}
	return e.splice(dst, 0, moved)
}

// CopyWithin stages a copy of the n bytes at src over the n bytes at dst.
// The source is read from the pre-operation content (memmove semantics).
func (e *Editor) CopyWithin(dst, src, n int) error {
	if e.closed {
		return ErrEditorClosed
	}
	if n < 0 || src < 0 || src+n > e.length || dst < 0 || dst+n > e.length {
		return e.fail(ErrOutOfRange)
	}
	if n == 0 {
		return nil
	}
	tmp, err := e.readRange(src, n) // rvalue: pre-operation content
	if err != nil {
		return e.fail(err)
	}
	return e.splice(dst, n, tmp)
}

// Commit publishes the staged edits as a new slice and frees unused temp
// blocks. The base slice is untouched.
func (e *Editor) Commit() (*Slice, error) {
	if e.closed {
		return nil, ErrEditorClosed
	}
	e.closed = true
	e.txn.commit(e.exts)
	return e.st.newSlice(e.length, e.exts), nil
}

// Cancel rolls the whole edit back: every temp block is freed and the
// ledger returns to its pre-edit state.
func (e *Editor) Cancel() {
	if e.closed {
		return
	}
	e.closed = true
	e.txn.rollback()
}
