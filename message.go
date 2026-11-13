package netsec

import (
	"bytes"
	"fmt"
	"io"
)

// HeaderField is one message header field. Raw contains the complete field,
// including its name, colon, value, and any folding, but excludes the final
// line ending. Keeping the original bytes is important for DKIM header
// canonicalization.
type HeaderField struct {
	Name string
	Raw  []byte
}

// Message is a parsed internet message. Header fields retain their original
// order and raw form so they can be canonicalized exactly as received.
type Message struct {
	Header []HeaderField
	Body   []byte
}

// ParseMessage parses an RFC 5322 message. The header/body separator may use
// CRLF or LF line endings.
func ParseMessage(data []byte) (*Message, error) {
	msg := &Message{}
	pos := 0
	fieldStart := -1
	fieldEnd := -1

	finishField := func() error {
		if fieldStart < 0 {
			return nil
		}
		raw := data[fieldStart:fieldEnd]
		colon := bytes.IndexByte(raw, ':')
		if colon <= 0 {
			return fmt.Errorf("netsec: header field without a colon")
		}
		name := string(bytes.TrimSpace(raw[:colon]))
		if name == "" {
			return fmt.Errorf("netsec: empty header field name")
		}
		msg.Header = append(msg.Header, HeaderField{Name: name, Raw: raw})
		fieldStart, fieldEnd = -1, -1
		return nil
	}

	for pos < len(data) {
		textEnd, next := lineBounds(data, pos)
		line := data[pos:textEnd]
		if len(line) == 0 {
			if err := finishField(); err != nil {
				return nil, err
			}
			msg.Body = data[next:]
			return msg, nil
		}

		if line[0] == ' ' || line[0] == '\t' {
			if fieldStart < 0 {
				return nil, fmt.Errorf("netsec: header continuation without a field")
			}
			fieldEnd = textEnd
			pos = next
			continue
		}

		if err := finishField(); err != nil {
			return nil, err
		}
		fieldStart = pos
		fieldEnd = textEnd
		pos = next
	}

	if err := finishField(); err != nil {
		return nil, err
	}
	return msg, nil
}

// ReadMessage reads and parses an RFC 5322 message.
func ReadMessage(r io.Reader) (*Message, error) {
	if r == nil {
		return nil, fmt.Errorf("netsec: nil message reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("netsec: read message: %w", err)
	}
	return ParseMessage(data)
}

func lineBounds(data []byte, pos int) (textEnd, next int) {
	for i := pos; i < len(data); i++ {
		switch data[i] {
		case '\n':
			return i, i + 1
		case '\r':
			if i+1 < len(data) && data[i+1] == '\n' {
				return i, i + 2
			}
			return i, i + 1
		}
	}
	return len(data), len(data)
}

func fieldValue(raw []byte) []byte {
	colon := bytes.IndexByte(raw, ':')
	if colon < 0 {
		return nil
	}
	return raw[colon+1:]
}

func fieldName(raw []byte) string {
	colon := bytes.IndexByte(raw, ':')
	if colon < 0 {
		return ""
	}
	return string(bytes.TrimSpace(raw[:colon]))
}
