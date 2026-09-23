package lcc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

var magic = []byte{'L', 'C', 'C', '1'}

type layerRecord struct {
	codec      uint16
	encodedLen uint32
	decodedLen uint32
}

func makeHeader(layerCount int, payloadLen uint64) ([]byte, error) {
	if layerCount < 1 || layerCount > maxLayerCount {
		return nil, fmt.Errorf("layer count %d is outside [1,%d]", layerCount, maxLayerCount)
	}
	if payloadLen > maxPayloadSize {
		return nil, fmt.Errorf("payload length %d exceeds uint32 length field", payloadLen)
	}
	header := make([]byte, headerSize)
	copy(header[0:4], magic)
	binary.BigEndian.PutUint16(header[4:6], FormatVersion)
	binary.BigEndian.PutUint16(header[6:8], uint16(layerCount))
	binary.BigEndian.PutUint32(header[8:12], uint32(payloadLen))
	binary.BigEndian.PutUint32(header[12:16], crc32.ChecksumIEEE(header[:12]))
	return header, nil
}

func marshalRecords(records []layerRecord) []byte {
	body := make([]byte, len(records)*recordSize)
	for i, record := range records {
		offset := i * recordSize
		binary.BigEndian.PutUint16(body[offset:offset+2], record.codec)
		binary.BigEndian.PutUint32(body[offset+4:offset+8], record.encodedLen)
		binary.BigEndian.PutUint32(body[offset+8:offset+12], record.decodedLen)
	}
	return body
}

func parseRecords(body []byte, layerCount int) ([]layerRecord, error) {
	if len(body) != layerCount*recordSize {
		return nil, errors.New("invalid layer table length")
	}
	records := make([]layerRecord, layerCount)
	for i := range records {
		offset := i * recordSize
		if binary.BigEndian.Uint16(body[offset+2:offset+4]) != 0 ||
			binary.BigEndian.Uint32(body[offset+12:offset+16]) != 0 {
			return nil, fmt.Errorf("layer %d has non-zero reserved field", i)
		}
		records[i] = layerRecord{
			codec:      binary.BigEndian.Uint16(body[offset : offset+2]),
			encodedLen: binary.BigEndian.Uint32(body[offset+4 : offset+8]),
			decodedLen: binary.BigEndian.Uint32(body[offset+8 : offset+12]),
		}
	}
	return records, nil
}

// Records are stored outermost-first. The input budget is the sum of every
// encoded layer consumed while decoding; the output budget is the sum of
// every intermediate and final decoded layer.
func checkPlan(l Limits, records []layerRecord, payloadLen uint64) error {
	if len(records) < 1 || len(records) > l.MaxLayers {
		return fmt.Errorf("layer count %d is outside [1,%d]", len(records), l.MaxLayers)
	}
	if payloadLen != uint64(records[0].encodedLen) {
		return fmt.Errorf("payload length %d does not match outer encoded length %d",
			payloadLen, records[0].encodedLen)
	}

	var inputTotal, outputTotal uint64
	for i, record := range records {
		if record.codec != codecRLE {
			return fmt.Errorf("layer %d uses unsupported codec %d", i, record.codec)
		}
		if (record.encodedLen == 0) != (record.decodedLen == 0) {
			return fmt.Errorf("layer %d has inconsistent zero length", i)
		}
		if !withinRatio(uint64(record.decodedLen), uint64(record.encodedLen), l.MaxExpansionBasisPoints) {
			return fmt.Errorf("layer %d expansion exceeds configured basis-point ratio", i)
		}
		if i+1 < len(records) && record.decodedLen != records[i+1].encodedLen {
			return fmt.Errorf("layer %d output does not match next layer input", i)
		}
		inputTotal += uint64(record.encodedLen)
		outputTotal += uint64(record.decodedLen)
	}

	originalEncoded := uint64(records[0].encodedLen)
	finalDecoded := uint64(records[len(records)-1].decodedLen)
	if !withinRatio(finalDecoded, originalEncoded, l.MaxExpansionBasisPoints) {
		return errors.New("cumulative expansion exceeds configured basis-point ratio")
	}
	if inputTotal > uint64(l.MaxInputBytes) {
		return fmt.Errorf("shared input budget exceeded: %d > %d", inputTotal, l.MaxInputBytes)
	}
	if outputTotal > uint64(l.MaxOutputBytes) {
		return fmt.Errorf("shared output budget exceeded: %d > %d", outputTotal, l.MaxOutputBytes)
	}
	return nil
}
