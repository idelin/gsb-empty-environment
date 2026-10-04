package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Run file format: repeated [uvarint keyLen][key bytes][uvarint source][uvarint seq].
// Offsets recorded in checkpoints are exact byte offsets into this stream.

const maxKeyLen = 1 << 30

func encodeRecord(w *bufio.Writer, rec Record) error {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(rec.Key)))
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	if _, err := w.WriteString(rec.Key); err != nil {
		return err
	}
	n = binary.PutUvarint(tmp[:], uint64(rec.Source))
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	n = binary.PutUvarint(tmp[:], uint64(rec.Seq))
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	return nil
}

// writeRunFile writes a sorted run, fsyncs it and closes it. The caller must
// only publish the file in a checkpoint after this returns nil.
func writeRunFile(path string, recs []Record) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 64*1024)
	for _, rec := range recs {
		if err := encodeRecord(w, rec); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) ReadByte() (byte, error) {
	var b [1]byte
	n, err := c.r.Read(b[:])
	c.n += int64(n)
	if n == 0 {
		if err == nil {
			err = io.ErrNoProgress
		}
		return 0, err
	}
	return b[0], nil
}

// decodeRecord reads one record and reports the exact number of bytes consumed.
// Returns io.EOF only at a clean record boundary.
func decodeRecord(r io.Reader) (Record, int64, error) {
	cr := &countingReader{r: r}
	keyLen, err := binary.ReadUvarint(cr)
	if err != nil {
		return Record{}, cr.n, err
	}
	if keyLen > maxKeyLen {
		return Record{}, cr.n, fmt.Errorf("record key too large (%d bytes), corrupt run file?", keyLen)
	}
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(cr, key); err != nil {
		return Record{}, cr.n, err
	}
	src, err := binary.ReadUvarint(cr)
	if err != nil {
		return Record{}, cr.n, err
	}
	seq, err := binary.ReadUvarint(cr)
	if err != nil {
		return Record{}, cr.n, err
	}
	return Record{Key: string(key), Source: int(src), Seq: int(seq)}, cr.n, nil
}

// runReader reads records sequentially from a run file, tracking the consumed
// byte offset so checkpoints can record merge progress per run.
type runReader struct {
	f   *os.File
	off int64
}

func openRunReader(path string, off int64) (*runReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return &runReader{f: f, off: off}, nil
}

// next returns the next record and the file offset just past it (the offset up
// to which the run has been consumed once this record is emitted).
func (rr *runReader) next() (Record, int64, error) {
	rec, n, err := decodeRecord(rr.f)
	if err != nil {
		return Record{}, 0, err
	}
	rr.off += n
	return rec, rr.off, nil
}

func (rr *runReader) close() { rr.f.Close() }
