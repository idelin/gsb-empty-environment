package dsc

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const (
	filesField  = "Files"
	sha256Field = "Checksums-Sha256"
	sha1Field   = "Checksums-Sha1"
)

var (
	fieldNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
	armorHdrRE  = regexp.MustCompile(`^(Hash|Comment|MessageID|Version): [ -~]*$`)
	armorBodyRE = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
)

func armorHeaderLine(line string) bool { return armorHdrRE.MatchString(line) }

func armorBodyLine(line string) bool {
	if line == "" {
		return false
	}
	return armorBodyRE.MatchString(line)
}

// parseParagraph parses the signed cleartext as a single RFC 822-style
// control paragraph with folded continuation lines. A blank line anywhere
// inside the cleartext ends the paragraph; anything after it is rejected.
func parseParagraph(cleartext []byte) ([]Field, error) {
	body := cleartext
	if len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	body = bytes.Trim(body, "\n")
	if len(body) == 0 {
		return nil, errors.Join(ErrMalformed, errors.New("empty signed control paragraph"))
	}

	lines := bytes.Split(body, []byte("\n"))
	var fields []Field
	for i := 0; i < len(lines); {
		line := string(lines[i])
		if line == "" {
			return nil, errors.Join(ErrField, errors.New("blank line inside signed paragraph"))
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, errors.Join(ErrField, errors.New("continuation line without a field"))
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, errors.Join(ErrField, errors.New("field without colon"))
		}
		name := line[:colon]
		if !fieldNameRE.MatchString(name) {
			return nil, errors.Join(ErrField, errors.New("invalid field name: "+name))
		}
		rest := line[colon+1:]
		if len(rest) > 0 && rest[0] != ' ' {
			return nil, errors.Join(ErrField, errors.New("field value not introduced by SP: "+name))
		}

		var valueLines []string
		if rest != "" {
			valueLines = append(valueLines, rest[1:])
		}
		i++
		for i < len(lines) && (len(lines[i]) > 0 && (lines[i][0] == ' ' || lines[i][0] == '\t')) {
			valueLines = append(valueLines, string(lines[i][1:]))
			i++
		}
		fields = append(fields, Field{Name: name, Value: strings.Join(valueLines, "\n")})
	}
	return fields, nil
}

// checksumRecord is one whitespace-separated line of a Files or
// Checksums-* field: digest, size, path.
type checksumRecord struct {
	hash string
	size int64
	name string
}

func parseChecksumRecords(value, fieldName string) ([]checksumRecord, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.Join(ErrField, errors.New(fieldName+" field is empty"))
	}
	var records []checksumRecord
	for lineNo, line := range strings.Split(value, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, errors.Join(ErrField, errors.New(fieldName+": malformed record on line "+strconv.Itoa(lineNo+1)))
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 {
			return nil, errors.Join(ErrField, errors.New(fieldName+": invalid size for "+parts[2]))
		}
		records = append(records, checksumRecord{hash: parts[0], size: size, name: parts[2]})
	}
	return records, nil
}

// validateName enforces basename-only references: no path separators and no
// dotfiles (which would include "." and "..").
func validateName(name string) error {
	if name == "" {
		return errors.Join(ErrField, errors.New("empty file name"))
	}
	if strings.Contains(name, "/") {
		return errors.Join(ErrField, errors.New("file name must not contain \"/\": "+name))
	}
	if name[0] == '.' {
		return errors.Join(ErrField, errors.New("file name must not start with \".\": "+name))
	}
	return nil
}

