package layerpack

import "encoding/binary"

// Container layout (all integers big-endian):
//
//	magic    4 bytes  "LYRC"
//	version  1 byte   currently 1
//	nlayers  1 byte   number of layer records
//	record   nlayers * 22 bytes, in apply order (layer 1 first):
//	  codec    1 byte   codec identifier
//	  reserved 1 byte   must be 0
//	  compLen  8 bytes  compressed payload length of this layer
//	  rawLen   8 bytes  uncompressed length (this layer's decode output)
//	  crc32    4 bytes  CRC-32 (IEEE) of the uncompressed data
//	payloads concatenated in the same order as the records.
const (
	formatVersion = 1
	headerLen     = 6
	recordLen     = 22
)

var magic = [4]byte{'L', 'Y', 'R', 'C'}

type record struct {
	codec   byte
	compLen uint64
	rawLen  uint64
	crc     uint32
}

func appendRecord(b []byte, r record) []byte {
	b = append(b, r.codec, 0)
	b = binary.BigEndian.AppendUint64(b, r.compLen)
	b = binary.BigEndian.AppendUint64(b, r.rawLen)
	b = binary.BigEndian.AppendUint32(b, r.crc)
	return b
}

func parseRecord(b []byte) record {
	return record{
		codec:   b[0],
		compLen: binary.BigEndian.Uint64(b[2:10]),
		rawLen:  binary.BigEndian.Uint64(b[10:18]),
		crc:     binary.BigEndian.Uint32(b[18:22]),
	}
}
