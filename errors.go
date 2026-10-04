package bptree

import "errors"

var (
	ErrNotFound        = errors.New("bptree: key not found")
	ErrSnapshotClosed  = errors.New("bptree: snapshot closed")
	ErrIteratorClosed  = errors.New("bptree: iterator closed")
	ErrValueTooLarge   = errors.New("bptree: value exceeds MaxValueSize")
	ErrTxDone          = errors.New("bptree: transaction already finished")
	ErrChecksum        = errors.New("bptree: page checksum mismatch")
	ErrCorrupt         = errors.New("bptree: corrupt page")
	ErrNotAllocated    = errors.New("bptree: page not allocated")
	ErrInjected        = errors.New("bptree: injected write failure")
	ErrInvalidOptions  = errors.New("bptree: invalid options")
	ErrInvalidPageSize = errors.New("bptree: invalid page size")
)
