package netsec

import (
	"bytes"
	"fmt"
)

// writeFoldedHeader writes exactly one header field. Tokens are already
// validated and encoded; folding only inserts legal CRLF + SP whitespace
// between tokens. Callers cannot supply a field name or raw header text.
func writeFoldedHeader(buf *bytes.Buffer, name string, tokens []string) error {
	if !isHeaderFieldName(name) {
		return fmt.Errorf("%w: bad field name", ErrInvalidHeader)
	}
	if len(tokens) == 0 {
		return fmt.Errorf("%w: %s has no value", ErrInvalidHeader, name)
	}

	buf.WriteString(name)
	buf.WriteByte(':')
	lineLength := len(name) + 1
	for i, token := range tokens {
		if err := validateASCIIHeaderToken(token); err != nil {
			return err
		}
		separatorLength := 1
		if lineLength+separatorLength+len(token) > maxHeaderLine &&
			(lineLength > len(name)+1 || i == 0) {
			buf.WriteString("\r\n ")
			lineLength = 1
			buf.WriteString(token)
			lineLength += len(token)
			continue
		}
		buf.WriteByte(' ')
		buf.WriteString(token)
		lineLength += separatorLength + len(token)
	}
	buf.WriteString("\r\n")
	return nil
}

func isHeaderFieldName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if !((b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-') {
			return false
		}
	}
	return true
}

func addressTokens(addresses []Address) ([]string, error) {
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w: empty address list", ErrInvalidHeader)
	}
	tokens := make([]string, 0, len(addresses)*3)
	for i, address := range addresses {
		item, err := address.headerTokens()
		if err != nil {
			return nil, err
		}
		if i < len(addresses)-1 {
			item[len(item)-1] += ","
		}
		tokens = append(tokens, item...)
	}
	return tokens, nil
}
