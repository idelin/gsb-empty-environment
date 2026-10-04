package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Archive format (all integers little-endian):
//   magic: 8 bytes "UNARC001"
//   then entries until EOF:
//     u8    type: 'D' dir, 'F' file, 'S' symlink, 'H' hardlink
//     u16   path length, then path bytes (slash-separated, relative)
//     'F':  u32 mode, u64 size, then size bytes of payload
//     'S'/'H': u16 target length, then target bytes

var archiveMagic = []byte("UNARC001")

const (
	maxPathLen  = 4096
	maxFileSize = 1 << 31
	maxEntryCnt = 1 << 20
)

type entryType byte

const (
	entDir      entryType = 'D'
	entFile     entryType = 'F'
	entSymlink  entryType = 'S'
	entHardlink entryType = 'H'
)

type entry struct {
	typ    entryType
	path   string // cleaned, relative, slash-separated
	mode   os.FileMode
	data   []byte // file payload
	target string // symlink: raw target; hardlink: target path from archive

	resolved     string // path after in-root symlink resolution
	linkResolved string // hardlink only: resolved target path within root
}

func writeString(w io.Writer, s string) error {
	if len(s) > 0xffff {
		return fmt.Errorf("string too long: %d bytes", len(s))
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

func readString(r io.Reader) (string, error) {
	var n uint16
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > maxPathLen {
		return "", fmt.Errorf("string too long: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func writeArchive(w io.Writer, entries []*entry) error {
	if _, err := w.Write(archiveMagic); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := w.Write([]byte{byte(e.typ)}); err != nil {
			return err
		}
		if err := writeString(w, e.path); err != nil {
			return err
		}
		switch e.typ {
		case entDir:
		case entFile:
			if err := binary.Write(w, binary.LittleEndian, uint32(e.mode.Perm())); err != nil {
				return err
			}
			if err := binary.Write(w, binary.LittleEndian, uint64(len(e.data))); err != nil {
				return err
			}
			if _, err := w.Write(e.data); err != nil {
				return err
			}
		case entSymlink, entHardlink:
			if err := writeString(w, e.target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown entry type %q", byte(e.typ))
		}
	}
	return nil
}

func readArchive(r io.Reader) ([]*entry, error) {
	magic := make([]byte, len(archiveMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if !bytes.Equal(magic, archiveMagic) {
		return nil, fmt.Errorf("bad magic, not a unarc archive")
	}
	var entries []*entry
	typ := make([]byte, 1)
	for {
		_, err := r.Read(typ)
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		if len(entries) >= maxEntryCnt {
			return nil, fmt.Errorf("too many entries")
		}
		e := &entry{typ: entryType(typ[0])}
		if e.path, err = readString(r); err != nil {
			return nil, fmt.Errorf("entry %d: %w", len(entries), err)
		}
		switch e.typ {
		case entDir:
		case entFile:
			var mode uint32
			var size uint64
			if err := binary.Read(r, binary.LittleEndian, &mode); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
				return nil, err
			}
			if size > maxFileSize {
				return nil, fmt.Errorf("file %q too large: %d bytes", e.path, size)
			}
			e.mode = os.FileMode(mode).Perm()
			e.data = make([]byte, size)
			if _, err := io.ReadFull(r, e.data); err != nil {
				return nil, fmt.Errorf("reading %q payload: %w", e.path, err)
			}
		case entSymlink, entHardlink:
			if e.target, err = readString(r); err != nil {
				return nil, fmt.Errorf("entry %q: %w", e.path, err)
			}
		default:
			return nil, fmt.Errorf("unknown entry type %q", typ[0])
		}
		entries = append(entries, e)
	}
}
