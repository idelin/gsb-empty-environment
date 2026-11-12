package netsec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Standard members of a Ruby gem tar archive.
const (
	MemberMetadata  = "metadata.gz"
	MemberData      = "data.tar.gz"
	MemberChecksums = "checksums.yaml.gz"
)

const defaultMaxMemberBytes = 1 << 30 // 1 GiB per member

// maxChecksumsBytes bounds the decompressed size of checksums.yaml; the real
// document is only a few hundred bytes, so anything larger is an attack.
const maxChecksumsBytes = 1 << 20 // 1 MiB

var requiredMembers = []string{MemberMetadata, MemberData, MemberChecksums}

func isKnownMember(name string) bool {
	switch name {
	case MemberMetadata, MemberData, MemberChecksums,
		MemberMetadata + ".sig", MemberData + ".sig", MemberChecksums + ".sig":
		return true
	}
	return false
}

// ReadMembers parses the outer gem tar and returns the raw bytes of every
// member keyed by its normalized name. A member name appearing more than once
// is rejected outright, so checksum verification and extraction can never
// observe different bytes for the same name. Unknown members and non-regular
// entries are rejected as well.
func ReadMembers(r io.Reader, maxMemberBytes int64) (map[string][]byte, error) {
	if maxMemberBytes <= 0 {
		maxMemberBytes = defaultMaxMemberBytes
	}

	members := make(map[string][]byte)
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedArchive, err)
		}

		name := normalizeName(hdr.Name)
		if name == "" || name == "." || name == ".." || isTraversal(hdr.Name) {
			return nil, fmt.Errorf("%w: invalid member path %q", ErrMalformedArchive, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("%w: non-regular member %q", ErrMalformedArchive, name)
		}
		if !isKnownMember(name) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownMember, name)
		}
		if _, exists := members[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateMember, name)
		}

		limited := io.LimitReader(tr, maxMemberBytes+1)
		raw, err := io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedArchive, err)
		}
		if int64(len(raw)) > maxMemberBytes {
			return nil, fmt.Errorf("%w: member %q exceeds size limit", ErrMalformedArchive, name)
		}
		members[name] = raw
	}

	for _, name := range requiredMembers {
		if _, ok := members[name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrMissingMember, name)
		}
	}
	return members, nil
}

func normalizeName(name string) string {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	return name
}

func isTraversal(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func gunzip(raw []byte, maxDecompressed int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, maxDecompressed+1))
	if err != nil {
		return nil, err
	}
	if err := zr.Close(); err != nil {
		return nil, err
	}
	if int64(len(out)) > maxDecompressed {
		return nil, fmt.Errorf("decompressed content exceeds %d bytes", maxDecompressed)
	}
	return out, nil
}

// VerifyChecksums gunzips checksums.yaml.gz, parses its SHA512 section and
// compares every recorded digest against the actual member bytes. Both
// metadata.gz and data.tar.gz must be covered, and every covered member must
// be present and match.
func VerifyChecksums(members map[string][]byte) error {
	raw, ok := members[MemberChecksums]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMissingMember, MemberChecksums)
	}

	plain, err := gunzip(raw, maxChecksumsBytes)
	if err != nil {
		return fmt.Errorf("%w: cannot gunzip checksums: %v", ErrMalformedChecksums, err)
	}
	digests, err := parseChecksumsYAML(plain)
	if err != nil {
		return err
	}

	shaSection, ok := digests["SHA512"]
	if !ok {
		return fmt.Errorf("%w: missing SHA512 section", ErrMalformedChecksums)
	}
	for _, required := range []string{MemberMetadata, MemberData} {
		if _, ok := shaSection[required]; !ok {
			return fmt.Errorf("%w: no SHA512 for %q", ErrMalformedChecksums, required)
		}
	}

	for name, wantHex := range shaSection {
		actual, ok := members[name]
		if !ok {
			return fmt.Errorf("%w: checksum references missing member %q", ErrMalformedChecksums, name)
		}
		sum := sha512.Sum512(actual)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), wantHex) {
			return fmt.Errorf("%w: %q", ErrChecksumMismatch, name)
		}
	}
	return nil
}

// parseChecksumsYAML parses the small, fixed-shape checksums.yaml document:
//
//	---
//	# comments and blank lines are allowed
//	SHA512:
//	  metadata.gz: <128 hex chars>
//	  data.tar.gz: <128 hex chars>
//
// It deliberately supports no general YAML features; anything outside this
// shape is rejected, and repeated keys are treated as an error.
func parseChecksumsYAML(doc []byte) (map[string]map[string]string, error) {
	result := make(map[string]map[string]string)
	var section string

	for lineNo, rawLine := range strings.Split(string(doc), "\n") {
		line := stripComment(rawLine)
		if strings.TrimSpace(line) == "" || strings.TrimSpace(line) == "---" || strings.TrimSpace(line) == "..." {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.ContainsAny(line, "\t") {
			return nil, fmt.Errorf("%w: tab indentation on line %d", ErrMalformedChecksums, lineNo+1)
		}

		key, value, hasColon := splitKV(strings.TrimSpace(line))
		if !hasColon {
			return nil, fmt.Errorf("%w: cannot parse line %d", ErrMalformedChecksums, lineNo+1)
		}

		switch {
		case indent == 0:
			section = key
			if _, exists := result[section]; exists {
				return nil, fmt.Errorf("%w: duplicate key %q", ErrMalformedChecksums, section)
			}
			result[section] = make(map[string]string)
			if value != "" {
				return nil, fmt.Errorf("%w: unexpected scalar value for %q", ErrMalformedChecksums, section)
			}
		default:
			if section == "" {
				return nil, fmt.Errorf("%w: indented entry without section on line %d", ErrMalformedChecksums, lineNo+1)
			}
			digest, err := unquote(value)
			if err != nil {
				return nil, fmt.Errorf("%w line %d: %v", ErrMalformedChecksums, lineNo+1, err)
			}
			if len(digest) != sha512.Size*2 {
				return nil, fmt.Errorf("%w: %q digest is not a SHA512 hex string", ErrMalformedChecksums, key)
			}
			if _, err := hex.DecodeString(digest); err != nil {
				return nil, fmt.Errorf("%w: %q digest is not valid hex", ErrMalformedChecksums, key)
			}
			name, err := unquote(key)
			if err != nil {
				return nil, fmt.Errorf("%w line %d: %v", ErrMalformedChecksums, lineNo+1, err)
			}
			if _, exists := result[section][name]; exists {
				return nil, fmt.Errorf("%w: duplicate key %q", ErrMalformedChecksums, name)
			}
			result[section][name] = strings.ToLower(digest)
		}
	}
	return result, nil
}

func splitKV(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	return key, value, true
}

func stripComment(line string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i]
			}
		}
	}
	return line
}

func unquote(s string) (string, error) {
	if len(s) >= 2 {
		switch s[0] {
		case '"':
			if s[len(s)-1] != '"' {
				return "", errors.New("unterminated double quote")
			}
			return strings.ReplaceAll(s[1:len(s)-1], `\"`, `"`), nil
		case '\'':
			if s[len(s)-1] != '\'' {
				return "", errors.New("unterminated single quote")
			}
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
		}
	}
	return s, nil
}
