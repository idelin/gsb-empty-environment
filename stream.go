package dzblock

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Reader decodes a block stream produced with AppendStreamHeader and
// AppendBlock. Blocks are independent: a corrupt block never prevents
// locating the next one (see Resync), and no decoder state carries over
// between blocks.
type Reader struct {
	r        *bufio.Reader
	dict     []byte
	dictID   [32]byte
	pending  *BlockHeader // header found by Resync, not yet consumed
	produced int64

	// MaxOutput bounds the total number of decompressed bytes this
	// Reader will produce across all blocks. Zero means no output.
	MaxOutput int64
}

// NewReader reads and validates the stream header and binds the Reader
// to dict. A stream written for a different dictionary is rejected.
func NewReader(r io.Reader, dict []byte, maxOutput int64) (*Reader, error) {
	br := bufio.NewReader(r)
	hdr := make([]byte, StreamHeaderSize)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(hdr[0:4]) != streamMagic {
		return nil, ErrBadMagic
	}
	if hdr[4] != Version {
		return nil, ErrBadVersion
	}
	if crc32.Checksum(hdr[:45], castagnoli) != binary.LittleEndian.Uint32(hdr[45:49]) {
		return nil, ErrCorrupt
	}
	id := DictID(dict)
	if string(hdr[5:37]) != string(id[:]) {
		return nil, ErrDictMismatch
	}
	return &Reader{r: br, dict: dict, dictID: id, MaxOutput: maxOutput}, nil
}

// ReadBlock decodes the next block. It returns io.EOF at a clean stream
// end. On any other error the stream position is unspecified; call
// Resync to locate the following independent block.
func (rd *Reader) ReadBlock() ([]byte, error) {
	hdr := rd.pending
	rd.pending = nil
	if hdr == nil {
		// Peek instead of read: on a parse failure the bytes stay in
		// the stream so Resync can re-scan them for the next header.
		buf, err := rd.r.Peek(BlockHeaderSize)
		if err != nil {
			if len(buf) == 0 {
				return nil, io.EOF // clean end at a block boundary
			}
			return nil, ErrCorrupt // truncated header at stream end
		}
		h, err := ParseBlockHeader(buf)
		if err != nil {
			return nil, err
		}
		if _, err := rd.r.Discard(BlockHeaderSize); err != nil {
			return nil, err
		}
		hdr = h
	}
	if hdr.DictID != rd.dictID {
		return nil, ErrDictMismatch
	}
	if rd.produced+int64(hdr.RawLen) > rd.MaxOutput {
		return nil, ErrOutputLimit
	}
	payload := make([]byte, hdr.EncLen)
	if _, err := io.ReadFull(rd.r, payload); err != nil {
		return nil, ErrCorrupt
	}
	if crc32.Checksum(payload, castagnoli) != hdr.PayloadCRC {
		return nil, ErrCorrupt
	}
	out, err := lzDecode(nil, rd.dict, payload, int(hdr.RawLen))
	if err != nil {
		return nil, err
	}
	rd.produced += int64(len(out))
	return out, nil
}

// Resync scans forward for the next valid block header (magic plus
// header CRC), so a corrupt block does not make the following
// independent block unlocatable. It returns io.EOF if no further block
// header exists before the end of the stream.
func (rd *Reader) Resync() error {
	rd.pending = nil
	var window [4]byte
	filled := 0
	var refeed []byte // bytes of a rejected candidate header, re-scanned
	next := func() (byte, error) {
		if len(refeed) > 0 {
			b := refeed[0]
			refeed = refeed[1:]
			return b, nil
		}
		return rd.r.ReadByte()
	}
	for {
		b, err := next()
		if err != nil {
			return io.EOF
		}
		window[0], window[1], window[2], window[3] = window[1], window[2], window[3], b
		if filled < 4 {
			filled++
			if filled < 4 {
				continue
			}
		}
		if binary.LittleEndian.Uint32(window[:]) != blockMagic {
			continue
		}
		cand := make([]byte, 0, BlockHeaderSize)
		cand = append(cand, window[:]...)
		for len(cand) < BlockHeaderSize {
			rb, err := next()
			if err != nil {
				return io.EOF
			}
			cand = append(cand, rb)
		}
		if hdr, err := ParseBlockHeader(cand); err == nil {
			rd.pending = hdr
			return nil
		}
		// False magic: re-scan the candidate's tail for a real header.
		refeed = append(refeed, cand[4:]...)
	}
}
