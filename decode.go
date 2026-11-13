package netsec

import (
	"bytes"
	"io"
	"os"
)

// Archive is an alias for Reader.
type Archive = Reader

// Entry is an alias for File.
type Entry = File

// OpenBytes opens a ZIP archive held in memory.
func OpenBytes(data []byte) (*Reader, error) {
	return NewReader(bytes.NewReader(data), int64(len(data)))
}

// Decode validates and decodes an archive, returning all of its entries.
func Decode(r io.ReaderAt, size int64) ([]*File, error) {
	zr, err := NewReader(r, size)
	if err != nil {
		return nil, err
	}
	return zr.Files(), nil
}

// DecodeBytes validates and decodes an in-memory archive.
func DecodeBytes(data []byte) ([]*File, error) {
	return Decode(bytes.NewReader(data), int64(len(data)))
}

// Validate checks the central directory and every local header without
// extracting file contents.
func Validate(r io.ReaderAt, size int64) error {
	_, err := NewReader(r, size)
	return err
}

// ValidateBytes checks an in-memory archive.
func ValidateBytes(data []byte) error {
	return Validate(bytes.NewReader(data), int64(len(data)))
}

// ValidateFile checks an archive on disk.
func ValidateFile(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return Validate(f, info.Size())
}

// ReadAll extracts every entry in the archive.
func ReadAll(r io.ReaderAt, size int64) (map[string][]byte, error) {
	zr, err := NewReader(r, size)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(zr.Files()))
	for _, f := range zr.Files() {
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, oerr
		}
		data, rerr := io.ReadAll(rc)
		cerr := rc.Close()
		if rerr != nil {
			return nil, rerr
		}
		if cerr != nil {
			return nil, cerr
		}
		out[f.Name] = data
	}
	return out, nil
}
