package netsec

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// stanzaReader reads age stanzas while enforcing a per-response byte budget.
// The budget counts every wire byte consumed from the underlying stream and is
// reset between plugin responses, which makes the 64KiB limit exact rather
// than dependent on internal buffering.
type stanzaReader struct {
	scanner *bufio.Reader
	limit   int
	used    int
}

func newStanzaReader(in io.Reader) *stanzaReader {
	return &stanzaReader{scanner: bufio.NewReader(in)}
}

// resetLimit starts a new response budget of limit wire bytes.
func (r *stanzaReader) resetLimit(limit int) {
	r.limit = limit
	r.used = 0
}

// readLine returns one logical line without its trailing newline, counting the
// raw bytes consumed (newline included) against the response budget.
func (r *stanzaReader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.scanner.ReadSlice('\n')
		r.used += len(chunk)
		if r.limit > 0 && r.used > r.limit {
			return nil, fmt.Errorf("%w: response is larger than %d bytes", ErrResponseTooLarge, MaxPluginResponse)
		}
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return trimCR(line), nil
			}
			return nil, err
		}
		return trimCR(line[:len(line)-1]), nil
	}
}

func trimCR(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\r' {
		return line[:len(line)-1]
	}
	return line
}

// ReadStanza reads one complete stanza, decoding its base64 body.
func (r *stanzaReader) ReadStanza() (*Stanza, error) {
	header, err := r.readLine()
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(header), " ")
	if len(fields) == 0 || fields[0] == "" {
		return nil, fmt.Errorf("%w: empty stanza header", ErrProtocol)
	}
	stanza := &Stanza{Type: fields[0], Args: fields[1:]}

	var body []byte
	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("%w: truncated stanza body", ErrProtocol)
			}
			return nil, err
		}
		if len(line) == 0 {
			stanza.Body = body
			return stanza, nil
		}

		decoded, decodeErr := base64.StdEncoding.DecodeString(string(line))
		if decodeErr != nil {
			return nil, fmt.Errorf("%w: invalid stanza body encoding: %v", ErrProtocol, decodeErr)
		}
		body = append(body, decoded...)
	}
}

// stanzaWriter serializes stanzas using the age wire format: a space-joined
// header followed by base64 body lines wrapped at 64 columns and a blank
// terminating line.
type stanzaWriter struct {
	w io.Writer
}

func (w *stanzaWriter) WriteStanza(stanza *Stanza) error {
	if stanza.Type == "" || strings.ContainsAny(stanza.Type, " \t\r\n") {
		return fmt.Errorf("%w: invalid stanza type %q", ErrProtocol, stanza.Type)
	}

	var builder strings.Builder
	builder.WriteString(stanza.Type)
	for _, arg := range stanza.Args {
		if arg == "" || strings.ContainsAny(arg, " \t\r\n") {
			return fmt.Errorf("%w: invalid stanza argument %q", ErrProtocol, arg)
		}
		builder.WriteByte(' ')
		builder.WriteString(arg)
	}
	builder.WriteByte('\n')

	if len(stanza.Body) > 0 {
		encoded := base64.StdEncoding.EncodeToString(stanza.Body)
		for len(encoded) > 0 {
			chunk := encoded
			if len(encoded) > 64 {
				chunk = encoded[:64]
			}
			builder.WriteString(chunk)
			builder.WriteByte('\n')
			encoded = encoded[len(chunk):]
		}
	}
	builder.WriteByte('\n')

	_, err := io.WriteString(w.w, builder.String())
	return err
}
