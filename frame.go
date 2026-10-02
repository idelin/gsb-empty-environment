package main

// Frame layout (50 bytes, byte-aligned):
//
//   +----------------+----------------+-----------------------------+
//   | sync word (4B) | symbol count |  Huffman payload (44 B,       |
//   | A5 5A A5 5A    | (2 B, BE)    |  zero-padded to byte)         |
//   +----------------+----------------+-----------------------------+
//
// Sync words therefore sit at byte offsets 0, 50, 100, 150, ... of the
// encoded stream. A decoder that loses phase (bit flip / dropped bits)
// scans forward bit-by-bit for the sync pattern and re-locks there,
// confining the damage to the frames between failure and resync.

var SyncPattern = []byte{0xA5, 0x5A, 0xA5, 0x5A}

const (
	SyncLen      = 4 // len(SyncPattern)
	CountBytes   = 2
	PayloadBytes = 44
	HeaderBytes  = SyncLen + CountBytes       // 6
	FrameBytes   = HeaderBytes + PayloadBytes // 50
	PayloadBits  = PayloadBytes * 8           // 352
)

func syncWordBits() uint32 {
	return uint32(SyncPattern[0])<<24 | uint32(SyncPattern[1])<<16 |
		uint32(SyncPattern[2])<<8 | uint32(SyncPattern[3])
}

// EncodedStream is the encoded bitstream plus bookkeeping used to
// measure how much content survives a corruption.
type EncodedStream struct {
	Data          []byte
	Codes         map[byte]code
	FrameSymbols  []int // symbols carried by each frame
	FrameStartSym []int // index of the first symbol of each frame
}

// SymbolsFromFrame returns how many original symbols live in frames i..N.
func (e *EncodedStream) SymbolsFromFrame(i int) int {
	total := 0
	for ; i < len(e.FrameSymbols); i++ {
		total += e.FrameSymbols[i]
	}
	return total
}

// EncodeFrames packs data into sync-delimited frames.
func EncodeFrames(data []byte, codes map[byte]code) *EncodedStream {
	es := &EncodedStream{Codes: codes}
	var out []byte
	sym := 0
	for sym < len(data) {
		es.FrameStartSym = append(es.FrameStartSym, sym)
		w := bitWriter{}
		n := 0
		for sym < len(data) {
			c := codes[data[sym]]
			if w.BitLen()+c.length > PayloadBits {
				break
			}
			w.WriteBits(c.bits, c.length)
			sym++
			n++
		}
		if n == 0 {
			panic("symbol does not fit in an empty frame")
		}
		payload := w.Bytes()
		out = append(out, SyncPattern...)
		out = append(out, byte(n>>8), byte(n))
		out = append(out, payload...)
		for pad := PayloadBytes - len(payload); pad > 0; pad-- {
			out = append(out, 0)
		}
		es.FrameSymbols = append(es.FrameSymbols, n)
	}
	es.Data = out
	return es
}

// Decoder events.
const (
	EventSyncLost  = "sync-lost"
	EventResync    = "resync"
	EventTruncated = "truncated"
	EventAbort     = "abort"
)

// Event records a fault-tolerant decoder state transition.
type Event struct {
	Kind   string
	BitPos int
	Msg    string
}

// Decode status of a single frame payload.
const (
	decodeOK       = iota
	decodeOverflow // consumed more than PayloadBits: stream is corrupt
	decodeUnderrun // ran out of stream
)

func decodeSymbols(r *bitReader, root *decodeNode, count, budget int, out *[]byte) int {
	start := r.pos
	for i := 0; i < count; i++ {
		n := root
		for !n.leaf {
			b, ok := r.ReadBit()
			if !ok {
				return decodeUnderrun
			}
			n = n.children[int(b)]
			if r.pos-start > budget {
				return decodeOverflow
			}
		}
		*out = append(*out, n.sym)
	}
	return decodeOK
}

