package netsec

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strconv"
)

// Checkpoint is the three-line body of a transparency-log checkpoint note.
type Checkpoint struct {
	Origin   string
	TreeSize uint64
	Root     []byte
}

// ParseCheckpoint parses a canonical note body:
//
//	origin\n
//	decimal tree size\n
//	base64 root\n
//
// Additional extension lines are rejected so a mirror cannot smuggle extra
// content into the signed body.
func ParseCheckpoint(body []byte) (*Checkpoint, error) {
	if !bytes.HasSuffix(body, []byte("\n")) {
		return nil, errors.New("netsec: checkpoint body must end in newline")
	}

	lines := bytes.Split(body, []byte("\n"))
	// A trailing newline produces one empty final element; the body must hold
	// exactly the three required lines and nothing more.
	if len(lines) != 4 || len(lines[3]) != 0 {
		return nil, errors.New("netsec: checkpoint body must contain exactly three lines")
	}

	origin := lines[0]
	if len(origin) == 0 || bytes.IndexByte(origin, '\n') >= 0 {
		return nil, errors.New("netsec: checkpoint origin must be a single non-empty line")
	}

	sizeText := lines[1]
	if len(sizeText) == 0 || (len(sizeText) > 1 && sizeText[0] == '0') {
		return nil, errors.New("netsec: checkpoint tree size must be canonical decimal")
	}
	size, err := strconv.ParseUint(string(sizeText), 10, 64)
	if err != nil {
		return nil, errors.New("netsec: checkpoint tree size is not a valid unsigned integer")
	}

	rootText := lines[2]
	root, err := base64.StdEncoding.DecodeString(string(rootText))
	if err != nil {
		return nil, errors.New("netsec: checkpoint root must be standard base64")
	}

	return &Checkpoint{Origin: string(origin), TreeSize: size, Root: root}, nil
}

// Marshal renders the checkpoint back into its canonical three-line body.
func (c *Checkpoint) Marshal() []byte {
	var buf bytes.Buffer
	buf.WriteString(c.Origin)
	buf.WriteByte('\n')
	buf.WriteString(strconv.FormatUint(c.TreeSize, 10))
	buf.WriteByte('\n')
	buf.WriteString(base64.StdEncoding.EncodeToString(c.Root))
	buf.WriteByte('\n')
	return buf.Bytes()
}
