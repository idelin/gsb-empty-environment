package rangecoder

import "errors"

var (
	// ErrQuotaExceeded is returned when decoding would produce more than
	// the configured output quota. It bounds the damage a corrupt or
	// hostile stream can do.
	ErrQuotaExceeded = errors.New("rangecoder: decode output quota exceeded")
	// ErrCorruptStream is returned when the stream decodes to an
	// impossible symbol value.
	ErrCorruptStream = errors.New("rangecoder: corrupt or truncated stream")
)

// DefaultQuota bounds decoded output size unless changed with SetQuota.
const DefaultQuota = 1 << 20

// Encoder compresses a sequence of segments. Each segment is an
// independent range-coded stream; the adaptive model carries across
// segments unless Reset is called. A model Snapshot taken at a segment
// boundary is sufficient to resume encoding or to decode later segments
// without reading earlier ones.
type Encoder struct {
	re *rangeEncoder
	m  *Model
}

func NewEncoder() *Encoder {
	return &Encoder{re: newRangeEncoder(), m: NewModel()}
}

// NewEncoderFromCheckpoint resumes encoding with the model restored from
// a checkpoint. The produced stream is byte-identical to a continuous run
// that followed the same segmenting policy.
func NewEncoderFromCheckpoint(cp []byte) (*Encoder, error) {
	m, err := ModelFromCheckpoint(cp)
	if err != nil {
		return nil, err
	}
	return &Encoder{re: newRangeEncoder(), m: m}, nil
}

// Reset discards the adaptive model and starts from uniform frequencies.
func (e *Encoder) Reset() { e.m = NewModel() }

func (e *Encoder) EncodeByte(b byte) {
	e.re.encode(e.m.cumFreq(int(b)), e.m.freq[b], e.m.total)
	e.m.update(int(b))
}

func (e *Encoder) Encode(p []byte) {
	for _, b := range p {
		e.EncodeByte(b)
	}
}

// Snapshot returns a verifiable checkpoint of the current model state.
func (e *Encoder) Snapshot() []byte { return e.m.Snapshot() }

// FinishSegment encodes the EOF symbol, flushes the range coder and
// returns the segment bytes. The model keeps adapting; the range coder
// restarts so the next segment is an independent stream.
func (e *Encoder) FinishSegment() []byte {
	e.re.encode(e.m.cumFreq(EOFSymbol), e.m.freq[EOFSymbol], e.m.total)
	e.m.update(EOFSymbol)
	e.re.flush()
	out := e.re.out
	e.re = newRangeEncoder()
	return out
}

// Decoder decodes one segment stream.
type Decoder struct {
	rd    *rangeDecoder
	m     *Model
	quota int
}

func NewDecoder(stream []byte) *Decoder {
	return &Decoder{rd: newRangeDecoder(stream), m: NewModel(), quota: DefaultQuota}
}

// NewDecoderFromCheckpoint decodes a segment using the model restored
// from a checkpoint, giving random access to later segments without
// decoding (or even reading) earlier ones.
func NewDecoderFromCheckpoint(cp, stream []byte) (*Decoder, error) {
	m, err := ModelFromCheckpoint(cp)
	if err != nil {
		return nil, err
	}
	return &Decoder{rd: newRangeDecoder(stream), m: m, quota: DefaultQuota}, nil
}

// SetQuota bounds the number of bytes ReadAll may return.
func (d *Decoder) SetQuota(n int) { d.quota = n }

// NextSegment switches decoding to the next segment stream while keeping
// the adaptive model, mirroring Encoder.FinishSegment. Use it for
// sequential decoding; use NewDecoderFromCheckpoint for random access.
func (d *Decoder) NextSegment(stream []byte) {
	d.rd = newRangeDecoder(stream)
}

// ReadAll decodes until the EOF symbol, the quota, or a stream error.
func (d *Decoder) ReadAll() ([]byte, error) {
	var out []byte
	for {
		if len(out) >= d.quota {
			return nil, ErrQuotaExceeded
		}
		total := d.m.total
		v := d.rd.value(total)
		if v >= total {
			return nil, ErrCorruptStream
		}
		sym, cum := d.m.symbolFor(v)
		if sym < 0 {
			return nil, ErrCorruptStream
		}
		d.rd.update(cum, d.m.freq[sym], total)
		d.m.update(sym)
		if sym == EOFSymbol {
			return out, nil
		}
		out = append(out, byte(sym))
	}
}
