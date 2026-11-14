package depverify

import (
	"bufio"
	"fmt"
	"strings"
)

// SumKey identifies a checksum line in go.sum: a module path plus either a
// version (module zip hash, "<path> <version>") or the go.mod pseudo
// version ("<path> <version>/go.mod").
type SumKey struct {
	Path    string
	Version string
	GoMod   bool
}

// GoSumFile holds the h1: hashes parsed from a go.sum file.
type GoSumFile struct {
	hashes map[SumKey]string
}

// ParseGoSum parses the contents of a go.sum file. Only h1: hashes are
// recognized, matching the go command's current algorithm.
func ParseGoSum(input string) (*GoSumFile, error) {
	sum := &GoSumFile{hashes: map[SumKey]string{}}
	scanner := bufio.NewScanner(strings.NewReader(input))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, &ParseError{File: "go.sum", Line: lineNo,
				Msg: "malformed checksum line (expected module version hash)"}
		}
		name, version, hash := fields[0], fields[1], fields[2]
		if !strings.HasPrefix(hash, "h1:") {
			return nil, &ParseError{File: "go.sum", Line: lineNo, Msg: "unsupported hash algorithm in " + hash}
		}
		key := SumKey{Path: name, Version: version}
		if strings.HasSuffix(version, "/go.mod") {
			key.Version = strings.TrimSuffix(version, "/go.mod")
			key.GoMod = true
		}
		if existing, ok := sum.hashes[key]; ok && existing != hash {
			return nil, &ParseError{File: "go.sum", Line: lineNo,
				Msg: fmt.Sprintf("conflicting hashes for %s@%s", name, version)}
		}
		sum.hashes[key] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sum, nil
}

// Lookup returns the h1: hash for the module zip (or its go.mod when goMod is
// true). The boolean reports whether an entry exists.
func (s *GoSumFile) Lookup(path, version string, goMod bool) (string, bool) {
	if s == nil {
		return "", false
	}
	h, ok := s.hashes[SumKey{Path: path, Version: version, GoMod: goMod}]
	return h, ok
}

// Entries returns a copy of all parsed checksum entries.
func (s *GoSumFile) Entries() map[SumKey]string {
	out := make(map[SumKey]string, len(s.hashes))
	for k, v := range s.hashes {
		out[k] = v
	}
	return out
}
