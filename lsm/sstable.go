// Package lsm implements a minimal LSM-tree storage engine with immutable
// SSTables, memtable flushes and size-tiered compaction. SSTable files are
// never modified in place, which makes them safe to snapshot via hard links.
package lsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
)

var sstMagic = []byte("SST1")

// WriteSSTable atomically writes entries as a sorted, immutable SSTable file.
// The file is written to a temporary path, fsynced and renamed into place so
// readers never observe a partially written table.
func WriteSSTable(path string, entries map[string]string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if err := encodeSSTable(f, entries); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func encodeSSTable(w io.Writer, entries map[string]string) error {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if _, err := w.Write(sstMagic); err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(keys)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	for _, k := range keys {
		v := entries[k]
		var kh [2]byte
		binary.BigEndian.PutUint16(kh[:], uint16(len(k)))
		var vh [4]byte
		binary.BigEndian.PutUint32(vh[:], uint32(len(v)))
		for _, b := range [][]byte{kh[:], []byte(k), vh[:], []byte(v)} {
			if _, err := w.Write(b); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadSSTable loads a whole SSTable into memory. Tables in this demo engine
// are small (one flush each), so a full scan is acceptable.
func ReadSSTable(path string) (map[string]string, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 || !bytes.Equal(data[:4], sstMagic) {
		return nil, fmt.Errorf("lsm: %s is not an SSTable", path)
	}
	r := bytes.NewReader(data[4:])
	var count uint32
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	out := make(map[string]string, count)
	for i := uint32(0); i < count; i++ {
		var klen uint16
		if err := binary.Read(r, binary.BigEndian, &klen); err != nil {
			return nil, err
		}
		kb := make([]byte, klen)
		if _, err := io.ReadFull(r, kb); err != nil {
			return nil, err
		}
		var vlen uint32
		if err := binary.Read(r, binary.BigEndian, &vlen); err != nil {
			return nil, err
		}
		vb := make([]byte, vlen)
		if _, err := io.ReadFull(r, vb); err != nil {
			return nil, err
		}
		out[string(kb)] = string(vb)
	}
	return out, nil
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
