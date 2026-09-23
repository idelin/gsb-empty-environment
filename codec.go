package mtf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// UnknownPolicy selects how the encoder treats symbols outside the alphabet.
type UnknownPolicy int

const (
	// PolicyReject rejects unknown symbols with ErrUnknownSymbol.
	PolicyReject UnknownPolicy = iota
	// PolicyEscapeUpdate emits an explicit in-band escape token carrying the
	// literal symbol; both encoder and decoder then extend their alphabets
	// identically. No symbol is ever inserted privately by one side.
	PolicyEscapeUpdate
)

// Token is one unit of the encoded stream. Exactly one of the forms applies:
//   - Literal == false: Index is an MTF rank in [0, alphabetLen).
//   - Literal == true:  Index == EscapeIndex(alphabetLen) and Sym carries the
//     literal symbol being introduced by the escape-update protocol.
type Token struct {
	Index   int
	Literal bool
	Sym     rune
}

// EscapeIndex returns the reserved index signaling an escape for a given
// current alphabet length. It always equals the length, so both sides track
// it implicitly as the alphabet grows.
func EscapeIndex(alphabetLen int) int { return alphabetLen }

// Encoder turns a symbol stream into tokens, updating its MTF model.
type Encoder struct {
	model  *Model
	policy UnknownPolicy
}

// NewEncoder builds an encoder over a fresh model for the alphabet.
func NewEncoder(a *Alphabet, policy UnknownPolicy) *Encoder {
	return &Encoder{model: NewModel(a), policy: policy}
}

// Model exposes the encoder's model for reset/checkpoint at block boundaries.
func (e *Encoder) Model() *Model { return e.model }

// Encode encodes one symbol, returning the tokens produced (one for known
// symbols; an escape token plus a literal token under PolicyEscapeUpdate).
func (e *Encoder) Encode(sym rune) ([]Token, error) {
	if _, ok := e.model.Alphabet().ID(sym); !ok {
		switch e.policy {
		case PolicyReject:
			return nil, ErrUnknownSymbol
		case PolicyEscapeUpdate:
			esc := EscapeIndex(e.model.Alphabet().Len())
			e.model.grow(sym)
			return []Token{
				{Index: esc},
				{Index: int(sym), Literal: true, Sym: sym},
			}, nil
		default:
			return nil, fmt.Errorf("mtf: invalid unknown-symbol policy %d", e.policy)
		}
	}
	idx, err := e.model.Encode(sym)
	if err != nil {
		return nil, err
	}
	return []Token{{Index: idx}}, nil
}

// Decoder turns tokens back into symbols, mirroring the encoder's model.
type Decoder struct {
	model *Model
}

// NewDecoder builds a decoder over a fresh model for the alphabet.
func NewDecoder(a *Alphabet) *Decoder {
	return &Decoder{model: NewModel(a)}
}

// Model exposes the decoder's model for reset/checkpoint at block boundaries.
func (d *Decoder) Model() *Model { return d.model }

// Decode consumes one token and returns the decoded symbol. Escape tokens
// are rejected here: after an escape, the literal token must be passed to
// DecodeLiteral. Use DecodeStream for whole sequences.
func (d *Decoder) Decode(tok Token) (rune, error) {
	if tok.Literal {
		return d.DecodeLiteral(tok.Sym)
	}
	if tok.Index == EscapeIndex(d.model.Alphabet().Len()) {
		return 0, fmt.Errorf("mtf: escape token must be followed by a literal token")
	}
	return d.model.Decode(tok.Index)
}

// DecodeLiteral applies the escape-update protocol on the decoder side:
// the literal symbol is appended to the alphabet and placed at the front,
// exactly mirroring the encoder.
func (d *Decoder) DecodeLiteral(sym rune) (rune, error) {
	if _, ok := d.model.Alphabet().ID(sym); ok {
		return 0, fmt.Errorf("mtf: literal %q already in alphabet (protocol violation)", sym)
	}
	d.model.grow(sym)
	return sym, nil
}

