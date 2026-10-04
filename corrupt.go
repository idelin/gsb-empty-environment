package pcf

// This file provides independent stream constructors used by tests and by
// the CLI to produce corrupted archives. None of them mutate their input.

// FlipBit returns a copy of b with a single bit flipped at byte offset off.
// bit 0 is the least significant bit of the byte.
func FlipBit(b []byte, off int, bit uint) []byte {
	out := append([]byte(nil), b...)
	out[off] ^= 1 << bit
	return out
}

// InsertFakeMagic splices the frame magic, followed by filler bytes, into
// the stream at offset off. The result looks like it may contain a frame
// header but (short of a CRC32 accident) none of it is a valid frame.
func InsertFakeMagic(b []byte, off int, filler ...byte) []byte {
	out := make([]byte, 0, len(b)+len(Magic)+len(filler))
	out = append(out, b[:off]...)
	out = append(out, Magic...)
	out = append(out, filler...)
	out = append(out, b[off:]...)
	return out
}

// InsertTruncatedHead splices the first headLen bytes of a valid frame
// (encoding data with the given sequence number) into the stream at off.
// The spliced bytes form a plausible but incomplete frame header.
func InsertTruncatedHead(b []byte, off int, seq uint32, data []byte, headLen int) []byte {
	frame := EncodeFrame(seq, data)
	if headLen > len(frame) {
		headLen = len(frame)
	}
	out := make([]byte, 0, len(b)+headLen)
	out = append(out, b[:off]...)
	out = append(out, frame[:headLen]...)
	out = append(out, b[off:]...)
	return out
}

// CorruptCodeTable returns a copy of the stream in which the code table of
// the frame starting at frameOff is replaced by an oversubscribed (and
// therefore structurally invalid) set of code lengths.
func CorruptCodeTable(b []byte, frameOff int) []byte {
	out := append([]byte(nil), b...)
	for i := 0; i < TableSize; i++ {
		out[frameOff+TableOff+i] = 1
	}
	return out
}
