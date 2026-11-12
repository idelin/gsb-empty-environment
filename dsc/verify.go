package dsc

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// VerifyFiles opens every referenced file relative to dir (the directory
// holding the .dsc) and checks its recorded size and every recorded digest
// (MD5 from Files, SHA-1, SHA-256) against the actual content.
//
// Referenced names were already constrained to basenames without a leading
// dot during parsing; this function additionally refuses symlinks and any
// path that is not a regular file.
func (d *DSC) VerifyFiles(dir string) error {
	for _, entry := range d.Files {
		path := filepath.Join(dir, entry.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return errors.Join(ErrChecksumMismatch, err)
		}
		if !info.Mode().IsRegular() {
			return errors.Join(ErrChecksumMismatch, errors.New("not a regular file: "+entry.Name))
		}
		if info.Size() != entry.Size {
			return errors.Join(ErrChecksumMismatch, errors.New("size mismatch for "+entry.Name))
		}

		hashes := newHasherSet(entry)
		f, err := os.Open(path)
		if err != nil {
			return errors.Join(ErrChecksumMismatch, err)
		}
		_, copyErr := io.Copy(hashes.writer(), f)
		closeErr := f.Close()
		if copyErr != nil {
			return errors.Join(ErrChecksumMismatch, copyErr)
		}
		if closeErr != nil {
			return errors.Join(ErrChecksumMismatch, closeErr)
		}
		if err := hashes.check(entry); err != nil {
			return err
		}
	}
	return nil
}

type hasherSet struct {
	md5    hash.Hash
	sha1   hash.Hash
	sha256 hash.Hash
}

func newHasherSet(entry FileEntry) *hasherSet {
	set := &hasherSet{}
	if len(entry.MD5) > 0 {
		set.md5 = md5.New()
	}
	if len(entry.SHA1) > 0 {
		set.sha1 = sha1.New()
	}
	if len(entry.SHA256) > 0 {
		set.sha256 = sha256.New()
	}
	return set
}

func (s *hasherSet) writer() io.Writer {
	var writers []io.Writer
	if s.md5 != nil {
		writers = append(writers, s.md5)
	}
	if s.sha1 != nil {
		writers = append(writers, s.sha1)
	}
	if s.sha256 != nil {
		writers = append(writers, s.sha256)
	}
	return io.MultiWriter(writers...)
}

func (s *hasherSet) check(entry FileEntry) error {
	if s.md5 != nil && !bytes.Equal(s.md5.Sum(nil), entry.MD5) {
		return errors.Join(ErrChecksumMismatch, errors.New("md5 mismatch for "+entry.Name))
	}
	if s.sha1 != nil && !bytes.Equal(s.sha1.Sum(nil), entry.SHA1) {
		return errors.Join(ErrChecksumMismatch, errors.New("sha1 mismatch for "+entry.Name))
	}
	if s.sha256 != nil && !bytes.Equal(s.sha256.Sum(nil), entry.SHA256) {
		return errors.Join(ErrChecksumMismatch, errors.New("sha256 mismatch for "+entry.Name))
	}
	return nil
}

// sourcesEntry is one paragraph of a Debian Sources index.
type sourcesEntry struct {
	directory string
	files     map[string][]byte // basename -> sha256
}

// VerifyInSources anchors the .dsc in the trusted Sources index: an index
// paragraph must publish this exact SHA-256 digest for the .dsc filename
// (matched as basename or relative to its Directory field).
func (d *DSC) VerifyInSources(sources []byte, dscName string) error {
	if strings.Contains(dscName, "/") || strings.HasPrefix(dscName, ".") {
		return errors.Join(ErrSources, errors.New("invalid .dsc name: "+dscName))
	}
	want := sha256.Sum256(d.raw)

	entries, err := parseSources(sources)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		for indexName, digest := range entry.files {
			base := filepath.Base(indexName)
			relative := strings.TrimPrefix(indexName, entry.directory+"/")
			matchesName := base == dscName || indexName == dscName || relative == dscName
			if matchesName && bytes.Equal(digest, want[:]) {
				return nil
			}
		}
	}
	return errors.Join(ErrSources, errors.New("no Sources entry publishes sha256 for "+dscName))
}

// VerifyChain runs the full post-signature chain: on-disk file verification
// followed by Sources index anchoring.
func (d *DSC) VerifyChain(dir string, sources []byte, dscName string) error {
	if err := d.VerifyFiles(dir); err != nil {
		return err
	}
	return d.VerifyInSources(sources, dscName)
}

// parseSources splits a Sources index into paragraphs and records the
// SHA-256 line of each Checksums-Sha256 field.
func parseSources(data []byte) ([]sourcesEntry, error) {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	paragraphs := bytes.Split(normalized, []byte("\n\n"))
	var entries []sourcesEntry
	for idx, paragraph := range paragraphs {
		if len(bytes.TrimSpace(paragraph)) == 0 {
			continue
		}
		fields, err := parseParagraph(append(append([]byte(nil), paragraph...), '\n'))
		if err != nil {
			return nil, errors.Join(ErrMalformed, errors.New("Sources paragraph "+strconv.Itoa(idx+1)+": "+err.Error()))
		}
		entry := sourcesEntry{
			directory: firstValue(fields, "Directory"),
			files:     map[string][]byte{},
		}
		for _, value := range allValues(fields, sha256Field) {
			records, err := parseChecksumRecords(value, sha256Field)
			if err != nil {
				return nil, err
			}
			for _, rec := range records {
				digest, err := decodeHex(rec.hash)
				if err != nil {
					return nil, errors.Join(ErrField, errors.New("Sources: invalid sha256 for "+rec.name))
				}
				if _, dup := entry.files[rec.name]; dup {
					return nil, errors.Join(ErrField, errors.New("Sources: duplicate sha256 entry for "+rec.name))
				}
				entry.files[rec.name] = digest
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func firstValue(fields []Field, name string) string {
	for _, f := range fields {
		if equalFoldASCII(f.Name, name) {
			return f.Value
		}
	}
	return ""
}

func allValues(fields []Field, name string) []string {
	var out []string
	for _, f := range fields {
		if equalFoldASCII(f.Name, name) {
			out = append(out, f.Value)
		}
	}
	return out
}
