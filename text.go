package netsec

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

func (s *Session) readTextRequest() (*Request, error) {
	line, err := readWireLine(s.r, s.n.cfg.MaxCommandLineLength, "request")
	if err != nil {
		return nil, err
	}
	fields, err := splitCommandLine(line, s.n.cfg.MaxKeysPerRequest)
	if err != nil {
		return nil, err
	}

	command := string(bytes.ToLower(fields[0]))
	args := fields[1:]
	switch command {
	case "set", "add", "replace", "append", "prepend":
		return s.parseStorage(command, args, false)
	case "cas":
		return s.parseStorage(command, args, true)
	case "get", "gets":
		return s.parseRetrieval(command, args)
	case "gat", "gats":
		return s.parseGat(command, args)
	case "delete":
		return s.parseDelete(args)
	case "incr", "decr":
		return s.parseIncrDecr(command, args)
	case "touch":
		return s.parseTouch(args)
	case "stats":
		return s.parseStats(args)
	case "version":
		if len(args) != 0 {
			return nil, fmt.Errorf("%w: version takes no arguments", ErrMalformedRequest)
		}
		return &Request{
			Protocol:     ProtocolText,
			Command:      command,
			ResponseKind: ResponseSingle,
			Bytes:        buildTextLine([][]byte{[]byte(command)}, nil),
		}, nil
	case "quit":
		if len(args) != 0 {
			return nil, fmt.Errorf("%w: quit takes no arguments", ErrMalformedRequest)
		}
		return &Request{
			Protocol:     ProtocolText,
			Command:      command,
			CloseAfter:   true,
			ResponseKind: ResponseNone,
			Bytes:        buildTextLine([][]byte{[]byte(command)}, nil),
		}, nil
	case "flush_all":
		return s.parseFlushAll(args)
	case "verbosity":
		return s.parseVerbosity(args)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedCommand, command)
	}
}

func readWireLine(r *bufio.Reader, max int, kind string) ([]byte, error) {
	var line []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				if len(line) == 0 {
					return nil, io.EOF
				}
				return nil, fmt.Errorf("%w: %s line has no CRLF", ErrTruncated, kind)
			}
			return nil, err
		}
		if b == '\n' {
			if len(line) == 0 || line[len(line)-1] != '\r' {
				return nil, fmt.Errorf("%w: %s line must end with CRLF", ErrMalformedRequest, kind)
			}
			line = line[:len(line)-1]
			if len(line) > max {
				return nil, fmt.Errorf("%w: %s line exceeds %d bytes", ErrTooLarge, kind, max)
			}
			return line, nil
		}
		// One extra byte is allowed so the final CR does not count against
		// the content limit.
		if len(line) >= max+1 {
			return nil, fmt.Errorf("%w: %s line exceeds %d bytes", ErrTooLarge, kind, max)
		}
		line = append(line, b)
	}
}