// plausibleHeader reports whether a sync+count pair at bit pos looks valid.
func plausibleHeader(r *bitReader, pos, minLen int) bool {
	v, ok := r.PeekBits(pos, 32)
	if !ok || v != syncWordBits() {
		return false
	}
	cnt, ok := r.PeekBits(pos+32, 16)
	if !ok || cnt < 1 || int(cnt)*minLen > PayloadBits {
		return false
	}
	return pos+HeaderBytes*8+int(cnt)*minLen <= r.BitLen()
}

// scanSync searches bit-by-bit for the next plausible sync word.
func scanSync(r *bitReader, from, minLen int) (int, bool) {
	for p := from; p+HeaderBytes*8 <= r.BitLen(); p++ {
		if plausibleHeader(r, p, minLen) {
			return p, true
		}
	}
	return 0, false
}

// DecodeFaultTolerant decodes a framed stream, re-locking on the next
// sync word whenever the frame structure is violated.
func DecodeFaultTolerant(stream []byte, codes map[byte]code) ([]byte, []Event) {
	root := buildDecodeTree(codes)
	minLen, _ := codeLenBounds(codes)
	r := &bitReader{data: stream}
	var out []byte
	var events []Event

	resync := func(from int) bool {
		p, ok := scanSync(r, from, minLen)
		if !ok {
			events = append(events, Event{EventAbort, r.BitLen(), "no further sync word; rest of stream abandoned"})
			return false
		}
		events = append(events, Event{EventResync, p, "re-locked on sync word"})
		r.pos = p
		return true
	}

	for r.pos+HeaderBytes*8 <= r.BitLen() {
		if !plausibleHeader(r, r.pos, minLen) {
			events = append(events, Event{EventSyncLost, r.pos, "sync word mismatch; scanning forward"})
			// Dropped bits shift the next sync word to BEFORE its
			// expected position, so rescan from just after the
			// previous frame's start rather than from here.
			from := r.pos - FrameBytes*8 + 1
			if from < 0 {
				from = 0
			}
			if !resync(from) {
				break
			}
			continue
		}
		frameStart := r.pos
		cnt, _ := r.PeekBits(r.pos+32, 16)
		r.pos += HeaderBytes * 8
		switch decodeSymbols(r, root, int(cnt), PayloadBits, &out) {
		case decodeUnderrun:
			events = append(events, Event{EventTruncated, r.pos, "stream ends mid-frame"})
			return out, events
		case decodeOverflow:
			events = append(events, Event{EventSyncLost, frameStart, "frame payload overruns budget; stream corrupt"})
			if !resync(frameStart + 1) {
				return out, events
			}
			continue
		}
		// Frames are fixed-size: skip any zero padding to the next frame.
		r.pos = frameStart + FrameBytes*8
	}
	return out, events
}

// DecodeNaive is the ordinary decoder: it trusts the frame layout
// blindly and never verifies or recovers synchronization.
func DecodeNaive(stream []byte, codes map[byte]code) []byte {
	root := buildDecodeTree(codes)
	r := &bitReader{data: stream}
	var out []byte
	for r.pos+HeaderBytes*8 <= r.BitLen() {
		frameStart := r.pos
		r.pos += 32 // skip assumed sync word, unchecked
		cnt, ok := r.ReadBits(16)
		if !ok {
			break
		}
		if decodeSymbols(r, root, int(cnt), PayloadBits, &out) == decodeUnderrun {
			break
		}
		r.pos = frameStart + FrameBytes*8
	}
	return out
}

// DeleteBits simulates dropped bits on an unreliable channel by
// removing n bits starting at bitPos from the stream.
func DeleteBits(data []byte, bitPos, n int) []byte {
	r := bitReader{data: data}
	w := bitWriter{}
	for i := 0; i < r.BitLen(); i++ {
		b, _ := r.ReadBit()
		if i >= bitPos && i < bitPos+n {
			continue
		}
		w.WriteBits(b, 1)
	}
	return w.Bytes()
}

// positionMatch returns the fraction of equal bytes when a and b are
// compared position-by-position over their common length.
func positionMatch(a, b []byte) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	same := 0
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			same++
		}
	}
	return float64(same) / float64(n)
}
