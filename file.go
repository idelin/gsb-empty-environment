package kvlog

import "io"

// File is the injectable file abstraction. All durable-state transitions
// go through Sync; anything written but not synced may be lost on crash.
type File interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Truncate(size int64) error
	Size() int64
	Close() error
}

// FS is the injectable filesystem abstraction. Rename must be atomic.
type FS interface {
	OpenFile(name string) (File, error)
	Rename(old, new string) error
	Remove(name string) error
}