func validHexDigest(s string, wantLen int) bool {
	if len(s) != wantLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func fieldByName(fields []Field, name string) []Field {
	var out []Field
	for _, f := range fields {
		if equalFoldASCII(f.Name, name) {
			out = append(out, f)
		}
	}
	return out
}

// buildFiles merges the Files and Checksums-* fields into [FileEntry]
// values, enforcing uniqueness, naming, size agreement and digest agreement
// across every field that mentions a path.
func (d *DSC) buildFiles() error {
	filesFields := fieldByName(d.Fields, filesField)
	sha256Fields := fieldByName(d.Fields, sha256Field)
	sha1Fields := fieldByName(d.Fields, sha1Field)

	if len(filesFields) != 1 {
		return errors.Join(ErrField, errors.New("exactly one Files field is required"))
	}
	if len(sha256Fields) != 1 {
		return errors.Join(ErrField, errors.New("exactly one Checksums-Sha256 field is required"))
	}
	if len(sha1Fields) > 1 {
		return errors.Join(ErrField, errors.New("at most one Checksums-Sha1 field is allowed"))
	}

	filesRecords, err := parseChecksumRecords(filesFields[0].Value, filesField)
	if err != nil {
		return err
	}
	sha256Records, err := parseChecksumRecords(sha256Fields[0].Value, sha256Field)
	if err != nil {
		return err
	}
	var sha1Records []checksumRecord
	if len(sha1Fields) == 1 {
		sha1Records, err = parseChecksumRecords(sha1Fields[0].Value, sha1Field)
		if err != nil {
			return err
		}
	}

	entries := make(map[string]*FileEntry, len(filesRecords))
	ordered := make([]string, 0, len(filesRecords))
	for _, rec := range filesRecords {
		if err := validateName(rec.name); err != nil {
			return err
		}
		if !validHexDigest(rec.hash, md5HexLen) {
			return errors.Join(ErrField, errors.New(filesField+": invalid md5 digest for "+rec.name))
		}
		if _, dup := entries[rec.name]; dup {
			return errors.Join(ErrField, errors.New(filesField+": duplicate file name: "+rec.name))
		}
		digest, err := decodeHex(rec.hash)
		if err != nil {
			return errors.Join(ErrField, errors.New(filesField+": invalid md5 digest for "+rec.name))
		}
		entries[rec.name] = &FileEntry{Name: rec.name, Size: rec.size, MD5: digest}
		ordered = append(ordered, rec.name)
	}

	if err := mergeChecksums(entries, sha256Records, sha256Field, sha256HexLen, true); err != nil {
		return err
	}
	if len(sha1Fields) == 1 {
		if err := mergeChecksums(entries, sha1Records, sha1Field, sha1HexLen, false); err != nil {
			return err
		}
	}

	d.Files = make([]FileEntry, 0, len(ordered))
	for _, name := range ordered {
		entry := entries[name]
		if len(entry.SHA256) == 0 {
			return errors.Join(ErrChecksumMismatch, errors.New(sha256Field+": missing digest for "+name))
		}
		if len(sha1Fields) == 1 && len(entry.SHA1) == 0 {
			return errors.Join(ErrChecksumMismatch, errors.New(sha1Field+": missing digest for "+name))
		}
		d.Files = append(d.Files, *entry)
	}
	return nil
}

// mergeChecksums folds one Checksums-* field into the entry map. Every
// record must reference a Files entry with the same size; unknown paths and
// duplicated records are rejected.
func mergeChecksums(entries map[string]*FileEntry, records []checksumRecord, fieldName string, hexLen int, required bool) error {
	seen := make(map[string]bool, len(records))
	for _, rec := range records {
		if err := validateName(rec.name); err != nil {
			return err
		}
		if seen[rec.name] {
			return errors.Join(ErrField, errors.New(fieldName+": duplicate file name: "+rec.name))
		}
		seen[rec.name] = true
		if !validHexDigest(rec.hash, hexLen) {
			return errors.Join(ErrField, errors.New(fieldName+": invalid digest for "+rec.name))
		}
		digest, err := decodeHex(rec.hash)
		if err != nil {
			return errors.Join(ErrField, errors.New(fieldName+": invalid digest for "+rec.name))
		}
		entry, ok := entries[rec.name]
		if !ok {
			return errors.Join(ErrField, errors.New(fieldName+": references file absent from Files: "+rec.name))
		}
		if entry.Size != rec.size {
			return errors.Join(ErrChecksumMismatch, errors.New(fieldName+": size disagrees with Files for "+rec.name))
		}
		switch fieldName {
		case sha256Field:
			entry.SHA256 = digest
		case sha1Field:
			entry.SHA1 = digest
		}
	}
	if required && len(seen) != len(entries) {
		return errors.Join(ErrChecksumMismatch, errors.New(fieldName+": does not cover every Files entry"))
	}
	return nil
}

func decodeHex(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexNibble(s[2*i])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(s[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, errors.New("invalid hex digit")
	}
}
