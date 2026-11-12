package netsec

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// ReadResponse reads and filters one response for req. Foreign VALUE bodies
// are consumed in full and emitted only when their key begins with this
// tenant's prefix. The returned bytes can be written to the client unchanged.
// Pass a *bufio.Reader when calling this repeatedly on the same stream.
func (n *Normalizer) ReadResponse(r io.Reader, req *Request) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrMalformedResponse)
	}
	if req.ResponseKind == ResponseNone {
		return nil, nil
	}
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	if req.Protocol == ProtocolBinary {
		return n.readBinaryResponse(br, req)
	}
	if req.Protocol != ProtocolText {
		return nil, fmt.Errorf("%w: unknown request protocol", ErrMalformedResponse)
	}
	if req.ResponseKind == ResponseSingle {
		return n.readSingleTextResponse(br)
	}
	return n.readMultiTextResponse(br, req)
}

// ReadResponseFrom is a one-shot convenience wrapper around ReadResponse. It
// must not be called repeatedly with the same unbuffered reader because its
// temporary buffer could consume bytes belonging to a later response.
func (n *Normalizer) ReadResponseFrom(r io.Reader, req *Request) ([]byte, error) {
	return n.ReadResponse(r, req)
}

func (n *Normalizer) readSingleTextResponse(r *bufio.Reader) ([]byte, error) {
	line, err := readWireLine(r, n.cfg.MaxCommandLineLength, "response")
	if err != nil {
		return nil, mapResponseLineError(err)
	}
	if err := validateResponseLine(line); err != nil {
		return nil, err
	}
	if bytes.HasPrefix(line, []byte("VALUE ")) {
		return nil, fmt.Errorf("%w: unsolicited VALUE response", ErrMalformedResponse)
	}
	out := append(copyBytes(line), '\r', '\n')
	if len(out) > n.cfg.MaxResponseLength {
		return nil, fmt.Errorf("%w: response line exceeds %d bytes",
			ErrTooLarge, n.cfg.MaxResponseLength)
	}
	return out, nil
}

func (n *Normalizer) readMultiTextResponse(r *bufio.Reader, req *Request) ([]byte, error) {
	var out []byte
	for {
		line, err := readWireLine(r, n.cfg.MaxCommandLineLength, "response")
		if err != nil {
			return nil, mapResponseLineError(err)
		}
		if err := validateResponseLine(line); err != nil {
			return nil, err
		}

		if bytes.Equal(line, []byte("END")) {
			return appendResponse(out, []byte("END\r\n"), n.cfg.MaxResponseLength)
		}
		if bytes.HasPrefix(line, []byte("VALUE ")) {
			entry, value, keep, err := n.readValueResponse(r, line)
			if err != nil {
				return nil, err
			}
			if keep {
				out, err = appendResponse(out, entry, n.cfg.MaxResponseLength)
				if err != nil {
					return nil, err
				}
				out, err = appendResponse(out, value, n.cfg.MaxResponseLength)
				if err != nil {
					return nil, err
				}
			}
			continue
		}
		if isStatusErrorLine(line) {
			return appendResponse(out, append(copyBytes(line), '\r', '\n'), n.cfg.MaxResponseLength)
		}
		if req.Command == "stats" {
			if bytes.HasPrefix(line, []byte("STAT ")) {
				out, err = appendResponse(out, append(copyBytes(line), '\r', '\n'), n.cfg.MaxResponseLength)
				if err != nil {
					return nil, err
				}
				continue
			}
			if bytes.HasPrefix(line, []byte("ITEM ")) {
				filtered, keep, err := n.filterItemResponse(line)
				if err != nil {
					return nil, err
				}
				if keep {
					out, err = appendResponse(out, filtered, n.cfg.MaxResponseLength)
					if err != nil {
						return nil, err
					}
				}
				continue
			}
		}
		return nil, fmt.Errorf("%w: unexpected response line %q", ErrMalformedResponse, line)
	}
}

