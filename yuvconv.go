// Package yuvconv converts raw 4:2:0 YUV frames (I420, NV12 and 10-bit
// P010) into 8-bit sRGB images.
//
// Frames coming from different capture pipelines routinely disagree about
// whether the luma/chroma values use limited (TV) or full range and whether
// they were encoded with BT.601, BT.709 or BT.2020 coefficients. The caller
// describes the frame with [Params]; treating a limited-range BT.709 frame as
// full-range BT.601 is therefore no longer possible by accident.
package yuvconv

import "errors"

// Matrix identifies the Y'CbCr-to-RGB color matrix assumed by the frame.
type Matrix int

const (
	// MatrixBT601 is the ITU-R BT.601 matrix (standard-definition content).
	MatrixBT601 Matrix = iota
	// MatrixBT709 is the ITU-R BT.709 matrix (HD content).
	MatrixBT709
	// MatrixBT2020 is the ITU-R BT.2020 non-constant-luminance matrix (UHD content).
	MatrixBT2020
)

func (m Matrix) valid() bool {
	return m >= MatrixBT601 && m <= MatrixBT2020
}

// Range identifies the signal range of the luma and chroma planes.
type Range int

const (
	// RangeLimited is the TV/limited ("MPEG") range: luma 16..235 and
	// chroma 16..240 on an 8-bit scale, scaled proportionally for 10-bit.
	RangeLimited Range = iota
	// RangeFull is the PC/full ("JPEG") range: 0..255 on an 8-bit scale,
	// 0..1023 on a 10-bit scale.
	RangeFull
)

func (r Range) valid() bool {
	return r == RangeLimited || r == RangeFull
}

// ChromaLoc identifies the horizontal/vertical sampling position of the
// 4:2:0 chroma samples relative to the luma samples.
type ChromaLoc int

const (
	// ChromaLeft places each chroma sample on the upper-left luma sample of
	// its 2x2 block ("MPEG-1"/JPEG siting, chroma sample type 0).
	ChromaLeft ChromaLoc = iota
	// ChromaCenter places each chroma sample between the four luma samples of
	// its block ("MPEG-2" siting, chroma sample type 2); it is reconstructed
	// with bilinear interpolation, clamping at the frame borders.
	ChromaCenter
)

func (c ChromaLoc) valid() bool {
	return c == ChromaLeft || c == ChromaCenter
}

// Params describes how a frame is encoded and how it must be converted.
type Params struct {
	// Width and Height are the luma dimensions in pixels; odd values are
	// supported.
	Width  int
	Height int
	// Matrix selects the Y'CbCr-to-RGB coefficients.
	Matrix Matrix
	// Range selects limited (TV) or full (PC) signal range.
	Range Range
	// Chroma selects the chroma sample location used for upsampling.
	Chroma ChromaLoc
}

// I420Frame is a planar 4:2:0 frame: one Y plane followed by separate,
// contiguous U (Cb) and V (Cr) planes, each with chroma dimensions
// ceil(Width/2) x ceil(Height/2).
type I420Frame struct {
	Y, U, V []byte
	YStride int
	CStride int
}

// NV12Frame is a semi-planar 4:2:0 frame: a Y plane followed by an
// interleaved Cb/Cr plane whose rows are 2*ceil(Width/2) bytes wide.
type NV12Frame struct {
	Y, C    []byte
	YStride int
	CStride int
}

// P010Frame is the 10-bit semi-planar 4:2:0 layout used by most desktop
// capture APIs. Every component occupies the high 10 bits of a little-endian
// uint16 (the low 6 bits are zero); the Y plane and the interleaved Cb/Cr
// plane are padded to even sample counts.
type P010Frame struct {
	Y, C    []uint16
	YStride int // stride in uint16 samples
	CStride int // stride in uint16 samples
}

// ErrInvalidParams reports an unsupported parameter combination or a buffer
// that is too small for the declared geometry.
var ErrInvalidParams = errors.New("yuvconv: invalid frame parameters")
