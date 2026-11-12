package netsec

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Tag is a decoded CBOR tag other than the bignum tags (2 and 3). Tag 24
// values contain the recursively decoded embedded data item.
type Tag struct {
	Number uint64
	Value  any
}

// SimpleValue represents CBOR simple values that do not map to a Go scalar.
// In particular, undefined is SimpleValue(23); null is nil.
type SimpleValue uint8

// Map keys that Go maps cannot hold directly are normalized to these
// comparable wrapper types. String, integer, boolean, float, nil and
// SimpleValue keys are returned unchanged.
type (
	ByteStringKey struct{ Hex string }
	CompositeKey  struct{ Canonical string }
	BigIntKey     struct{ Decimal string }
)

// Reader decodes one CBOR data item at a time from a byte slice using the
// configured limits. It is not safe for concurrent use.
type Reader struct {
	data   []byte
	limits Limits
	pos    int
	budget *decodeBudget
}

type decodeBudget struct {
	stringChunks int
}

// RestrictedReader is an alias for Reader.
type RestrictedReader = Reader

// NewReader creates a restricted reader over data. With no limits argument the
// default limits are used. If more than one Limits value is supplied, only the
// first is used.
func NewReader(data []byte, limits ...Limits) *Reader {
	normalized := DefaultLimits()
	if len(limits) > 0 {
		normalized = limits[0].normalize()
	}
	return &Reader{
		data:   data,
		limits: normalized,
		budget: &decodeBudget{},
	}
}

// NewRestrictedReader is an alias for NewReader.
func NewRestrictedReader(data []byte, limits ...Limits) *Reader {
	return NewReader(data, limits...)
}

// Decode decodes exactly one CBOR item and rejects trailing bytes.
func Decode(data []byte, limits ...Limits) (any, error) {
	return NewReader(data, limits...).Decode()
}

// DecodeWithLimits is an explicit-limit alias for Decode.
func DecodeWithLimits(data []byte, limits Limits) (any, error) {
	return Decode(data, limits)
}

// Read is an alias for Decode.
func Read(data []byte, limits ...Limits) (any, error) {
	return Decode(data, limits...)
}

