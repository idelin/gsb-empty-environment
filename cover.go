package testsel

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
)

// CoverBlock is one entry of a "go test -coverprofile" file.
type CoverBlock struct {
	File               string // as written in the profile, e.g. "mod/pkg/foo.go"
	StartLine, EndLine int
	NumStmt, Count     int
}

// ParseCoverProfile parses a coverage profile produced by go test.
func ParseCoverProfile(path string) ([]CoverBlock, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var blocks []CoverBlock
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		b, err := parseCoverLine(line)
		if err != nil {
			return nil, fmt.Errorf("testsel: %s:%d: %v", path, i+1, err)
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// Line format: file.go:startLine.startCol,endLine.endCol numStmt count
func parseCoverLine(line string) (CoverBlock, error) {
	var b CoverBlock
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return b, fmt.Errorf("malformed cover line %q", line)
	}
	colon := strings.LastIndex(fields[0], ":")
	if colon < 0 {
		return b, fmt.Errorf("malformed cover line %q", line)
	}
	b.File = fields[0][:colon]
	span := strings.SplitN(fields[0][colon+1:], ",", 2)
	if len(span) != 2 {
		return b, fmt.Errorf("malformed cover span %q", fields[0])
	}
	var err error
	if b.StartLine, err = lineOf(span[0]); err != nil {
		return b, err
	}
	if b.EndLine, err = lineOf(span[1]); err != nil {
		return b, err
	}
	if b.NumStmt, err = strconv.Atoi(fields[1]); err != nil {
		return b, err
	}
	if b.Count, err = strconv.Atoi(fields[2]); err != nil {
		return b, err
	}
	return b, nil
}

func lineOf(pos string) (int, error) {
	dot := strings.Index(pos, ".")
	if dot < 0 {
		return 0, fmt.Errorf("malformed position %q", pos)
	}
	return strconv.Atoi(pos[:dot])
}

// MapBlocks attributes covered blocks to functions. Only blocks with a
// non-zero execution count contribute.
func (idx *FuncIndex) MapBlocks(blocks []CoverBlock) map[FuncID]bool {
	out := map[FuncID]bool{}
	for _, b := range blocks {
		if b.Count == 0 {
			continue
		}
		if id, ok := idx.lookup(b.File, b.StartLine); ok {
			out[id] = true
		}
	}
	return out
}

func (idx *FuncIndex) lookup(file string, line int) (FuncID, bool) {
	spans := idx.findFile(file)
	if spans == nil {
		return "", false
	}
	for _, sp := range spans {
		if line >= sp.startLine && line <= sp.endLn {
			return sp.id, true
		}
	}
	return "", false
}

// findFile resolves a profile path to recorded spans, tolerating paths
// rooted differently than the analyzed module (absolute paths, GOPATH
// style, ...).
func (idx *FuncIndex) findFile(file string) []funcSpan {
	if spans, ok := idx.files[file]; ok {
		return spans
	}
	for key, spans := range idx.files {
		if strings.HasSuffix(file, key) {
			return spans
		}
	}
	base := filepath.Base(file)
	var match []funcSpan
	for key, spans := range idx.files {
		if filepath.Base(key) == base {
			if match != nil {
				return nil // ambiguous base name
			}
			match = spans
		}
	}
	return match
}