func splitCommandLine(line []byte, maxFields int) ([][]byte, error) {
	if len(line) == 0 {
		return nil, fmt.Errorf("%w: empty command line", ErrMalformedRequest)
	}
	for i, b := range line {
		if b < 0x20 || b == 0x7f {
			return nil, fmt.Errorf("%w: control byte %#x at offset %d", ErrMalformedRequest, b, i)
		}
	}
	fields := make([][]byte, 0, 8)
	start := -1
	for i, b := range line {
		if b == ' ' {
			if start >= 0 {
				fields = append(fields, line[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, line[start:])
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: empty command line", ErrMalformedRequest)
	}
	if maxFields > 0 && len(fields)-1 > maxFields &&
		string(bytes.ToLower(fields[0])) != "stats" {
		return nil, fmt.Errorf("%w: %d fields exceeds limit %d",
			ErrTooLarge, len(fields)-1, maxFields)
	}
	return fields, nil
}

func buildTextLine(fields [][]byte, tail []byte) []byte {
	size := 2
	for _, f := range fields {
		size += len(f) + 1
	}
	size += len(tail)
	out := make([]byte, 0, size)
	for i, f := range fields {
		if i != 0 {
			out = append(out, ' ')
		}
		out = append(out, f...)
	}
	if len(tail) != 0 {
		if len(fields) != 0 {
			out = append(out, ' ')
		}
		out = append(out, tail...)
	}
	out = append(out, '\r', '\n')
	return out
}

func (s *Session) parseStorage(command string, args [][]byte, cas bool) (*Request, error) {
	want := 4
	if cas {
		want = 5
	}
	if len(args) != want && len(args) != want+1 {
		return nil, fmt.Errorf("%w: %s takes %d or %d arguments",
			ErrMalformedRequest, command, want, want+1)
	}
	noreply := false
	if len(args) == want+1 {
		if !isNoReply(args[want]) {
			return nil, fmt.Errorf("%w: unexpected %s argument %q",
				ErrMalformedRequest, command, args[want])
		}
		noreply = true
	}

	key, err := s.n.PrefixKey(args[0])
	if err != nil {
		return nil, err
	}
	flags, err := parseUint32Token(args[1])
	if err != nil {
		return nil, err
	}
	exptime, err := parseExptimeToken(args[2])
	if err != nil {
		return nil, err
	}
	valueLen, err := parseUintToken(args[3])
	if err != nil {
		return nil, err
	}
	if valueLen > uint64(s.n.cfg.MaxValueLength) {
		return nil, fmt.Errorf("%w: declared value length %d exceeds %d",
			ErrTooLarge, valueLen, s.n.cfg.MaxValueLength)
	}

	fields := [][]byte{
		[]byte(command),
		key,
		[]byte(strconv.FormatUint(uint64(flags), 10)),
		[]byte(strconv.FormatInt(exptime, 10)),
		[]byte(strconv.FormatUint(valueLen, 10)),
	}
	if cas {
		casID, err := parseUintToken(args[4])
		if err != nil {
			return nil, err
		}
		fields = append(fields, []byte(strconv.FormatUint(casID, 10)))
	}
	if noreply {
		fields = append(fields, []byte("noreply"))
	}

	value, err := s.readStorageValue(int(valueLen))
	if err != nil {
		return nil, err
	}
	line := buildTextLine(fields, nil)
	out := make([]byte, 0, len(line)+len(value)+2)
	out = append(out, line...)
	out = append(out, value...)
	out = append(out, '\r', '\n')

	kind := ResponseSingle
	if noreply {
		kind = ResponseNone
	}
	return &Request{
		Protocol:     ProtocolText,
		Command:      command,
		Keys:         [][]byte{copyBytes(args[0])},
		Value:        value,
		NoReply:      noreply,
		ResponseKind: kind,
		Bytes:        out,
	}, nil
}

func (s *Session) readStorageValue(n int) ([]byte, error) {
	value := make([]byte, n)
	if _, err := io.ReadFull(s.r, value); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("%w: value ended after %d bytes", ErrTruncated, n)
		}
		return nil, err
	}
	var crlf [2]byte
	if _, err := io.ReadFull(s.r, crlf[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("%w: data block is not followed by CRLF", ErrTruncated)
		}
		return nil, err
	}
	if crlf[0] != '\r' || crlf[1] != '\n' {
		return nil, fmt.Errorf("%w: declared %d bytes, got trailing %q",
			ErrMalformedBody, n, crlf[:])
	}
	return value, nil
}

func (s *Session) parseRetrieval(command string, args [][]byte) (*Request, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: %s requires at least one key", ErrMalformedRequest, command)
	}
	keys, err := s.prefixKeys(args)
	if err != nil {
		return nil, err
	}
	fields := append([][]byte{[]byte(command)}, keys...)
	return &Request{
		Protocol:     ProtocolText,
		Command:      command,
		Keys:         copyKeys(args),
		ResponseKind: ResponseMulti,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseGat(command string, args [][]byte) (*Request, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%w: %s requires an expiration and at least one key",
			ErrMalformedRequest, command)
	}
	exptime, err := parseExptimeToken(args[0])
	if err != nil {
		return nil, err
	}
	keys, err := s.prefixKeys(args[1:])
	if err != nil {
		return nil, err
	}
	fields := [][]byte{
		[]byte(command),
		[]byte(strconv.FormatInt(exptime, 10)),
	}
	fields = append(fields, keys...)
	return &Request{
		Protocol:     ProtocolText,
		Command:      command,
		Keys:         copyKeys(args[1:]),
		ResponseKind: ResponseMulti,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseDelete(args [][]byte) (*Request, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%w: delete takes a key and optional noreply", ErrMalformedRequest)
	}
	noreply := false
	if len(args) == 2 {
		if !isNoReply(args[1]) {
			return nil, fmt.Errorf("%w: unexpected delete argument %q", ErrMalformedRequest, args[1])
		}
		noreply = true
	}
	key, err := s.n.PrefixKey(args[0])
	if err != nil {
		return nil, err
	}
	fields := [][]byte{[]byte("delete"), key}
	if noreply {
		fields = append(fields, []byte("noreply"))
	}
	kind := ResponseSingle
	if noreply {
		kind = ResponseNone
	}
	return &Request{
		Protocol:     ProtocolText,
		Command:      "delete",
		Keys:         [][]byte{copyBytes(args[0])},
		NoReply:      noreply,
		ResponseKind: kind,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseIncrDecr(command string, args [][]byte) (*Request, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%w: %s takes key, delta, optional noreply",
			ErrMalformedRequest, command)
	}
	noreply := false
	if len(args) == 3 {
		if !isNoReply(args[2]) {
			return nil, fmt.Errorf("%w: unexpected %s argument %q",
				ErrMalformedRequest, command, args[2])
		}
		noreply = true
	}
	key, err := s.n.PrefixKey(args[0])
	if err != nil {
		return nil, err
	}
	delta, err := parseUintToken(args[1])
	if err != nil {
		return nil, err
	}
	fields := [][]byte{
		[]byte(command),
		key,
		[]byte(strconv.FormatUint(delta, 10)),
	}
	if noreply {
		fields = append(fields, []byte("noreply"))
	}
	kind := ResponseSingle
	if noreply {
		kind = ResponseNone
	}
	return &Request{
		Protocol:     ProtocolText,
		Command:      command,
		Keys:         [][]byte{copyBytes(args[0])},
		NoReply:      noreply,
		ResponseKind: kind,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseTouch(args [][]byte) (*Request, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%w: touch takes key, expiration, optional noreply",
			ErrMalformedRequest)
	}
	noreply := false
	if len(args) == 3 {
		if !isNoReply(args[2]) {
			return nil, fmt.Errorf("%w: unexpected touch argument %q", ErrMalformedRequest, args[2])
		}
		noreply = true
	}
	key, err := s.n.PrefixKey(args[0])
	if err != nil {
		return nil, err
	}
	exptime, err := parseExptimeToken(args[1])
	if err != nil {
		return nil, err
	}
	fields := [][]byte{
		[]byte("touch"),
		key,
		[]byte(strconv.FormatInt(exptime, 10)),
	}
	if noreply {
		fields = append(fields, []byte("noreply"))
	}
	kind := ResponseSingle
	if noreply {
		kind = ResponseNone
	}
	return &Request{
		Protocol:     ProtocolText,
		Command:      "touch",
		Keys:         [][]byte{copyBytes(args[0])},
		NoReply:      noreply,
		ResponseKind: kind,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseStats(args [][]byte) (*Request, error) {
	fields := append([][]byte{[]byte("stats")}, args...)
	return &Request{
		Protocol:     ProtocolText,
		Command:      "stats",
		ResponseKind: ResponseMulti,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseFlushAll(args [][]byte) (*Request, error) {
	if len(args) > 2 {
		return nil, fmt.Errorf("%w: flush_all takes an optional delay and noreply",
			ErrMalformedRequest)
	}
	noreply := false
	fields := [][]byte{[]byte("flush_all")}
	switch len(args) {
	case 1:
		if isNoReply(args[0]) {
			noreply = true
			fields = append(fields, []byte("noreply"))
		} else {
			delay, err := parseUint32Token(args[0])
			if err != nil {
				return nil, err
			}
			fields = append(fields, []byte(strconv.FormatUint(uint64(delay), 10)))
		}
	case 2:
		delay, err := parseUint32Token(args[0])
		if err != nil {
			return nil, err
		}
		if !isNoReply(args[1]) {
			return nil, fmt.Errorf("%w: unexpected flush_all argument %q",
				ErrMalformedRequest, args[1])
		}
		noreply = true
		fields = append(fields,
			[]byte(strconv.FormatUint(uint64(delay), 10)),
			[]byte("noreply"))
	}
	kind := ResponseSingle
	if noreply {
		kind = ResponseNone
	}
	return &Request{
		Protocol:     ProtocolText,
		Command:      "flush_all",
		NoReply:      noreply,
		ResponseKind: kind,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) parseVerbosity(args [][]byte) (*Request, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%w: verbosity takes a level and optional noreply",
			ErrMalformedRequest)
	}
	level, err := parseUint32Token(args[0])
	if err != nil {
		return nil, err
	}
	noreply := false
	fields := [][]byte{
		[]byte("verbosity"),
		[]byte(strconv.FormatUint(uint64(level), 10)),
	}
	if len(args) == 2 {
		if !isNoReply(args[1]) {
			return nil, fmt.Errorf("%w: unexpected verbosity argument %q",
				ErrMalformedRequest, args[1])
		}
		noreply = true
		fields = append(fields, []byte("noreply"))
	}
	kind := ResponseSingle
	if noreply {
		kind = ResponseNone
	}
	return &Request{
		Protocol:     ProtocolText,
		Command:      "verbosity",
		NoReply:      noreply,
		ResponseKind: kind,
		Bytes:        buildTextLine(fields, nil),
	}, nil
}

func (s *Session) prefixKeys(keys [][]byte) ([][]byte, error) {
	out := make([][]byte, len(keys))
	for i, key := range keys {
		prefixed, err := s.n.PrefixKey(key)
		if err != nil {
			return nil, err
		}
		out[i] = prefixed
	}
	return out, nil
}

func copyKeys(keys [][]byte) [][]byte {
	out := make([][]byte, len(keys))
	for i, key := range keys {
		out[i] = copyBytes(key)
	}
	return out
}

func copyBytes(b []byte) []byte {
	return append([]byte(nil), b...)
}