// DecodeReader reads a bounded amount from src and then decodes exactly one
// CBOR item. MaxInputBytes from Limits applies to this entry point.
func DecodeReader(src io.Reader, limits ...Limits) (any, error) {
	if src == nil {
		return nil, syntaxError(0, "nil input reader")
	}
	l := DefaultLimits()
	if len(limits) > 0 {
		l = limits[0].normalize()
	}
	data, err := io.ReadAll(io.LimitReader(src, int64(l.MaxInputBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("cbor: reading input: %w", err)
	}
	if len(data) > l.MaxInputBytes {
		return nil, fmt.Errorf("%w: got more than %d bytes", ErrInputTooLarge, l.MaxInputBytes)
	}
	return NewReader(data, l).Decode()
}

// Decode decodes one item from the reader's current position and rejects
// trailing bytes.
func (r *Reader) Decode() (any, error) {
	value, err := r.parseValue(0, 0, 0)
	if err != nil {
		return nil, err
	}
	if r.pos != len(r.data) {
		return nil, fmt.Errorf("%w: %d bytes at offset %d",
			ErrTrailingData, len(r.data)-r.pos, r.pos)
	}
	return value, nil
}

// ReadValue decodes one item from the reader's current position without
// requiring the rest of the input to be consumed.
func (r *Reader) ReadValue() (any, error) {
	return r.parseValue(0, 0, 0)
}

// Read is an alias for ReadValue.
func (r *Reader) Read() (any, error) { return r.ReadValue() }

// Limits returns the normalized budgets in use by the reader.
func (r *Reader) Limits() Limits { return r.limits }

// Position returns the number of input bytes consumed so far.
func (r *Reader) Position() int { return r.pos }

// Remaining returns the number of unread input bytes.
func (r *Reader) Remaining() int { return len(r.data) - r.pos }

func (r *Reader) parseValue(depth, tagDepth, tag24Depth int) (any, error) {
	if depth > r.limits.MaxDepth {
		return nil, limitError("nesting depth", uint64(r.limits.MaxDepth),
			uint64(depth), r.pos, ErrNestingTooDeep)
	}

	start := r.pos
	major, ai, n, indefinite, err := r.readHeader()
	if err != nil {
		return nil, err
	}

	switch major {
	case 0: // unsigned integer
		if indefinite {
			return nil, syntaxError(start, "indefinite unsigned integer")
		}
		return n, nil

	case 1: // negative integer, value is -1-n
		if indefinite {
			return nil, syntaxError(start, "indefinite negative integer")
		}
		if n <= math.MaxInt64 {
			return -1 - int64(n), nil
		}
		value := new(big.Int).SetUint64(n)
		value.Add(value, big.NewInt(1))
		value.Neg(value)
		return value, nil

	case 2, 3: // byte string, text string
		return r.readString(major, n, indefinite)

	case 4: // array
		return r.readArray(depth+1, n, indefinite)

	case 5: // map
		return r.readMap(depth+1, n, indefinite)

	case 6: // tag
		if indefinite {
			return nil, syntaxError(start, "indefinite tag")
		}
		if tagDepth+1 > r.limits.MaxTagDepth {
			return nil, limitError("tag nesting", uint64(r.limits.MaxTagDepth),
				uint64(tagDepth+1), start, ErrTagDepthExceeded)
		}
		return r.readTag(depth, tagDepth, tag24Depth, n)

	case 7:
		return r.readSimple(ai, n, indefinite)
	}

	return nil, syntaxError(start, "invalid major type %d", major)
}

func (r *Reader) readHeader() (major, ai byte, n uint64, indefinite bool, err error) {
	offset := r.pos
	if r.pos >= len(r.data) {
		err = syntaxError(offset, "unexpected end of input")
		return
	}
	first := r.data[r.pos]
	r.pos++

	major = first >> 5
	ai = first & 0x1f
	switch {
	case ai < 24:
		n = uint64(ai)
	case ai == 24:
		n, err = r.readUint(1, offset)
	case ai == 25:
		n, err = r.readUint(2, offset)
	case ai == 26:
		n, err = r.readUint(4, offset)
	case ai == 27:
		n, err = r.readUint(8, offset)
	case ai == 31:
		indefinite = true
	default:
		err = syntaxError(offset, "reserved additional information %d", ai)
	}
	return
}

func (r *Reader) readUint(size int, offset int) (uint64, error) {
	if len(r.data)-r.pos < size {
		return 0, syntaxError(offset, "truncated length field")
	}
	switch size {
	case 1:
		n := uint64(r.data[r.pos])
		r.pos++
		return n, nil
	case 2:
		n := uint64(binary.BigEndian.Uint16(r.data[r.pos:]))
		r.pos += 2
		return n, nil
	case 4:
		n := uint64(binary.BigEndian.Uint32(r.data[r.pos:]))
		r.pos += 4
		return n, nil
	case 8:
		n := binary.BigEndian.Uint64(r.data[r.pos:])
		r.pos += 8
		return n, nil
	default:
		return 0, syntaxError(offset, "invalid length field size %d", size)
	}
}

func (r *Reader) checkDeclared(n uint64, kind string, specific error) error {
	offset := r.pos
	remaining := len(r.data) - r.pos
	if float64(n) > float64(remaining)*r.limits.MaxDeclaredLengthRatio {
		return &LimitError{
			Kind:     kind,
			Limit:    r.limits.MaxDeclaredLengthRatio,
			Got:      n,
			Offset:   offset,
			specific: specific,
		}
	}
	return nil
}

func (r *Reader) requireAvailable(n uint64, kind string, specific error) error {
	remaining := uint64(len(r.data) - r.pos)
	if n > remaining {
		return limitError(kind, remaining, n, r.pos, specific)
	}
	return nil
}

func (r *Reader) readString(major byte, n uint64, indefinite bool) (any, error) {
	if indefinite {
		data, err := r.readIndefiniteString(major, 0)
		if err != nil {
			return nil, err
		}
		if major == 3 {
			if !utf8.Valid(data) {
				return nil, syntaxError(r.pos, "invalid UTF-8 text string")
			}
			return string(data), nil
		}
		return data, nil
	}

	if err := r.checkDeclared(n, "declared string length", ErrDeclaredLengthExceeded); err != nil {
		return nil, err
	}
	if err := r.requireAvailable(n, "string length", ErrDeclaredLengthExceeded); err != nil {
		return nil, err
	}
	data := readCopy(r.data[r.pos : r.pos+int(n)])
	r.pos += int(n)
	if major == 3 {
		if !utf8.Valid(data) {
			return nil, syntaxError(r.pos, "invalid UTF-8 text string")
		}
		return string(data), nil
	}
	return data, nil
}

// readIndefiniteString reads chunks until a break. maxOut, when positive,
// bounds the total chunk payload (used for bignums).
func (r *Reader) readIndefiniteString(major byte, maxOut int) ([]byte, error) {
	var out []byte
	for {
		chunkOffset := r.pos
		if r.pos >= len(r.data) {
			return nil, syntaxError(chunkOffset, "unterminated indefinite-length string")
		}
		if r.data[r.pos] == 0xff {
			r.pos++
			return out, nil
		}

		chunkMajor, _, chunkLen, chunkIndefinite, err := r.readHeader()
		if err != nil {
			return nil, err
		}
		if chunkIndefinite {
			return nil, syntaxError(chunkOffset, "indefinite-length string chunk")
		}
		if chunkMajor != major {
			return nil, syntaxError(chunkOffset,
				"string chunk has major type %d, want %d", chunkMajor, major)
		}

		r.budget.stringChunks++
		if r.budget.stringChunks > r.limits.MaxIndefiniteStringChunks {
			return nil, limitError("indefinite string chunks",
				uint64(r.limits.MaxIndefiniteStringChunks),
				uint64(r.budget.stringChunks), chunkOffset,
				ErrIndefiniteStringChunks)
		}
		if err := r.checkDeclared(chunkLen, "string chunk length", ErrDeclaredLengthExceeded); err != nil {
			return nil, err
		}
		if err := r.requireAvailable(chunkLen, "string chunk length", ErrDeclaredLengthExceeded); err != nil {
			return nil, err
		}
		if maxOut > 0 && len(out)+int(chunkLen) > maxOut {
			return nil, limitError("bignum bytes", uint64(maxOut),
				uint64(len(out)+int(chunkLen)), chunkOffset, ErrBigNumTooLarge)
		}

		out = append(out, r.data[r.pos:r.pos+int(chunkLen)]...)
		r.pos += int(chunkLen)
	}
}

func (r *Reader) readArray(depth int, n uint64, indefinite bool) ([]any, error) {
	if indefinite {
		values := make([]any, 0)
		for {
			if r.pos >= len(r.data) {
				return nil, syntaxError(r.pos, "unterminated indefinite-length array")
			}
			if r.data[r.pos] == 0xff {
				r.pos++
				return values, nil
			}
			if len(values) >= r.limits.MaxContainerElements {
				return nil, limitError("array elements",
					uint64(r.limits.MaxContainerElements),
					uint64(len(values)+1), r.pos, ErrContainerElementsExceeded)
			}
			value, err := r.parseValue(depth, 0, 0)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	}

	if err := r.checkDeclared(n, "array element count", ErrDeclaredLengthExceeded); err != nil {
		return nil, err
	}
	if err := r.requireAvailable(n, "array element count", ErrDeclaredLengthExceeded); err != nil {
		return nil, err
	}
	if n > uint64(r.limits.MaxContainerElements) {
		return nil, limitError("array elements", uint64(r.limits.MaxContainerElements),
			n, r.pos, ErrContainerElementsExceeded)
	}

	capHint := int(n)
	if capHint > 1024 {
		capHint = 1024
	}
	values := make([]any, 0, capHint)
	for i := uint64(0); i < n; i++ {
		value, err := r.parseValue(depth, 0, 0)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (r *Reader) readMap(depth int, n uint64, indefinite bool) (map[any]any, error) {
	capHint := 0
	if !indefinite {
		if err := r.checkDeclared(n, "map entry count", ErrDeclaredLengthExceeded); err != nil {
			return nil, err
		}
		if err := r.requireAvailable(n, "map entry count", ErrDeclaredLengthExceeded); err != nil {
			return nil, err
		}
		if n > uint64(r.limits.MaxContainerElements) {
			return nil, limitError("map entries", uint64(r.limits.MaxContainerElements),
				n, r.pos, ErrContainerElementsExceeded)
		}
		if n > 1024 {
			capHint = 1024
		} else {
			capHint = int(n)
		}
	}

	entries := make(map[any]any, capHint)
	seenRaw := make(map[string]struct{}, capHint)
	seenCanonical := make(map[string]struct{}, capHint)
	readEntry := func() error {
		keyOffset := r.pos
		key, err := r.parseValue(depth, 0, 0)
		if err != nil {
			return err
		}
		raw := string(r.data[keyOffset:r.pos])
		canonical := canonicalKey(key)
		if _, ok := seenRaw[raw]; ok {
			return fmt.Errorf("%w at byte %d", ErrDuplicateMapKey, keyOffset)
		}
		if _, ok := seenCanonical[canonical]; ok {
			return fmt.Errorf("%w at byte %d", ErrDuplicateMapKey, keyOffset)
		}
		seenRaw[raw] = struct{}{}
		seenCanonical[canonical] = struct{}{}

		value, err := r.parseValue(depth, 0, 0)
		if err != nil {
			return err
		}
		entries[normalizeMapKey(key)] = value
		return nil
	}

	if indefinite {
		count := 0
		for {
			if r.pos >= len(r.data) {
				return nil, syntaxError(r.pos, "unterminated indefinite-length map")
			}
			if r.data[r.pos] == 0xff {
				r.pos++
				return entries, nil
			}
			if count >= r.limits.MaxContainerElements {
				return nil, limitError("map entries",
					uint64(r.limits.MaxContainerElements),
					uint64(count+1), r.pos, ErrContainerElementsExceeded)
			}
			if err := readEntry(); err != nil {
				return nil, err
			}
			count++
		}
	}

	for i := uint64(0); i < n; i++ {
		if err := readEntry(); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func (r *Reader) readTag(depth, tagDepth, tag24Depth int, number uint64) (any, error) {
	switch number {
	case 2, 3: // positive/negative bignum
		content, err := r.readByteStringLimited(r.limits.MaxBigNumBytes)
		if err != nil {
			return nil, err
		}
		value := new(big.Int).SetBytes(content)
		if number == 3 {
			value.Neg(value)
			value.Sub(value, big.NewInt(1))
		}
		return value, nil

	case 24: // embedded CBOR data item
		if tag24Depth+1 > r.limits.MaxTag24Depth {
			return nil, limitError("tag 24 recursion", uint64(r.limits.MaxTag24Depth),
				uint64(tag24Depth+1), r.pos, ErrTag24DepthExceeded)
		}
		content, err := r.readByteStringLimited(0)
		if err != nil {
			return nil, err
		}
		embedded := &Reader{
			data:   content,
			limits: r.limits,
			budget: r.budget,
		}
		value, err := embedded.parseValue(depth+1, 0, tag24Depth+1)
		if err != nil {
			return nil, err
		}
		if embedded.pos != len(content) {
			return nil, fmt.Errorf("%w: embedded tag 24 item has %d trailing bytes",
				ErrTrailingData, len(content)-embedded.pos)
		}
		return Tag{Number: 24, Value: value}, nil

	default:
		value, err := r.parseValue(depth+1, tagDepth+1, tag24Depth)
		if err != nil {
			return nil, err
		}
		return Tag{Number: number, Value: value}, nil
	}
}

func (r *Reader) readByteStringLimited(maxBytes int) ([]byte, error) {
	start := r.pos
	major, _, n, indefinite, err := r.readHeader()
	if err != nil {
		return nil, err
	}
	if major != 2 {
		return nil, syntaxError(start, "expected byte string, got major type %d", major)
	}
	if indefinite {
		return r.readIndefiniteString(2, maxBytes)
	}
	if err := r.checkDeclared(n, "byte string length", ErrDeclaredLengthExceeded); err != nil {
		return nil, err
	}
	if err := r.requireAvailable(n, "byte string length", ErrDeclaredLengthExceeded); err != nil {
		return nil, err
	}
	if maxBytes > 0 && n > uint64(maxBytes) {
		return nil, limitError("bignum bytes", uint64(maxBytes), n, start, ErrBigNumTooLarge)
	}
	data := readCopy(r.data[r.pos : r.pos+int(n)])
	r.pos += int(n)
	return data, nil
}

func (r *Reader) readSimple(ai byte, n uint64, indefinite bool) (any, error) {
	if indefinite {
		return nil, syntaxError(r.pos, "unexpected break")
	}
	switch ai {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22:
		return nil, nil
	case 23:
		return SimpleValue(23), nil
	case 24:
		return SimpleValue(n), nil
	case 25:
		return halfToFloat64(uint16(n)), nil
	case 26:
		return float64(math.Float32frombits(uint32(n))), nil
	case 27:
		return math.Float64frombits(n), nil
	default:
		if ai < 20 {
			return SimpleValue(ai), nil
		}
		return nil, syntaxError(r.pos, "invalid simple value %d", ai)
	}
}

func halfToFloat64(h uint16) float64 {
	sign := h & 0x8000
	exponent := (h >> 10) & 0x1f
	fraction := h & 0x03ff

	var value float64
	switch exponent {
	case 0:
		value = math.Ldexp(float64(fraction), -24)
	case 31:
		if fraction == 0 {
			value = math.Inf(1)
		} else {
			value = math.NaN()
		}
	default:
		value = math.Ldexp(float64(fraction)+1024, int(exponent)-25)
	}
	if sign != 0 {
		value = -value
	}
	return value
}

func readCopy(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func normalizeMapKey(value any) any {
	switch key := value.(type) {
	case []byte:
		return ByteStringKey{Hex: hex.EncodeToString(key)}
	case []any, map[any]any, Tag:
		return CompositeKey{Canonical: canonicalKey(value)}
	case *big.Int:
		return BigIntKey{Decimal: key.String()}
	default:
		return value
	}
}

func canonicalKey(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool:" + strconv.FormatBool(value)
	case uint64:
		return "uint:" + strconv.FormatUint(value, 10)
	case int64:
		return "int:" + strconv.FormatInt(value, 10)
	case *big.Int:
		return "bigint:" + value.String()
	case float64:
		return "float:" + strconv.FormatFloat(value, 'g', -1, 64)
	case string:
		return "text:" + strconv.Quote(value)
	case []byte:
		return "bytes:" + hex.EncodeToString(value)
	case SimpleValue:
		return "simple:" + strconv.FormatUint(uint64(value), 10)
	case Tag:
		return "tag:" + strconv.FormatUint(value.Number, 10) +
			"(" + canonicalKey(value.Value) + ")"
	case ByteStringKey:
		return "bytes:" + value.Hex
	case CompositeKey:
		return "composite:" + value.Canonical
	case BigIntKey:
		return "bigint:" + value.Decimal
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = canonicalKey(item)
		}
		return "array[" + strings.Join(parts, ",") + "]"
	case map[any]any:
		parts := make([]string, 0, len(value))
		for key, item := range value {
			parts = append(parts, canonicalKey(key)+"="+canonicalKey(item))
		}
		sort.Strings(parts)
		return "map{" + strings.Join(parts, ",") + "}"
	default:
		return fmt.Sprintf("unknown:%T:%v", value, value)
	}
}