// DecodeStream decodes a full token sequence, handling escapes inline.
func (d *Decoder) DecodeStream(toks []Token) ([]rune, error) {
	out := make([]rune, 0, len(toks))
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if !tok.Literal && tok.Index == EscapeIndex(d.model.Alphabet().Len()) {
			if i+1 >= len(toks) || !toks[i+1].Literal {
				return nil, fmt.Errorf("mtf: escape at token %d not followed by literal", i)
			}
			i++
			sym, err := d.DecodeLiteral(toks[i].Sym)
			if err != nil {
				return nil, err
			}
			out = append(out, sym)
			continue
		}
		sym, err := d.Decode(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, nil
}

// Block container format (all integers uvarint, payload tokens uvarint):
//
//	magic        4 bytes  "MTF1"
//	formatVer    uvarint  (= FormatVersion)
//	alphabetVer  uvarint  alphabet version at block start
//	tokenCount   uvarint  number of tokens in payload
//	payloadLen   uvarint  payload byte length
//	payload      bytes    token values; literal tokens are stored as
//	                      escapeIndex+1 followed by the rune value
//	crc32        4 bytes  IEEE checksum of the payload bytes
const (
	magic         = "MTF1"
	FormatVersion = 1
)

var (
	ErrBadMagic       = errors.New("mtf: bad block magic")
	ErrBadVersion     = errors.New("mtf: unsupported format version")
	ErrBadLength      = errors.New("mtf: block length mismatch")
	ErrBadChecksum    = errors.New("mtf: block checksum mismatch")
	ErrOutputTooLarge = errors.New("mtf: decoded output exceeds quota")
)

// EncodeBlock serializes tokens into the versioned, checksummed block format.
func EncodeBlock(alphabetVer uint32, toks []Token) ([]byte, error) {
	var payload []byte
	var scratch [binary.MaxVarintLen64]byte
	put := func(v uint64) {
		n := binary.PutUvarint(scratch[:], v)
		payload = append(payload, scratch[:n]...)
	}
	for _, tok := range toks {
		if tok.Literal {
			// The preceding plain token already carries the escape index;
			// a literal token contributes only its rune value.
			put(uint64(tok.Sym))
		} else {
			put(uint64(tok.Index))
		}
	}
	var buf bytes.Buffer
	buf.WriteString(magic)
	hdr := func(v uint64) {
		n := binary.PutUvarint(scratch[:], v)
		buf.Write(scratch[:n])
	}
	hdr(FormatVersion)
	hdr(uint64(alphabetVer))
	hdr(uint64(len(toks)))
	hdr(uint64(len(payload)))
	buf.Write(payload)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
	buf.Write(crc[:])
	return buf.Bytes(), nil
}

// DecodeBlock parses and verifies a block, then decodes its tokens against
// the decoder's current model. Magic, format version, declared lengths and
// the CRC32 checksum are all verified before any token is applied.
// maxOutput bounds the number of decoded symbols; exceeding it is an error
// and no partial output is returned.
func (d *Decoder) DecodeBlock(block []byte, maxOutput int) ([]rune, error) {
	if len(block) < len(magic) || string(block[:len(magic)]) != magic {
		return nil, ErrBadMagic
	}
	rest := block[len(magic):]
	get := func() (uint64, error) {
		v, n := binary.Uvarint(rest)
		if n <= 0 {
			return 0, ErrBadLength
		}
		rest = rest[n:]
		return v, nil
	}
	ver, err := get()
	if err != nil {
		return nil, err
	}
	if ver != FormatVersion {
		return nil, ErrBadVersion
	}
	if _, err := get(); err != nil { // alphabetVer: informational, model is authoritative
		return nil, err
	}
	tokenCount, err := get()
	if err != nil {
		return nil, err
	}
	payloadLen, err := get()
	if err != nil {
		return nil, err
	}
	if payloadLen > uint64(len(rest))-4 || uint64(len(rest))-4 != payloadLen {
		return nil, ErrBadLength
	}
	payload, crcBytes := rest[:payloadLen], rest[payloadLen:]
	if len(crcBytes) != 4 {
		return nil, ErrBadLength
	}
	if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(crcBytes) {
		return nil, ErrBadChecksum
	}
	// Parse and apply tokens in a single pass: the escape index depends on
	// the current alphabet length, which grows as literals are applied, so
	// parsing cannot be separated from decoding. The model is rolled back
	// to the checkpoint if the block proves invalid midway.
	cp := d.model.Checkpoint()
	fail := func(err error) ([]rune, error) {
		d.model.Restore(cp)
		return nil, err
	}
	var out []rune
	values := 0
	for len(payload) > 0 {
		v, n := binary.Uvarint(payload)
		if n <= 0 {
			return fail(ErrBadLength)
		}
		payload = payload[n:]
		values++
		var sym rune
		if int(v) == EscapeIndex(d.model.Alphabet().Len()) {
			lit, n := binary.Uvarint(payload)
			if n <= 0 {
				return fail(ErrBadLength)
			}
			payload = payload[n:]
			values++
			sym, err = d.DecodeLiteral(rune(lit))
		} else {
			sym, err = d.model.Decode(int(v))
		}
		if err != nil {
			return fail(err)
		}
		out = append(out, sym)
		if len(out) > maxOutput {
			return fail(ErrOutputTooLarge)
		}
	}
	if uint64(values) != tokenCount {
		return fail(ErrBadLength)
	}
	return out, nil
}
