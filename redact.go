package sframe

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

var redactedValue = []byte("<redacted>")

// Redactor rewrites selected payload fields in otherwise intact frames.
// Header fields (sequence, timestamp) are never touched, so output streams
// keep their original timing and ordering and remain replayable.
type Redactor struct {
	// Paths are dot-separated field names, e.g. "user.credentials.password".
	Paths []string
	// Strict rejects the frame (no output) when any path is absent.
	Strict bool
}

// Redact parses frame, replaces the configured field values, recomputes all
// length prefixes and the CRC, and returns the new frame. On any error the
// returned frame is nil: callers must never fall back to forwarding the
// original bytes. Error messages contain only paths and structural facts,
// never field values.
func (r *Redactor) Redact(frame []byte) ([]byte, error) {
	hdr, payload, err := SplitFrame(frame)
	if err != nil {
		return nil, err
	}
	fields, err := ParsePayload(payload)
	if err != nil {
		return nil, err
	}
	var secrets [][]byte
	for _, p := range r.Paths {
		segs := strings.Split(p, ".")
		f := findField(fields, segs)
		if f == nil {
			if r.Strict {
				return nil, fmt.Errorf("sframe: redact: path %q not found, frame rejected", p)
			}
			continue
		}
		if s := secretBytes(f); len(s) > 0 {
			secrets = append(secrets, s)
		}
		redactField(f)
	}
	newPayload := MarshalObject(fields)
	// Leak guard: the bytes we just removed must not survive anywhere in the
	// output payload, e.g. duplicated inside an unknown extension field.
	for _, s := range secrets {
		if bytes.Contains(newPayload, s) {
			return nil, errors.New("sframe: redact: sensitive bytes still present in output, frame rejected")
		}
	}
	return EncodeFrame(hdr, newPayload), nil
}

func findField(fields []*Field, segs []string) *Field {
	if len(segs) == 0 {
		return nil
	}
	for _, f := range fields {
		if f.Name != segs[0] {
			continue
		}
		if len(segs) == 1 {
			return f
		}
		if f.Type == TypeObject {
			return findField(f.Kids, segs[1:])
		}
		return nil
	}
	return nil
}

// secretBytes returns the serialized sensitive value for the leak guard.
// Integers and booleans are skipped: too short to match safely.
func secretBytes(f *Field) []byte {
	switch f.Type {
	case TypeString, TypeBytes:
		return f.Raw
	case TypeObject:
		return MarshalObject(f.Kids)
	}
	return nil
}

func redactField(f *Field) {
	switch f.Type {
	case TypeString, TypeBytes:
		f.Raw = redactedValue
	case TypeInt64:
		f.Int = 0
	case TypeBool:
		f.Bool = false
	case TypeObject:
		f.Kids = nil
	}
}
