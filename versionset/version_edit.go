// Package versionset implements a crash-consistent Manifest/VersionSet
// manager for an LSM-tree, using only the Go standard library.
//
// A VersionEdit describes an atomic transition of the LSM directory
// structure: which SSTable files are added to / removed from each level,
// together with per-file CRC32 checksums. Edits are appended to a MANIFEST
// log; each record is framed as
//
//	[4B payload length][4B CRC32 of payload][JSON payload]
//
// and fsynced before the in-memory version is swapped, so a committed edit
// is durable. On recovery the log is replayed; a torn tail (partial record
// or checksum mismatch) is discarded, and SSTable files on disk that are
// not referenced by the recovered version are deleted as orphans.
package versionset

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
)

// NumLevels is the number of LSM levels tracked by the VersionSet.
const NumLevels = 7

// ErrCorruptRecord is returned when a manifest record fails CRC validation.
var ErrCorruptRecord = errors.New("versionset: corrupt manifest record")

// FileMetadata describes a single live SSTable file.
type FileMetadata struct {
	Number   uint64 `json:"number"`   // unique file number, also the file name: %06d.sst
	Size     uint64 `json:"size"`     // file size in bytes
	Smallest string `json:"smallest"` // smallest key in the table
	Largest  string `json:"largest"`  // largest key in the table
	Checksum uint32 `json:"checksum"` // CRC32-IEEE of the whole file content
}

// DeletedFileEntry marks a file for removal from a level.
type DeletedFileEntry struct {
	Level  int    `json:"level"`
	Number uint64 `json:"number"`
}

// NewFileEntry adds a file to a level.
type NewFileEntry struct {
	Level int          `json:"level"`
	Meta  FileMetadata `json:"meta"`
}

// VersionEdit is the atomic unit of VersionSet evolution.
type VersionEdit struct {
	LogNumber      uint64             `json:"log_number,omitempty"`
	NextFileNumber uint64             `json:"next_file_number,omitempty"`
	LastSequence   uint64             `json:"last_sequence,omitempty"`
	DeletedFiles   []DeletedFileEntry `json:"deleted_files,omitempty"`
	NewFiles       []NewFileEntry     `json:"new_files,omitempty"`
}

// encodeRecord frames one edit and appends it to w.
func encodeRecord(w io.Writer, edit *VersionEdit) error {
	payload, err := json.Marshal(edit)
	if err != nil {
		return err
	}
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], crc32.ChecksumIEEE(payload))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// decodeRecord reads one framed record. It returns io.EOF at a clean end of
// log, and io.ErrUnexpectedEOF or ErrCorruptRecord for a torn tail.
func decodeRecord(r io.Reader) (*VersionEdit, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err // io.EOF (clean) or io.ErrUnexpectedEOF (torn header)
	}
	length := binary.LittleEndian.Uint32(hdr[0:4])
	crc := binary.LittleEndian.Uint32(hdr[4:8])
	if length > 1<<30 {
		return nil, ErrCorruptRecord
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, io.ErrUnexpectedEOF // torn payload
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, ErrCorruptRecord
	}
	var edit VersionEdit
	if err := json.Unmarshal(payload, &edit); err != nil {
		return nil, ErrCorruptRecord
	}
	return &edit, nil
}
