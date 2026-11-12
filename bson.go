package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// BSON element types.
const (
	bsonDouble        = 0x01
	bsonString        = 0x02
	bsonDocument      = 0x03
	bsonArray         = 0x04
	bsonBinary        = 0x05
	bsonUndefined     = 0x06
	bsonObjectID      = 0x07
	bsonBoolean       = 0x08
	bsonDateTime      = 0x09
	bsonNull          = 0x0A
	bsonRegex         = 0x0B
	bsonDBPointer     = 0x0C
	bsonJavaScript    = 0x0D
	bsonSymbol        = 0x0E
	bsonCodeWithScope = 0x0F
	bsonInt32         = 0x10
	bsonTimestamp     = 0x11
	bsonInt64         = 0x12
	bsonDecimal128    = 0x13
	bsonMaxKey        = 0x7F
	bsonMinKey        = 0xFF
)

// DefaultMaxDepth is the nesting limit used when Policy.MaxDepth is zero.
// MongoDB also limits BSON nesting to 100 levels.
const DefaultMaxDepth = 100

// Sentinel errors. Use errors.Is to classify validation failures without
// depending on the human-readable error text.
var (
	ErrTotalLength     = errors.New("bson: document length does not match buffer")
	ErrDocumentLength  = errors.New("bson: invalid document length")
	ErrEarlyTerminator = errors.New("bson: document terminator precedes declared length")
	ErrNestedOverflow  = errors.New("bson: nested document exceeds parent boundary")
	ErrElementBoundary = errors.New("bson: element exceeds document boundary")
	ErrCString         = errors.New("bson: cstring is not NUL-terminated")
	ErrStringLength    = errors.New("bson: string length does not include trailing NUL")
	ErrDuplicateKey    = errors.New("bson: duplicate key in document")
	ErrDollarKey       = errors.New("bson: key starts with '$' and policy forbids it")
	ErrDottedKey       = errors.New("bson: key contains '.' and policy forbids it")
	ErrDepthExceeded   = errors.New("bson: document nesting depth exceeded")
	ErrUnsupportedType = errors.New("bson: unsupported element type")
	ErrTruncated       = errors.New("bson: truncated BSON value")
)

// Policy controls key-name validation. The zero value is strict: keys
// beginning with '$' and keys containing '.' are rejected.
type Policy struct {
	// AllowDollarKeys permits keys whose first byte is '$', such as "$set".
	AllowDollarKeys bool
	// AllowDottedKeys permits keys containing '.', such as "user.name".
	AllowDottedKeys bool
	// MaxDepth limits document nesting. Zero selects DefaultMaxDepth.
	MaxDepth int
}

// ValidationError identifies where a document failed validation. Offset is
// an absolute byte offset into the buffer passed to Validate.
type ValidationError struct {
	Offset int
	Path   string
	Detail string
	Err    error
}

