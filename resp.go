package netsec

import (
	"bufio"
	"strconv"
)

// ProtocolError describes a malformed RESP frame. Its Error method returns
// the text to send inside a "-ERR ..." reply.
type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string { return e.Message }

// ReadCommand reads one Redis command from r and returns it as a slice of
// bulk strings. It supports RESP2/RESP3 array-of-bulk-string commands and the
// inline command format used by simple clients.
func ReadCommand(r *bufio.Reader) ([][]byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if first != '*' {
		// Inline command: put the first byte back and read the whole line.
		if err := r.UnreadByte(); err != nil {
			return nil, err
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		fields := splitInline(line)
		if len(fields) == 0 {
			return nil, &ProtocolError{Message: "Protocol error: empty inline command"}
		}
		return fields, nil
	}

	argc, err := readLineInt(r)
	if err != nil {
		return nil, err
	}
	if argc <= 0 {
		return nil, &ProtocolError{Message: "Protocol error: invalid multibulk length"}
	}

	args := make([][]byte, argc)
	for i := range args {
		marker, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if marker != '$' {
			return nil, &ProtocolError{Message: "Protocol error: expected '$' bulk string marker"}
		}
		length, err := readLineInt(r)
		if err != nil {
			return nil, err
		}
		if length < 0 {
			return nil, &ProtocolError{Message: "Protocol error: null bulk in command"}
		}
		buf := make([]byte, length)
		if _, err := readFull(r, buf); err != nil {
			return nil, err
		}
		// Consume the trailing CRLF.
		cr, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		lf, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if cr != '\r' || lf != '\n' {
			return nil, &ProtocolError{Message: "Protocol error: expected CRLF after bulk string"}
		}
		args[i] = buf
	}
	return args, nil
}

// WriteError writes a gate error as a RESP error reply on w.
func WriteError(w *bufio.Writer, e *Error) error {
	if _, err := w.WriteString(e.Reply()); err != nil {
		return err
	}
	return w.Flush()
}

func readLineInt(r *bufio.Reader) (int, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return 0, &ProtocolError{Message: "Protocol error: expected CRLF terminator"}
	}
	value, err := strconv.Atoi(line[:len(line)-2])
	if err != nil {
		return 0, &ProtocolError{Message: "Protocol error: invalid integer"}
	}
	return value, nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func splitInline(line string) [][]byte {
	fields := make([][]byte, 0)
	current := make([]byte, 0)
	inToken := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ', '\t', '\r', '\n':
			if inToken {
				fields = append(fields, current)
				current = make([]byte, 0)
				inToken = false
			}
		default:
			current = append(current, line[i])
			inToken = true
		}
	}
	if inToken {
		fields = append(fields, current)
	}
	return fields
}
