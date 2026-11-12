package netsec

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// maxZlibDictionarySize is the largest history window representable by the
// zlib format.
const maxZlibDictionarySize = 32 << 10

type zlibCodec struct{}

func (zlibCodec) Compress(src, dictionary []byte) ([]byte, error) {
	if len(dictionary) > maxZlibDictionarySize {
		return nil, fmt.Errorf("netsec: zlib dictionary is %d bytes, maximum is %d", len(dictionary), maxZlibDictionarySize)
	}

	var output bytes.Buffer
	var writer *zlib.Writer
	var err error
	if len(dictionary) == 0 {
		writer, err = zlib.NewWriterLevel(&output, zlib.BestCompression)
	} else {
		writer, err = zlib.NewWriterLevelDict(&output, zlib.BestCompression, dictionary)
	}
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(src); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (zlibCodec) Decompress(src, dictionary []byte, maxLen int) ([]byte, error) {
	if maxLen < 0 {
		return nil, errors.New("netsec: negative decompression limit")
	}
	if len(dictionary) > maxZlibDictionarySize {
		return nil, fmt.Errorf("netsec: zlib dictionary is %d bytes, maximum is %d", len(dictionary), maxZlibDictionarySize)
	}

	var reader io.ReadCloser
	var err error
	if len(dictionary) == 0 {
		reader, err = zlib.NewReader(bytes.NewReader(src))
	} else {
		reader, err = zlib.NewReaderDict(bytes.NewReader(src), dictionary)
	}
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// Reading maxLen+1 bytes lets us distinguish an exact match from a stream
	// that expands beyond the declared length without buffering the whole
	// expansion.
	output, err := io.ReadAll(io.LimitReader(reader, int64(maxLen)+1))
	if err != nil {
		return nil, err
	}
	if len(output) > maxLen {
		return nil, ErrUncompressedTooLarge
	}
	return output, nil
}
