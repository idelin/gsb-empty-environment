package sintcol

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"math"
)

type Writer struct {
	w        io.Writer
	cfg      config
	closed   bool
	ordinal  int
	total    uint64
	entries  []indexEntry
	offset   int64
	lastVal  int64
	lastDiff int64
	hasValue bool
	hasDiff  bool
}

func NewWriter(w io.Writer, opts ...Option) (*Writer, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	n, err := w.Write([]byte(magic))
	if err != nil {
		return nil, err
	}
	return &Writer{w: w, cfg: cfg, offset: int64(n)}, nil
}

func (w *Writer) WrittenElements() int64 {
	if w.total > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(w.total)
}

func (w *Writer) Write(values []int64) (int, error) {
	return w.WriteBatch(values, EncZigZag, len(values))
}

func (w *Writer) WriteBatch(values []int64, enc Encoding, blockSize int) (int, error) {
	if blockSize <= 0 {
		blockSize = len(values)
	}
	written := 0
	for len(values) > 0 {
		n := blockSize
		if n > len(values) {
			n = len(values)
		}
		if _, err := w.WriteBlock(enc, values[:n], false, 0); err != nil {
			return written, err
		}
		written += n
		values = values[n:]
	}
	return written, nil
}

func (w *Writer) WriteBlock(enc Encoding, values []int64, chained bool, firstDelta int64) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	if len(values) == 0 || enc > EncDeltaDelta {
		return 0, ErrCorrupt
	}
	if uint64(len(values)) > uint64(w.cfg.maxBlockElements) {
		return 0, ErrTooLarge
	}
	if w.total+uint64(len(values)) < w.total || w.total+uint64(len(values)) > uint64(w.cfg.maxTotalElements) {
		return 0, ErrTooLarge
	}
	if chained && !w.hasValue {
		return 0, ErrBrokenChain
	}
	if chained && enc == EncDeltaDelta && len(values) >= 2 && !w.hasDiff {
		return 0, ErrBrokenChain
	}

	baseline := values[0]
	if chained {
		baseline = w.lastVal
	}
	payload, lastValue, lastDiff, hasDiff, err := encodePayload(
		enc, values, chained, baseline, w.lastDiff, firstDelta)
	if err != nil {
		return 0, err
	}
	if len(payload) > w.cfg.maxBlockPayload {
		return 0, ErrTooLarge
	}

	flags := uint8(enc) << 1
	if chained {
		flags |= flagChain
	}
	fields := make([]byte, 0, maxHeaderBytes)
	fields = appendUvarint(fields, uint64(w.ordinal))
	fields = appendUvarint(fields, w.total)
	fields = appendUvarint(fields, uint64(len(values)))
	fields = appendUvarint(fields, uint64(len(payload)))
	fields = appendZigzag(fields, baseline)
	if !chained && enc == EncDeltaDelta && len(values) >= 2 {
		fields = appendZigzag(fields, firstDelta)
	}

	totalHeader := 3 + len(fields)
	for {
		next := 2 + uvarintLen(uint64(totalHeader)) + len(fields)
		if next == totalHeader {
			break
		}
		totalHeader = next
	}
	header := make([]byte, 0, totalHeader)
	header = append(header, blockMarker, flags)
	header = appendUvarint(header, uint64(totalHeader))
	header = append(header, fields...)

	h := crc32.NewIEEE()
	h.Write(header)
	h.Write(payload)
	var checksum [4]byte
	binary.LittleEndian.PutUint32(checksum[:], h.Sum32())

	frameStart := w.offset
	n, err := w.w.Write(header)
	if err != nil {
		return 0, err
	}
	w.offset += int64(n)
	if n, err = w.w.Write(payload); err != nil {
		return 0, err
	}
	w.offset += int64(n)
	if n, err = w.w.Write(checksum[:]); err != nil {
		return 0, err
	}
	w.offset += int64(n)

	w.entries = append(w.entries, indexEntry{
		firstIndex: w.total,
		offset:     frameStart,
		count:      uint64(len(values)),
		enc:        enc,
		chained:    chained,
	})
	w.total += uint64(len(values))
	w.ordinal++
	w.lastVal = lastValue
	w.hasValue = true
	w.lastDiff = lastDiff
	w.hasDiff = hasDiff
	return len(values), nil
}

func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	w.closed = true

	entries := make([]byte, 0)
	for _, entry := range w.entries {
		entries = appendUvarint(entries, entry.firstIndex)
		entries = appendUvarint(entries, uint64(entry.offset))
		entries = appendUvarint(entries, entry.count)
		entries = append(entries, entryFlags(entry))
	}
	footerSize := len(entries) + uvarintLen(uint64(len(entries)+1))
	entries = appendUvarint(entries, uint64(footerSize))

	var checksum [4]byte
	binary.LittleEndian.PutUint32(checksum[:], crc32.ChecksumIEEE(entries))
	if _, err := w.w.Write([]byte{indexMarker}); err != nil {
		return err
	}
	if _, err := w.w.Write(checksum[:]); err != nil {
		return err
	}
	if _, err := w.w.Write(entries); err != nil {
		return err
	}
	_, err := w.w.Write([]byte(indexMagic))
	return err
}

func encodePayload(enc Encoding, values []int64, chained bool, baseline, prevDiff, firstDelta int64) ([]byte, int64, int64, bool, error) {
	payload := make([]byte, 0, len(values)*5)
	var lastDiff int64
	hasDiff := false

	switch enc {
	case EncZigZag:
		for _, value := range values {
			payload = appendZigzag(payload, value)
		}
	case EncDelta:
		prev := baseline
		for _, value := range values {
			diff, ok := subChecked(value, prev)
			if !ok {
				return nil, 0, 0, false, ErrCorrupt
			}
			payload = appendZigzag(payload, diff)
			lastDiff = diff
			prev = value
		}
		hasDiff = len(values) >= 2
	case EncDeltaDelta:
		current := baseline
		diff := firstDelta
		if chained && len(values) >= 2 {
			diff = prevDiff
		}
		for i, value := range values {
			if i == 0 {
				if value != current {
					return nil, 0, 0, false, ErrCorrupt
				}
				payload = appendZigzag(payload, 0)
				continue
			}
			nextDiff, ok := subChecked(value, current)
			if !ok {
				return nil, 0, 0, false, ErrCorrupt
			}
			change, ok := subChecked(nextDiff, diff)
			if !ok {
				return nil, 0, 0, false, ErrCorrupt
			}
			payload = appendZigzag(payload, change)
			current = value
			diff = nextDiff
			lastDiff = nextDiff
		}
		hasDiff = len(values) >= 2
	default:
		return nil, 0, 0, false, ErrCorrupt
	}
	return payload, values[len(values)-1], lastDiff, hasDiff, nil
}