func (e *ValidationError) Error() string {
	msg := e.Err.Error()
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Path != "" {
		msg += " (path " + e.Path + ")"
	}
	return fmt.Sprintf("%s at byte %d", msg, e.Offset)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// Validate checks a complete top-level BSON document using the strict
// zero-value Policy.
func Validate(data []byte) error {
	return ValidateWithPolicy(data, Policy{})
}

// ValidateWithPolicy checks a complete top-level BSON document. The declared
// top-level length must equal len(data) exactly. Nested documents are checked
// against their parent's declared boundary, duplicate keys are rejected at
// every level, and key-name restrictions come from policy.
func ValidateWithPolicy(data []byte, policy Policy) error {
	if len(data) < 5 {
		return &ValidationError{Offset: 0, Err: ErrDocumentLength, Detail: "document is shorter than 5 bytes"}
	}
	declared := int64(int32(binary.LittleEndian.Uint32(data[0:4])))
	if declared < 5 {
		return &ValidationError{Offset: 0, Err: ErrDocumentLength, Detail: fmt.Sprintf("declared length %d is below minimum 5", declared)}
	}
	if declared != int64(len(data)) {
		return &ValidationError{
			Offset: 0,
			Err:    ErrTotalLength,
			Detail: fmt.Sprintf("declared %d bytes, buffer has %d", declared, len(data)),
		}
	}

	maxDepth := policy.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	if maxDepth < 0 {
		return &ValidationError{Offset: 0, Err: ErrDepthExceeded, Detail: "MaxDepth must not be negative"}
	}

	v := validator{data: data, policy: policy, maxDepth: maxDepth}
	if _, err := v.document(0, len(data), 0, ""); err != nil {
		return err
	}
	return nil
}

type validator struct {
	data     []byte
	policy   Policy
	maxDepth int
}

// document validates a document whose first four bytes contain its length.
// limit is the first byte after the region the document may occupy (the
// parent's terminator is outside limit). It returns the declared end offset.
func (v *validator) document(start, limit, depth int, path string) (int, error) {
	if depth > v.maxDepth {
		return 0, &ValidationError{Offset: start, Path: path, Err: ErrDepthExceeded, Detail: fmt.Sprintf("depth %d > %d", depth, v.maxDepth)}
	}
	if start < 0 || start > limit || limit > len(v.data) || limit-start < 5 {
		return 0, &ValidationError{Offset: start, Path: path, Err: ErrDocumentLength, Detail: "not enough bytes for a document"}
	}

	rawLen := int64(int32(binary.LittleEndian.Uint32(v.data[start : start+4])))
	if rawLen < 5 {
		return 0, &ValidationError{Offset: start, Path: path, Err: ErrDocumentLength, Detail: fmt.Sprintf("declared length %d is below minimum 5", rawLen)}
	}
	end64 := int64(start) + rawLen
	if end64 > int64(limit) {
		err := ErrNestedOverflow
		detail := fmt.Sprintf("declared end %d exceeds parent limit %d", end64, limit)
		if depth == 0 {
			err = ErrTotalLength
			detail = fmt.Sprintf("declared end %d exceeds buffer size %d", end64, limit)
		}
		return 0, &ValidationError{Offset: start, Path: path, Err: err, Detail: detail}
	}
	end := int(end64)
	if v.data[end-1] != 0x00 {
		return 0, &ValidationError{Offset: end - 1, Path: path, Err: ErrEarlyTerminator, Detail: "last byte of declared document is not NUL"}
	}

	seen := make(map[string]struct{})
	pos := start + 4
	for pos < end-1 {
		typ := v.data[pos]
		pos++
		if typ == 0x00 {
			return 0, &ValidationError{Offset: pos - 1, Path: path, Err: ErrEarlyTerminator, Detail: "terminator found before declared document end"}
		}

		nameStart := pos
		rel := bytes.IndexByte(v.data[pos:end-1], 0x00)
		if rel < 0 {
			return 0, &ValidationError{Offset: nameStart, Path: path, Err: ErrCString, Detail: "element name has no NUL before document end"}
		}
		name := string(v.data[pos : pos+rel])
		pos += rel + 1

		if _, exists := seen[name]; exists {
			return 0, &ValidationError{Offset: nameStart, Path: joinPath(path, name), Err: ErrDuplicateKey, Detail: fmt.Sprintf("key %q", name)}
		}
		seen[name] = struct{}{}

		if name != "" && name[0] == '$' && !v.policy.AllowDollarKeys {
			return 0, &ValidationError{Offset: nameStart, Path: joinPath(path, name), Err: ErrDollarKey, Detail: fmt.Sprintf("key %q", name)}
		}
		if strings.ContainsRune(name, '.') && !v.policy.AllowDottedKeys {
			return 0, &ValidationError{Offset: nameStart, Path: joinPath(path, name), Err: ErrDottedKey, Detail: fmt.Sprintf("key %q", name)}
		}

		next, err := v.value(typ, pos, end-1, depth, joinPath(path, name))
		if err != nil {
			return 0, err
		}
		pos = next
	}
	if pos != end-1 {
		return 0, &ValidationError{Offset: pos, Path: path, Err: ErrElementBoundary, Detail: "element parsing crossed the document terminator"}
	}
	return end, nil
}

// value validates one element value and returns the offset immediately after
// it. limit is the document terminator position, so a value may never consume
// the terminator belonging to its parent document.
func (v *validator) value(typ byte, pos, limit, depth int, path string) (int, error) {
	switch typ {
	case bsonDouble, bsonDateTime, bsonTimestamp, bsonInt64:
		return v.fixed(pos, 8, limit, path)
	case bsonObjectID:
		return v.fixed(pos, 12, limit, path)
	case bsonInt32:
		return v.fixed(pos, 4, limit, path)
	case bsonDecimal128:
		return v.fixed(pos, 16, limit, path)
	case bsonUndefined, bsonNull, bsonMaxKey, bsonMinKey:
		return pos, nil
	case bsonBoolean:
		if pos >= limit {
			return 0, truncated(pos, limit, path)
		}
		if v.data[pos] > 1 {
			return 0, &ValidationError{Offset: pos, Path: path, Err: ErrTruncated, Detail: fmt.Sprintf("boolean byte %d is not 0 or 1", v.data[pos])}
		}
		return pos + 1, nil
	case bsonString, bsonJavaScript, bsonSymbol:
		return v.string(pos, limit, path)
	case bsonDocument, bsonArray:
		end, err := v.document(pos, limit, depth+1, path)
		if err != nil {
			return 0, err
		}
		return end, nil
	case bsonBinary:
		if pos+4 > limit {
			return 0, truncated(pos, limit, path)
		}
		n := int64(int32(binary.LittleEndian.Uint32(v.data[pos : pos+4])))
		if n < 0 {
			return 0, &ValidationError{Offset: pos, Path: path, Err: ErrTruncated, Detail: fmt.Sprintf("negative binary length %d", n)}
		}
		if int64(pos)+4+1+n > int64(limit) {
			return 0, truncated(pos, limit, path)
		}
		return int(int64(pos) + 4 + 1 + n), nil
	case bsonRegex:
		next, err := v.cstring(pos, limit, path, "regex pattern")
		if err != nil {
			return 0, err
		}
		return v.cstring(next, limit, path, "regex options")
	case bsonDBPointer:
		next, err := v.string(pos, limit, path)
		if err != nil {
			return 0, err
		}
		return v.fixed(next, 12, limit, path)
	case bsonCodeWithScope:
		return v.codeWithScope(pos, limit, depth, path)
	default:
		return 0, &ValidationError{Offset: pos - 1, Path: path, Err: ErrUnsupportedType, Detail: fmt.Sprintf("type 0x%02x", typ)}
	}
}

func (v *validator) fixed(pos, n, limit int, path string) (int, error) {
	if pos < 0 || n < 0 || pos > limit || n > limit-pos {
		return 0, truncated(pos, limit, path)
	}
	return pos + n, nil
}

func (v *validator) string(pos, limit int, path string) (int, error) {
	if pos+4 > limit {
		return 0, truncated(pos, limit, path)
	}
	n := int64(int32(binary.LittleEndian.Uint32(v.data[pos : pos+4])))
	if n < 1 {
		return 0, &ValidationError{Offset: pos, Path: path, Err: ErrStringLength, Detail: fmt.Sprintf("declared length %d; trailing NUL requires at least 1", n)}
	}
	end64 := int64(pos) + 4 + n
	if end64 > int64(limit) {
		return 0, truncated(pos, limit, path)
	}
	end := int(end64)
	if v.data[end-1] != 0x00 {
		return 0, &ValidationError{Offset: end - 1, Path: path, Err: ErrStringLength, Detail: "last byte of string is not NUL"}
	}
	return end, nil
}

func (v *validator) cstring(pos, limit int, path, what string) (int, error) {
	if pos >= limit {
		return 0, truncated(pos, limit, path)
	}
	rel := bytes.IndexByte(v.data[pos:limit], 0x00)
	if rel < 0 {
		return 0, &ValidationError{Offset: pos, Path: path, Err: ErrCString, Detail: what + " has no NUL before document end"}
	}
	return pos + rel + 1, nil
}

func (v *validator) codeWithScope(pos, limit, depth int, path string) (int, error) {
	if pos+4 > limit {
		return 0, truncated(pos, limit, path)
	}
	total := int64(int32(binary.LittleEndian.Uint32(v.data[pos : pos+4])))
	if total < 10 {
		return 0, &ValidationError{Offset: pos, Path: path, Err: ErrTruncated, Detail: fmt.Sprintf("code-with-scope length %d is too small", total)}
	}
	end64 := int64(pos) + total
	if end64 > int64(limit) {
		return 0, truncated(pos, limit, path)
	}
	end := int(end64)

	next, err := v.string(pos+4, end, path)
	if err != nil {
		return 0, err
	}
	docEnd, err := v.document(next, end, depth+1, path)
	if err != nil {
		return 0, err
	}
	if docEnd != end {
		return 0, &ValidationError{Offset: docEnd, Path: path, Err: ErrElementBoundary, Detail: "code-with-scope has trailing bytes"}
	}
	return end, nil
}

func truncated(pos, limit int, path string) error {
	return &ValidationError{Offset: pos, Path: path, Err: ErrTruncated, Detail: fmt.Sprintf("value exceeds document boundary %d", limit)}
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