func (n *Normalizer) readValueResponse(r *bufio.Reader, line []byte) ([]byte, []byte, bool, error) {
	fields, err := splitCommandLine(line, 0)
	if err != nil {
		return nil, nil, false, fmt.Errorf("%w: invalid VALUE header: %v",
			ErrMalformedResponse, err)
	}
	if len(fields) != 4 && len(fields) != 5 {
		return nil, nil, false, fmt.Errorf("%w: VALUE header has %d fields",
			ErrMalformedResponse, len(fields))
	}
	if !bytes.Equal(fields[0], []byte("VALUE")) {
		return nil, nil, false, fmt.Errorf("%w: expected VALUE header", ErrMalformedResponse)
	}
	flags, err := parseUint32Token(fields[2])
	if err != nil {
		return nil, nil, false, fmt.Errorf("%w: invalid VALUE flags", ErrMalformedResponse)
	}
	valueLen, err := parseUintToken(fields[3])
	if err != nil {
		return nil, nil, false, fmt.Errorf("%w: invalid VALUE length", ErrMalformedResponse)
	}
	if valueLen > uint64(n.cfg.MaxValueLength) {
		return nil, nil, false, fmt.Errorf("%w: VALUE length %d exceeds %d",
			ErrTooLarge, valueLen, n.cfg.MaxValueLength)
	}
	var casID uint64
	if len(fields) == 5 {
		casID, err = parseUintToken(fields[4])
		if err != nil {
			return nil, nil, false, fmt.Errorf("%w: invalid VALUE CAS", ErrMalformedResponse)
		}
	}

	value := make([]byte, int(valueLen))
	if _, err := io.ReadFull(r, value); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, nil, false, fmt.Errorf("%w: VALUE body ended early", ErrTruncated)
		}
		return nil, nil, false, err
	}
	var crlf [2]byte
	if _, err := io.ReadFull(r, crlf[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, nil, false, fmt.Errorf("%w: VALUE body has no CRLF", ErrTruncated)
		}
		return nil, nil, false, err
	}
	if crlf[0] != '\r' || crlf[1] != '\n' {
		return nil, nil, false, fmt.Errorf("%w: VALUE body length mismatch", ErrMalformedResponse)
	}

	stripped, tenant := n.StripKey(fields[1])
	if !tenant {
		return nil, nil, false, nil
	}
	header := [][]byte{
		[]byte("VALUE"),
		stripped,
		[]byte(strconv.FormatUint(uint64(flags), 10)),
		[]byte(strconv.FormatUint(valueLen, 10)),
	}
	if len(fields) == 5 {
		header = append(header, []byte(strconv.FormatUint(casID, 10)))
	}
	entry := buildTextLine(header, nil)
	block := make([]byte, 0, len(value)+2)
	block = append(block, value...)
	block = append(block, '\r', '\n')
	return entry, block, true, nil
}

func (n *Normalizer) filterItemResponse(line []byte) ([]byte, bool, error) {
	fields, err := splitCommandLine(line, 0)
	if err != nil || len(fields) < 2 {
		return nil, false, fmt.Errorf("%w: invalid ITEM response", ErrMalformedResponse)
	}
	stripped, tenant := n.StripKey(fields[1])
	if !tenant {
		return nil, false, nil
	}
	fields[1] = stripped
	return buildTextLine(fields, nil), true, nil
}

func validateResponseLine(line []byte) error {
	for i, b := range line {
		if b < 0x20 || b == 0x7f {
			return fmt.Errorf("%w: control byte %#x at offset %d", ErrMalformedResponse, b, i)
		}
	}
	return nil
}

func isStatusErrorLine(line []byte) bool {
	return bytes.Equal(line, []byte("ERROR")) ||
		bytes.HasPrefix(line, []byte("CLIENT_ERROR ")) ||
		bytes.HasPrefix(line, []byte("SERVER_ERROR "))
}

func mapResponseLineError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrMalformedResponse, err)
}

func appendResponse(dst, chunk []byte, max int) ([]byte, error) {
	if len(dst)+len(chunk) > max {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrTooLarge, max)
	}
	return append(dst, chunk...), nil
}
