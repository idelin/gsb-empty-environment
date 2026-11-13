// Package netsec adjudicates message address fields against header
// injection: a single address field must not contain CR or LF.
package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/textproto"
	"strings"
)

// ErrNewlineInAddressField reports an address field whose value
// contains a CR or LF, which would let the remainder of the value
// break out and become a new header.
var ErrNewlineInAddressField = errors.New("netsec: CR or LF in address field")

// addressFields lists header fields whose values are mailbox lists.
var addressFields = map[string]bool{
	"From":          true,
	"Sender":        true,
	"Reply-To":      true,
	"To":            true,
	"Cc":            true,
	"Bcc":           true,
	"Resent-From":   true,
	"Resent-Sender": true,
	"Resent-To":     true,
	"Resent-Cc":     true,
	"Resent-Bcc":    true,
	"Return-Path":   true,
}

// FieldError records a single address field that failed adjudication.
type FieldError struct {
	Field string
	Value string
	Err   error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("netsec: address field %s rejected: %v", e.Field, e.Err)
}

func (e *FieldError) Unwrap() error { return e.Err }

// Verdict is the outcome of adjudicating the address fields of a
// message. Kept holds the decoded address lists of fields without
// newlines; Failed lists the rejected fields.
type Verdict struct {
	Kept   map[string][]*mail.Address
	Failed []*FieldError
}

// Adjudicate checks every address field in h. A field whose value
// contains CR or LF fails; every other address list is decoded with
// the importable Go decoder (net/mail) and kept.
func Adjudicate(h mail.Header) *Verdict {
	v := &Verdict{Kept: make(map[string][]*mail.Address)}
	for name, values := range h {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if !addressFields[canonical] {
			continue
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				v.Failed = append(v.Failed, &FieldError{
					Field: canonical,
					Value: value,
					Err:   ErrNewlineInAddressField,
				})
				continue
			}
			list, err := mail.ParseAddressList(value)
			if err != nil {
				v.Failed = append(v.Failed, &FieldError{
					Field: canonical,
					Value: value,
					Err:   err,
				})
				continue
			}
			v.Kept[canonical] = append(v.Kept[canonical], list...)
		}
	}
	return v
}

// ReadMessage reads a message and adjudicates its address fields.
// The raw header block is checked before decoding, because the
// net/mail decoder unfolds continuation lines and would hide the
// CR/LF inside a folded address field. Decoding itself is done with
// the importable Go decoder (net/mail).
func ReadMessage(r io.Reader) (*mail.Message, *Verdict, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	return msg, adjudicateRaw(headerBlock(data)), nil
}

// headerBlock returns the raw header section of a message, before
// the blank line that separates it from the body.
func headerBlock(data []byte) string {
	if i := bytes.Index(data, []byte("\r\n\r\n")); i >= 0 {
		return string(data[:i])
	}
	if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
		return string(data[:i])
	}
	return string(data)
}

// adjudicateRaw adjudicates the address fields of a raw header block.
// A field's raw value still contains the CR/LF of any continuation
// lines, so a folded or injected address field fails here.
func adjudicateRaw(block string) *Verdict {
	v := &Verdict{Kept: make(map[string][]*mail.Address)}
	for _, f := range rawFields(block) {
		canonical := textproto.CanonicalMIMEHeaderKey(f.name)
		if !addressFields[canonical] {
			continue
		}
		if strings.ContainsAny(f.value, "\r\n") {
			v.Failed = append(v.Failed, &FieldError{
				Field: canonical,
				Value: f.value,
				Err:   ErrNewlineInAddressField,
			})
			continue
		}
		list, err := mail.ParseAddressList(strings.TrimSpace(f.value))
		if err != nil {
			v.Failed = append(v.Failed, &FieldError{
				Field: canonical,
				Value: f.value,
				Err:   err,
			})
			continue
		}
		v.Kept[canonical] = append(v.Kept[canonical], list...)
	}
	return v
}

// rawField is a single header field in its raw, still-folded form.
type rawField struct {
	name  string
	value string
}

// rawFields splits a raw header block into fields. Continuation
// lines (starting with a space or tab) stay part of the field, line
// breaks included.
func rawFields(block string) []rawField {
	var fields []rawField
	for len(block) > 0 {
		end := 0
		for {
			i := strings.IndexByte(block[end:], '\n')
			if i < 0 {
				end = len(block)
				break
			}
			end += i + 1
			if end >= len(block) || (block[end] != ' ' && block[end] != '\t') {
				break
			}
		}
		field := strings.TrimRight(block[:end], "\r\n")
		block = block[end:]
		name, value, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		fields = append(fields, rawField{
			name:  name,
			value: strings.TrimLeft(value, " \t"),
		})
	}
	return fields
}
