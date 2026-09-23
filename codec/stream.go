package codec

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"sfm/freq"
)

// Format identification. The version byte is explicit; decoders reject
// any other version.
const (
	Version      = 0x01
	flagOrder0   = 0x00
	flagOrder1   = 0x01
	InitContext  = 0x00 // context used for the first symbol of a stream
	checksumSize = 4
)

var magic = []byte{'S', 'F', 'M'}

// Decoding errors (explicit, distinguishable).
var (
	ErrBadMagic       = errors.New("codec: bad magic")
	ErrBadVersion     = errors.New("codec: unsupported format version")
	ErrBadFlags       = errors.New("codec: unknown model flags")
	ErrChecksum       = errors.New("codec: header checksum mismatch")
	ErrTruncated      = errors.New("codec: truncated stream")
	ErrTrailingData   = errors.New("codec: trailing garbage after payload")
	ErrOutputTooLarge = errors.New("codec: declared output exceeds decode limit")
	ErrBadHeader      = errors.New("codec: malformed header")
)

// groupPairs is one coding group: a context and its code lengths.
type groupSpec struct {
	ctx     byte
	lengths []uint8
}

// weightsPlusOne returns count+1 weights for the whole alphabet, so
// every symbol in the alphabet receives a code (explicit escape
// strategy for zero-frequency / unknown symbols).
func weightsPlusOne(alpha int, count func(sym byte) uint64) []uint64 {
	w := make([]uint64, alpha)
	for s := 0; s < alpha; s++ {
		w[s] = count(byte(s)) + 1
	}
	return w
}

// EncodeOrder0 encodes data with an order-0 model. The model must
// cover data (every symbol < model.Alpha); symbols outside the
// declared alphabet are rejected with ErrUnknownSymbol.
func EncodeOrder0(m *freq.Model, data []byte) ([]byte, error) {
	for _, s := range data {
		if int(s) >= m.Alpha {
			return nil, ErrUnknownSymbol
		}
	}
	lengths := codeLengths(weightsPlusOne(m.Alpha, m.Count))
	groups := []groupSpec{{ctx: 0, lengths: lengths}}

	hdr := marshalHeader(flagOrder0, m.Alpha, uint64(len(data)), groups)
	cc := newCanonicalCode(lengths)
	w := &bitWriter{}
	for _, s := range data {
		w.writeBits(cc.codes[s], int(cc.lengths[s]))
	}
	return append(hdr, w.bytes()...), nil
}

// EncodeConditional encodes data with an order-1 model. The context of
// symbol i is symbol i-1 (InitContext for i=0). Contexts sharing a
// merged table in the model emit identical code lengths, which is
// correct by construction. A context missing from the model is an
// error (explicit rejection).
func EncodeConditional(cm *freq.CondModel, data []byte) ([]byte, error) {
	for _, s := range data {
		if int(s) >= cm.Alpha {
			return nil, ErrUnknownSymbol
		}
	}
	var groups []groupSpec
	codes := make(map[byte]*canonicalCode)
	for _, ctx := range cm.Contexts() {
		c := ctx
		lengths := codeLengths(weightsPlusOne(cm.Alpha, func(sym byte) uint64 {
			return cm.Count(c, sym)
		}))
		groups = append(groups, groupSpec{ctx: ctx, lengths: lengths})
		codes[ctx] = newCanonicalCode(lengths)
	}
	hdr := marshalHeader(flagOrder1, cm.Alpha, uint64(len(data)), groups)
	w := &bitWriter{}
	prev := byte(InitContext)
	for _, s := range data {
		cc := codes[prev]
		if cc == nil {
			return nil, ErrBadHeader // context never observed: explicit rejection
		}
		w.writeBits(cc.codes[s], int(cc.lengths[s]))
		prev = s
	}
	return append(hdr, w.bytes()...), nil
}

func marshalHeader(flags byte, alpha int, n uint64, groups []groupSpec) []byte {
	var hdr []byte
	hdr = append(hdr, magic...)
	hdr = append(hdr, Version, flags)
	var tmp [binary.MaxVarintLen64]byte
	putUvarint := func(v uint64) {
		k := binary.PutUvarint(tmp[:], v)
		hdr = append(hdr, tmp[:k]...)
	}
	putUvarint(uint64(alpha))
	putUvarint(n)
	putUvarint(uint64(len(groups)))
	for _, g := range groups {
		putUvarint(uint64(g.ctx))
		hdr = append(hdr, g.lengths...)
	}
	crc := crc32.ChecksumIEEE(hdr)
	var c [checksumSize]byte
	binary.BigEndian.PutUint32(c[:], crc)
	hdr = append(hdr, c[:]...)
	return hdr
}

