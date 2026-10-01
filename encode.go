package canon

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

// Wire format (all multi-byte lengths are unsigned varints):
//
//	'N'                          null
//	'B' 0x00|0x01                bool
//	'I' 8-byte big-endian        int64
//	'F' 8-byte big-endian IEEE754 float64 (finite only; -0.0 normalized to +0.0)
//	'S' uvarint len, bytes       UTF-8 string
//	'A' uvarint count, elements  array (order preserved)
//	'O' uvarint count, entries   object; entries sorted by key bytes,
//	                             each entry: uvarint keylen, key, value
//	'G' uvarint n, uvarint root, n nodes  graph; nodes in canonical order,
//	                             each node: label value, uvarint out-degree,
//	                             out-degree target indices (uvarint)
const (
	tagNull   = 'N'
	tagBool   = 'B'
	tagInt    = 'I'
	tagFloat  = 'F'
	tagString = 'S'
	tagArray  = 'A'
	tagObject = 'O'
	tagGraph  = 'G'
)

// Options controls encoding limits.
type Options struct {
	// SearchBudget bounds the total number of partition-refinement
	// rounds and individualization search nodes used while
	// canonicalizing graphs. When the budget is exhausted, encoding
	// fails with ErrBudgetExhausted (explicitly incomplete) rather
	// than returning a non-canonical result. Zero means
	// DefaultSearchBudget.
	SearchBudget int64
}

// DefaultSearchBudget is the default graph canonicalization budget.
const DefaultSearchBudget int64 = 1 << 16

// ErrBudgetExhausted reports that graph canonicalization did not
// complete within the configured search budget.
var ErrBudgetExhausted = errors.New("canon: graph canonicalization incomplete: search budget exhausted")

// Encode returns the canonical encoding of v using default options.
func Encode(v Value) ([]byte, error) { return EncodeWith(v, Options{}) }

// EncodeWith returns the canonical encoding of v using the given options.
func EncodeWith(v Value, opts Options) ([]byte, error) {
	budget := opts.SearchBudget
	if budget <= 0 {
		budget = DefaultSearchBudget
	}
	var buf []byte
	if err := encodeValue(&buf, v, &budget); err != nil {
		return nil, err
	}
	return buf, nil
}

// Digest returns the SHA-256 digest of the canonical encoding of v.
// It is intended as a stable signing input; no signature algorithm is
// implemented here.
func Digest(v Value) ([32]byte, error) {
	enc, err := Encode(v)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(enc), nil
}

func encodeValue(buf *[]byte, v Value, budget *int64) error {
	switch v.kind {
	case KindNull:
		*buf = append(*buf, tagNull)
	case KindBool:
		if v.bval {
			*buf = append(*buf, tagBool, 1)
		} else {
			*buf = append(*buf, tagBool, 0)
		}
	case KindInt:
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], uint64(v.ival))
		*buf = append(*buf, tagInt)
		*buf = append(*buf, tmp[:]...)
	case KindFloat:
		if math.IsNaN(v.fval) || math.IsInf(v.fval, 0) {
			return errors.New("canon: non-finite float (NaN/Inf) is not encodable")
		}
		f := v.fval
		if f == 0 {
			f = 0 // normalize -0.0 to +0.0
		}
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], math.Float64bits(f))
		*buf = append(*buf, tagFloat)
		*buf = append(*buf, tmp[:]...)
	case KindString:
		if !utf8.ValidString(v.sval) {
			return errors.New("canon: string is not valid UTF-8")
		}
		*buf = append(*buf, tagString)
		*buf = binary.AppendUvarint(*buf, uint64(len(v.sval)))
		*buf = append(*buf, v.sval...)
	case KindArray:
		*buf = append(*buf, tagArray)
		*buf = binary.AppendUvarint(*buf, uint64(len(v.aval)))
		for _, e := range v.aval {
			if err := encodeValue(buf, e, budget); err != nil {
				return err
			}
		}
	case KindObject:
		pairs := make([]Pair, len(v.oval))
		copy(pairs, v.oval)
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
		for i, p := range pairs {
			if !utf8.ValidString(p.Key) {
				return fmt.Errorf("canon: object key %q is not valid UTF-8", p.Key)
			}
			if i > 0 && pairs[i-1].Key == p.Key {
				return fmt.Errorf("canon: duplicate object key %q", p.Key)
			}
		}
		*buf = append(*buf, tagObject)
		*buf = binary.AppendUvarint(*buf, uint64(len(pairs)))
		for _, p := range pairs {
			*buf = binary.AppendUvarint(*buf, uint64(len(p.Key)))
			*buf = append(*buf, p.Key...)
			if err := encodeValue(buf, p.Value, budget); err != nil {
				return err
			}
		}
	case KindGraph:
		enc, err := canonicalizeGraph(v.gval, budget)
		if err != nil {
			return err
		}
		*buf = append(*buf, enc...)
	default:
		return fmt.Errorf("canon: unsupported value kind %d", int(v.kind))
	}
	return nil
}
