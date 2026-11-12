package avroocf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

func (r *Reader) readHeader() error {
	prefix, err := readFull(r.input, len(magic))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotContainer, err)
	}
	if !bytes.Equal(prefix, magic[:]) {
		return ErrNotContainer
	}

	metadata, err := readMapHeader(r.input)
	if err != nil {
		return fmt.Errorf("%w: header metadata: %v", ErrMalformed, err)
	}
	schemaJSON, ok := metadata["avro.schema"]
	if !ok {
		return fmt.Errorf("%w: missing avro.schema metadata", ErrMalformed)
	}
	if int64(len(schemaJSON)) > r.config.MaxSchemaBytes {
		return fmt.Errorf("%w: schema exceeds %d bytes", ErrMalformed, r.config.MaxSchemaBytes)
	}
	schema, err := ParseSchema(schemaJSON, r.config.MaxSchemaDepth)
	if err != nil {
		return err
	}

	codec := "null"
	if rawCodec, present := metadata["avro.codec"]; present {
		codec = string(rawCodec)
	}
	if codec != "null" {
		if codec != "deflate" || !r.config.AllowDeflate || r.config.MaxDecompressedBlockBytes <= 0 {
			return fmt.Errorf("%w: %q", ErrCodecUnsupported, codec)
		}
	}

	fingerprint := r.config.Fingerprint(schema)
	if !r.config.AllowedFingerprints[fingerprint] {
		return fmt.Errorf("%w: fingerprint %s", ErrSchemaRejected, fingerprint)
	}

	syncBytes, err := readFull(r.input, syncSize)
	if err != nil {
		return fmt.Errorf("%w: short sync marker: %v", ErrMalformed, err)
	}
	copy(r.sync[:], syncBytes)

	r.schema = schema
	r.metadata = metadata
	r.blockMinSize = minEncodedSize(schema)
	return nil
}

func readMapHeader(r *countedReader) (map[string][]byte, error) {
	result := make(map[string][]byte)
	var totalEntries int64
	for {
		entryCount, err := readLong(r)
		if err != nil {
			return nil, err
		}
		if entryCount == 0 {
			if totalEntries > 1<<20 {
				return nil, fmt.Errorf("map entry count exceeds limit")
			}
			return result, nil
		}
		if entryCount < 0 {
			blockSize, err := readLong(r)
			if err != nil {
				return nil, err
			}
			if blockSize < 0 {
				return nil, fmt.Errorf("negative map block size")
			}
			entryCount = -entryCount
		}
		totalEntries += entryCount
		for index := int64(0); index < entryCount; index++ {
			key, err := readString(r)
			if err != nil {
				return nil, err
			}
			value, err := readBytes(r)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
	}
}

func readLong(r io.Reader) (int64, error) {
	var unsigned uint64
	var shift uint
	for position := 0; position < binary.MaxVarintLen64; position++ {
		var byteValue [1]byte
		if _, err := io.ReadFull(r, byteValue[:]); err != nil {
			return 0, err
		}
		unsigned |= uint64(byteValue[0]&0x7f) << shift
		if byteValue[0]&0x80 == 0 {
			return int64(unsigned>>1) ^ -int64(unsigned&1), nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("%w: varint too long", ErrMalformed)
}

func decodeCount(r io.Reader, first byte) (int64, error) {
	var unsigned uint64
	var shift uint
	byteValue := first
	for position := 0; position < binary.MaxVarintLen64; position++ {
		unsigned |= uint64(byteValue&0x7f) << shift
		if byteValue&0x80 == 0 {
			return int64(unsigned>>1) ^ -int64(unsigned&1), nil
		}
		shift += 7
		var next [1]byte
		if _, err := io.ReadFull(r, next[:]); err != nil {
			return 0, err
		}
		byteValue = next[0]
	}
	return 0, fmt.Errorf("%w: varint too long", ErrMalformed)
}

func readInt(r io.Reader) (int32, error) {
	value, err := readLong(r)
	if err != nil {
		return 0, err
	}
	const int32Max = int64(^uint32(0) >> 1)
	const int32Min = -int32Max - 1
	if value < int32Min || value > int32Max {
		return 0, fmt.Errorf("%w: int %d out of range", ErrMalformed, value)
	}
	return int32(value), nil
}

func readString(r io.Reader) (string, error) {
	value, err := readBytes(r)
	return string(value), err
}

func readBytes(r io.Reader) ([]byte, error) {
	size, err := readLong(r)
	if err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: negative byte string length", ErrMalformed)
	}
	return readFull(r, int(size))
}
