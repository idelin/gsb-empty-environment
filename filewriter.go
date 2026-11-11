package parquetcol

import (
	"encoding/binary"
	"io"
)

// fileWriter tracks the absolute offset of bytes written to the Parquet file.
type fileWriter struct {
	w       io.Writer
	offset_ int64
}

func newFileWriter(w io.Writer) *fileWriter {
	return &fileWriter{w: w}
}

func (f *fileWriter) write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	f.offset_ += int64(n)
	return n, err
}

func (f *fileWriter) offset() int64 { return f.offset_ }

func (f *fileWriter) writeUint32(v uint32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	_, err := f.write(b[:])
	return err
}
