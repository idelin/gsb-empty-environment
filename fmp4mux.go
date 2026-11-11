// Package fmp4mux implements a fragmented MP4 (fMP4) muxer for H.264/AVC
// access units together with a parser that reads the generated initialization
// and media segments back.
//
// The muxer emits an initialization segment (ftyp/moov with mvex/trex) and,
// per fragment, a media segment made of styp, sidx, moof and mdat boxes.
// Fragments are forced to start on IDR samples; trun carries per-sample
// duration, size, flags and composition time offset, using signed (version 1)
// offsets when B frames are present.
package fmp4mux

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// ErrFragmentStart is returned when a fragment would not start with an IDR.
var ErrFragmentStart = errors.New("fmp4mux: fragment must start with an IDR sample")

// ErrClosed is returned when samples are pushed after Close.
var ErrClosed = errors.New("fmp4mux: fragmenter is closed")

// ErrNegativePTS is returned when a fragment's earliest presentation time is
// negative; sidx version 1 stores it as an unsigned 64-bit value.
var ErrNegativePTS = errors.New("fmp4mux: earliest presentation time is negative")

// Sample is one H.264 access unit in decode order.
type Sample struct {
	DTS uint64 // decode time in the movie timescale
	CTO int32  // composition time offset; presentation time is DTS + CTO
	AU  []byte // access unit: Annex B or 4-byte-length-prefixed NAL units
	Key bool   // IDR / sync sample
}

// Config configures the muxer. SPS/PPS are raw NAL units (no start code);
// when omitted they are extracted from the first IDR access unit.
type Config struct {
	Timescale      uint32
	Width          uint16
	Height         uint16
	SPS            []byte
	PPS            []byte
	SequenceNumber uint32 // first fragment sequence number; defaults to 1
}

// bw is a big-endian box payload writer. Writes on bytes.Buffer never fail.
type bw struct {
	bytes.Buffer
}

func (w *bw) u8(v uint8)      { w.WriteByte(v) }
func (w *bw) u16(v uint16)    { var b [2]byte; binary.BigEndian.PutUint16(b[:], v); w.Write(b[:]) }
func (w *bw) u24(v uint32)    { w.Write([]byte{byte(v >> 16), byte(v >> 8), byte(v)}) }
func (w *bw) u32(v uint32)    { var b [4]byte; binary.BigEndian.PutUint32(b[:], v); w.Write(b[:]) }
func (w *bw) u64(v uint64)    { var b [8]byte; binary.BigEndian.PutUint64(b[:], v); w.Write(b[:]) }
func (w *bw) b(v []byte)      { w.Write(v) }
func (w *bw) fourcc(v string) { w.Write([]byte(v)) }

// makeBox wraps payload into a plain box.
func makeBox(typ string, payload []byte) []byte {
	out := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(out, uint32(8+len(payload)))
	copy(out[4:8], typ)
	return append(out, payload...)
}

// makeFullBox wraps inner payload into a full box (version + flags header).
func makeFullBox(typ string, version uint8, flags uint32, inner []byte) []byte {
	payload := make([]byte, 4, 4+len(inner))
	payload[0] = version
	payload[1] = byte(flags >> 16)
	payload[2] = byte(flags >> 8)
	payload[3] = byte(flags)
	payload = append(payload, inner...)
	return makeBox(typ, payload)
}

// container builds a plain container box from child boxes.
func container(typ string, children ...[]byte) []byte {
	var p []byte
	for _, c := range children {
		p = append(p, c...)
	}
	return makeBox(typ, p)
}
