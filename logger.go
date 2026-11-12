package netsec

import (
	"errors"
	"io"
	"os"
)

// Logger appends durable events to the authorization log. Each Append is
// followed by an fsync so a record is either fully durable or absent.
type Logger struct {
	f   *os.File
	seq uint64
}

// OpenLogger opens (or creates) the log at path for appending. The next
// sequence number is derived from the valid records already present.
func OpenLogger(path string) (*Logger, error) {
	var seq uint64
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		off := 0
		for off < len(data) {
			_, s, n, derr := decodeRecordAt(data, off)
			if derr != nil {
				break
			}
			off += n
			seq = s + 1
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Logger{f: f, seq: seq}, nil
}

// Append durably records e and returns its sequence number.
func (l *Logger) Append(e Event) (uint64, error) {
	rec, err := encodeRecord(l.seq, e)
	if err != nil {
		return 0, err
	}
	if _, err := l.f.Write(rec); err != nil {
		return 0, err
	}
	if err := l.f.Sync(); err != nil {
		return 0, err
	}
	seq := l.seq
	l.seq++
	return seq, nil
}

// Close flushes and closes the log.
func (l *Logger) Close() error {
	if err := l.f.Sync(); err != nil {
		_ = l.f.Close()
		return err
	}
	return l.f.Close()
}

var _ io.Closer = (*Logger)(nil)