// Decode parses a stream produced by EncodeOrder0 or EncodeConditional
// and returns the original bytes. maxOut bounds the decoded output:
// if the declared symbol count exceeds maxOut, Decode fails with
// ErrOutputTooLarge before decoding anything.
func Decode(blob []byte, maxOut int) ([]byte, error) {
	if len(blob) < len(magic)+2 {
		return nil, ErrTruncated
	}
	if blob[0] != magic[0] || blob[1] != magic[1] || blob[2] != magic[2] {
		return nil, ErrBadMagic
	}
	if blob[3] != Version {
		return nil, ErrBadVersion
	}
	flags := blob[4]
	if flags != flagOrder0 && flags != flagOrder1 {
		return nil, ErrBadFlags
	}
	rest := blob[5:]
	readUvarint := func() (uint64, error) {
		v, k := binary.Uvarint(rest)
		if k <= 0 {
			return 0, ErrTruncated
		}
		rest = rest[k:]
		return v, nil
	}
	alpha, err := readUvarint()
	if err != nil {
		return nil, err
	}
	if alpha < 1 || alpha > 256 {
		return nil, ErrBadHeader
	}
	n, err := readUvarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(maxOut) {
		return nil, ErrOutputTooLarge
	}
	ngroups, err := readUvarint()
	if err != nil {
		return nil, err
	}
	if ngroups < 1 || ngroups > 256 {
		return nil, ErrBadHeader
	}
	// Header length is bounded by what remains; each group needs at
	// least 1 (ctx) + alpha (lengths) bytes plus the trailing checksum.
	if uint64(len(rest)) < ngroups*(1+alpha)+checksumSize {
		return nil, ErrTruncated
	}
	codes := make(map[byte]*canonicalCode)
	for g := uint64(0); g < ngroups; g++ {
		ctx, err := readUvarint()
		if err != nil {
			return nil, err
		}
		if ctx > 255 {
			return nil, ErrBadHeader
		}
		if len(rest) < int(alpha) {
			return nil, ErrTruncated
		}
		lengths := make([]uint8, alpha)
		copy(lengths, rest[:alpha])
		rest = rest[alpha:]
		for _, l := range lengths {
			if l == 0 || int(l) > maxCodeLen {
				return nil, ErrBadHeader
			}
		}
		codes[byte(ctx)] = newCanonicalCode(lengths)
	}
	if len(rest) < checksumSize {
		return nil, ErrTruncated
	}
	headerEnd := len(blob) - len(rest) + checksumSize
	want := binary.BigEndian.Uint32(rest[:checksumSize])
	if crc32.ChecksumIEEE(blob[:headerEnd-checksumSize]) != want {
		return nil, ErrChecksum
	}
	payload := rest[checksumSize:]

	out := make([]byte, 0, n)
	r := newBitReader(payload)
	prev := byte(InitContext)
	for i := uint64(0); i < n; i++ {
		var cc *canonicalCode
		if flags == flagOrder1 {
			cc = codes[prev]
		} else {
			cc = codes[0] // order-0 streams carry exactly one group, ctx 0
		}
		if cc == nil {
			return nil, ErrBadHeader
		}
		sym, err := decodeSymbol(r, cc)
		if err != nil {
			return nil, err
		}
		out = append(out, sym)
		prev = sym
	}
	// After the declared number of symbols, only sub-byte zero padding
	// may remain; anything else is trailing garbage.
	if r.remainingBits() >= 8 {
		return nil, ErrTrailingData
	}
	for r.remainingBits() > 0 {
		b, _ := r.read()
		if b != 0 {
			return nil, ErrTrailingData
		}
	}
	return out, nil
}

// decodeSymbol reads one canonical Huffman symbol.
func decodeSymbol(r *bitReader, cc *canonicalCode) (byte, error) {
	code := uint32(0)
	for l := 1; l <= cc.maxLen; l++ {
		b, ok := r.read()
		if !ok {
			return 0, ErrTruncated
		}
		code = code<<1 | uint32(b)
		for s, cl := range cc.lengths {
			if int(cl) == l && cc.codes[s] == code {
				return byte(s), nil
			}
		}
	}
	return 0, ErrBadHeader
}
