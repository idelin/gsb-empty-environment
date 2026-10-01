package cidx

import (
	"bytes"
	"sync"
)

// View is a read-only query handle bound to one mapping generation. It
// holds a borrow for its whole lifetime, so the mapping stays alive while
// any view is open. Closing a view never affects other views on the same
// mapping.
type View struct {
	m   *Mapping
	gen uint64

	mu     sync.Mutex
	closed bool
}

// OpenView borrows m and returns a view on it.
func OpenView(m *Mapping) (*View, error) {
	if err := m.borrow(); err != nil {
		return nil, err
	}
	return &View{m: m, gen: m.gen}, nil
}

// Generation returns the mapping generation this view is bound to.
func (v *View) Generation() uint64 { return v.gen }

// Query looks up key by binary search over the validated records. It
// returns ErrViewClosed after Close and ErrNotFound for a missing key.
func (v *View) Query(key []byte) ([]byte, error) {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return nil, ErrViewClosed
	}
	m := v.m
	if m.queryHook != nil {
		m.queryHook()
	}
	lo, hi := 0, int(m.layout.EntryCount)
	for lo < hi {
		mid := lo + (hi-lo)/2
		k, err := m.readKey(mid)
		if err != nil {
			return nil, err
		}
		switch cmp := bytes.Compare(k, key); {
		case cmp == 0:
			_, val, err := m.readRecord(mid)
			if err != nil {
				return nil, err
			}
			return append([]byte(nil), val...), nil
		case cmp < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return nil, ErrNotFound
}

// Cursor opens an iteration cursor. The cursor takes its own borrow,
// independent of the view's, so it keeps the mapping alive even after the
// view is closed.
func (v *View) Cursor() (*Cursor, error) {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return nil, ErrViewClosed
	}
	if err := v.m.borrow(); err != nil {
		return nil, err
	}
	return &Cursor{m: v.m}, nil
}

// Close releases the view's borrow. Further queries fail with
// ErrViewClosed. Close is idempotent.
func (v *View) Close() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	v.mu.Unlock()
	v.m.release()
}

// Cursor iterates records in key order. It is not safe for concurrent use;
// use one cursor per goroutine.
type Cursor struct {
	m      *Mapping
	idx    int
	closed bool
}

// Next returns the next key/value pair, or ok == false at the end.
func (c *Cursor) Next() (key, value []byte, ok bool, err error) {
	if c.closed {
		return nil, nil, false, ErrCursorClosed
	}
	if c.idx >= int(c.m.layout.EntryCount) {
		return nil, nil, false, nil
	}
	k, v, err := c.m.readRecord(c.idx)
	if err != nil {
		return nil, nil, false, err
	}
	c.idx++
	return k, v, true, nil
}

// Close releases the cursor's borrow. It is idempotent.
func (c *Cursor) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.m.release()
}
