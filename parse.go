package pgoutput

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// reader is a bounds-checked big-endian cursor over a pgoutput payload.
type reader struct {
	buf []byte
	pos int
}

func (r *reader) remaining() int { return len(r.buf) - r.pos }

func (r *reader) byte() (byte, error) {
	if r.remaining() < 1 {
		return 0, errors.New("pgoutput: truncated message (byte)")
	}
	v := r.buf[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) uint16() (uint16, error) {
	if r.remaining() < 2 {
		return 0, errors.New("pgoutput: truncated message (uint16)")
	}
	v := binary.BigEndian.Uint16(r.buf[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *reader) uint32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, errors.New("pgoutput: truncated message (uint32)")
	}
	v := binary.BigEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) int32() (int32, error) {
	v, err := r.uint32()
	return int32(v), err
}

func (r *reader) uint64() (uint64, error) {
	if r.remaining() < 8 {
		return 0, errors.New("pgoutput: truncated message (uint64)")
	}
	v := binary.BigEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

func (r *reader) cstring() (string, error) {
	start := r.pos
	for r.pos < len(r.buf) && r.buf[r.pos] != 0 {
		r.pos++
	}
	if r.pos >= len(r.buf) {
		return "", errors.New("pgoutput: truncated message (cstring)")
	}
	s := string(r.buf[start:r.pos])
	r.pos++ // skip NUL
	return s, nil
}

func (r *reader) bytes16() ([]byte, error) {
	n, err := r.uint16()
	if err != nil {
		return nil, err
	}
	if r.remaining() < int(n) {
		return nil, errors.New("pgoutput: truncated message (bytea16)")
	}
	v := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return v, nil
}

func (r *reader) bytes32() ([]byte, error) {
	n, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if r.remaining() < int(n) {
		return nil, errors.New("pgoutput: truncated message (bytea32)")
	}
	v := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return v, nil
}

// Parse decodes one pgoutput message. The argument may either be the raw
// payload (starting with the message type byte) or a complete CopyData
// message ('w' wrapper) as delivered by the replication stream.
func Parse(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, errors.New("pgoutput: empty message")
	}
	payload := data
	if data[0] == 'w' {
		var err error
		payload, err = unwrapCopyData(data)
		if err != nil {
			return nil, err
		}
	}
	r := &reader{buf: payload}
	typ, err := r.byte()
	if err != nil {
		return nil, err
	}
	var msg any
	switch typ {
	case 'B':
		msg, err = parseBegin(r)
	case 'C':
		msg, err = parseCommit(r)
	case 'O':
		msg, err = parseOrigin(r)
	case 'R':
		msg, err = parseRelation(r)
	case 'Y':
		msg, err = parseType(r)
	case 'I':
		msg, err = parseInsert(r)
	case 'U':
		msg, err = parseUpdate(r)
	case 'D':
		msg, err = parseDelete(r)
	case 'T':
		msg, err = parseTruncate(r)
	case 'M':
		msg, err = parseLogicalMessage(r, true)
	case 'm':
		msg, err = parseLogicalMessage(r, false)
	case 'S':
		msg, err = parseStreamStart(r)
	case 'E':
		msg = StreamStop{}
	case 'c':
		msg, err = parseStreamCommit(r)
	case 'A':
		msg, err = parseStreamAbort(r)
	case 'k':
		msg, err = parseKeepalive(r)
	default:
		return nil, fmt.Errorf("pgoutput: unknown message type %q", typ)
	}
	if err != nil {
		return nil, err
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("pgoutput: %d trailing byte(s) after message type %q", r.remaining(), typ)
	}
	return msg, nil
}

// unwrapCopyData strips a CopyData ('w') frame header: 'w' + uint32 length,
// returning the inner pgoutput payload.
func unwrapCopyData(data []byte) ([]byte, error) {
	if len(data) < 5 {
		return nil, errors.New("pgoutput: truncated CopyData frame")
	}
	frameLen := binary.BigEndian.Uint32(data[1:5])
	if int(frameLen) != len(data)-1 {
		return nil, fmt.Errorf("pgoutput: CopyData length mismatch: header %d, actual %d", frameLen, len(data)-1)
	}
	return data[5:], nil
}

func parseBegin(r *reader) (Begin, error) {
	lsn, err := r.uint64()
	if err != nil {
		return Begin{}, err
	}
	micros, err := r.uint64()
	if err != nil {
		return Begin{}, err
	}
	xid, err := r.uint32()
	if err != nil {
		return Begin{}, err
	}
	return Begin{FinalLSN: LSN(lsn), CommitTime: microsToTime(int64(micros)), XID: xid}, nil
}

func parseCommit(r *reader) (Commit, error) {
	flags, err := r.byte()
	if err != nil {
		return Commit{}, err
	}
	commitLSN, err := r.uint64()
	if err != nil {
		return Commit{}, err
	}
	endLSN, err := r.uint64()
	if err != nil {
		return Commit{}, err
	}
	micros, err := r.uint64()
	if err != nil {
		return Commit{}, err
	}
	return Commit{
		Flags:      flags,
		CommitLSN:  LSN(commitLSN),
		EndLSN:     LSN(endLSN),
		CommitTime: microsToTime(int64(micros)),
	}, nil
}

func parseOrigin(r *reader) (Origin, error) {
	lsn, err := r.uint64()
	if err != nil {
		return Origin{}, err
	}
	name, err := r.cstring()
	if err != nil {
		return Origin{}, err
	}
	return Origin{LSN: LSN(lsn), Name: name}, nil
}
