package intwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// JSON wire shape: {"k":"u|s|big","w":64,"v":"<canonical decimal>"}
//
// The exact value always travels as a STRING tag, so a JSON processor that
// parses numbers into IEEE-754 doubles (53-bit mantissa) cannot silently
// truncate it. Decoding never executes host code: it only validates bytes
// and constructs integers.

var ErrJSONShape = errors.New("intwire: malformed JSON integer envelope")

type jsonEnvelope struct {
	K string `json:"k"`
	W int    `json:"w,omitempty"`
	V string `json:"v"`
}

// MarshalJSON emits the canonical string-tagged envelope.
func (v Value) MarshalJSON() ([]byte, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(jsonEnvelope{K: v.Kind.String(), W: v.Width, V: v.Decimal()})
}

// UnmarshalJSON accepts the canonical envelope, a bare JSON number lexeme,
// or a quoted decimal/exponent lexeme. Bare numbers are parsed from their
// raw text — never through a float64.
func (v *Value) UnmarshalJSON(raw []byte) error {
	parsed, err := ParseJSONValue(raw)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// ParseJSONValue decodes one JSON value (object envelope, string, or bare
// number) into a Value without ever materializing a host float.
func ParseJSONValue(raw []byte) (Value, error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 {
		return Value{}, ErrJSONShape
	}
	switch trim[0] {
	case '{':
		var env jsonEnvelope
		dec := json.NewDecoder(bytes.NewReader(trim))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&env); err != nil {
			return Value{}, fmt.Errorf("%w: %v", ErrJSONShape, err)
		}
		return envelopeValue(env)
	case '"':
		var s string
		if err := json.Unmarshal(trim, &s); err != nil {
			return Value{}, fmt.Errorf("%w: %v", ErrJSONShape, err)
		}
		return ParseIntegerLexeme(s)
	default:
		// Bare JSON number: parse the raw lexeme textually.
		return ParseIntegerLexeme(string(trim))
	}
}

func envelopeValue(env jsonEnvelope) (Value, error) {
	n, err := parsePlainDecimal(env.V)
	if err != nil {
		return Value{}, err
	}
	v := NewBig(n)
	v.Raw = env.V
	switch env.K {
	case "big":
		if env.W != 0 {
			return Value{}, ErrWidth
		}
		return v, nil
	case "u":
		if !n.IsUint64() {
			return Value{}, fmt.Errorf("%w: %s not unsigned 64-bit", ErrRange, env.V)
		}
		uv, err := NewUnsigned(env.W, n.Uint64())
		if err != nil {
			return Value{}, err
		}
		uv.Raw = env.V
		return uv, nil
	case "s":
		if !n.IsInt64() {
			return Value{}, fmt.Errorf("%w: %s not signed 64-bit", ErrRange, env.V)
		}
		sv, err := NewSigned(env.W, n.Int64())
		if err != nil {
			return Value{}, err
		}
		sv.Raw = env.V
		return sv, nil
	}
	return Value{}, fmt.Errorf("%w: unknown kind %q", ErrJSONShape, env.K)
}

// parsePlainDecimal parses a canonical decimal integer string (optional
// sign + digits) exactly.
func parsePlainDecimal(s string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty value", ErrSyntax)
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	if i == len(s) {
		return nil, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	for ; i < len(s); i++ {
		if !isDigit(s[i]) {
			return nil, fmt.Errorf("%w: %q", ErrSyntax, s)
		}
	}
	if len(s) > MaxDecimalDigits+1 {
		return nil, ErrQuota
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	return n, nil
}

// StreamEncoder writes a sequence of values as canonical JSON envelopes to
// an underlying stream (one JSON object per value).
type StreamEncoder struct {
	enc *json.Encoder
}

func NewStreamEncoder(w io.Writer) *StreamEncoder {
	return &StreamEncoder{enc: json.NewEncoder(w)}
}

func (s *StreamEncoder) WriteValue(v Value) error { return s.enc.Encode(v) }

// StreamDecoder reads values back. Each element is captured as raw JSON
// text and parsed lexically, so no value ever passes through a float64.
type StreamDecoder struct {
	dec *json.Decoder
}

func NewStreamDecoder(r io.Reader) *StreamDecoder {
	return &StreamDecoder{dec: json.NewDecoder(r)}
}

// ReadValue returns io.EOF when the stream is exhausted.
func (s *StreamDecoder) ReadValue() (Value, error) {
	var raw json.RawMessage
	if err := s.dec.Decode(&raw); err != nil {
		return Value{}, err
	}
	return ParseJSONValue(raw)
}
