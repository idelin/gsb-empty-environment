package zipguard

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"sort"
)

// VerifyBytes validates an in-memory archive and returns its report. All
// structural, naming, mode and quota checks are performed before success is
// returned; the first failure aborts validation.
func VerifyBytes(b []byte, policy Policy) (*Report, error) {
	p, err := policy.normalized()
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > p.MaxArchiveSize {
		return nil, fmt.Errorf("%w: archive is %d bytes (limit %d)", ErrUnsupported, len(b), p.MaxArchiveSize)
	}

	eocdRec, err := findEOCD(b)
	if err != nil {
		return nil, err
	}
	cds, err := parseCentralDirectory(b, eocdRec, p)
	if err != nil {
		return nil, err
	}

	// EOCD (and, for zip64, the locator and zip64 EOCD) occupy the tail.
	ivs := make([]interval, 0, len(cds)+3)
	if eocdRec.zip64 {
		locStart := eocdRec.offset - z64locFixed
		z64Start := locStart - z64eocdFixed
		ivs = append(ivs,
			interval{start: z64Start, end: locStart, kind: "zip64-eocd"},
			interval{start: locStart, end: eocdRec.offset, kind: "zip64-locator"},
		)
	}
	ivs = append(ivs, interval{
		start: eocdRec.offset,
		end:   eocdRec.offset + eocdFixedSize + int64(eocdRec.commentLength),
		kind:  "eocd",
	})

	parsed := make([]*lfh, len(cds))
	cdStart := int64(eocdRec.cdOffset)
	if eocdRec.zip64 {
		cdStart = int64(eocdRec.z64CDOffset)
	}
	for i, c := range cds {
		if c.uncompressedSize > uint64(policy.MaxEntryUncompressed) ||
			c.uncompressedSize > uint64(policy.MaxTotalUncompressed) {
			return nil, fmt.Errorf("%w: %q declares %d uncompressed bytes",
				ErrEntryTooLarge, c.name, c.uncompressedSize)
		}
		if int64(c.localOffset) >= cdStart {
			return nil, fmt.Errorf("%w: %q LFH offset %d outside local file area",
				ErrBadSignature, c.name, c.localOffset)
		}
		l, iv, err := parseLFH(b, c)
		if err != nil {
			return nil, err
		}
		parsed[i] = l
		ivs = append(ivs, iv)
		// The CDH itself, including name, extra field and comment, must also
		// be covered by the byte map.
		cdhLen := cdhFixedSize + int64(len(c.name)) + int64(len(c.extra)) + int64(len(c.comment))
		ivs = append(ivs, interval{
			start: c.headerOffset,
			end:   c.headerOffset + cdhLen,
			kind:  "cdh:" + c.name,
		})
	}

	sort.Slice(ivs, func(i, j int) bool {
		if ivs[i].start == ivs[j].start {
			return ivs[i].end < ivs[j].end
		}
		return ivs[i].start < ivs[j].start
	})
	if err := checkCoverage(ivs, int64(len(b))); err != nil {
		return nil, err
	}

	// Name and mode admission, including normalized-name collision detection.
	seen := make(map[string]string, len(cds))
	report := &Report{Entries: make([]Entry, 0, len(cds))}
	for _, c := range cds {
		norm, isDir, err := validateName(c.name)
		if err != nil {
			return nil, err
		}
		if other, dup := seen[norm]; dup {
			return nil, fmt.Errorf("%w: %q collides with %q (key %q)",
				ErrDuplicateName, c.name, other, norm)
		}
		seen[norm] = c.name

		mode, err := validateMode(c, p)
		if err != nil {
			return nil, err
		}
		report.Entries = append(report.Entries, Entry{
			Name:             c.name,
			NormalizedName:   norm,
			IsDir:            isDir,
			Method:           c.method,
			CRC32:            c.crc32,
			CompressedSize:   c.compressedSize,
			UncompressedSize: c.uncompressedSize,
			ExternalAttrs:    c.externalAttrs,
			UnixMode:         mode,
		})
	}

	// Streamed, quota-accounted payload verification.
	for i, c := range cds {
		actual, err := verifyPayload(b, parsed[i], c, p, report.TotalUncompressed)
		if err != nil {
			return nil, err
		}
		if uint64(actual) != c.uncompressedSize {
			return nil, fmt.Errorf("%w: %q produced %d bytes, header says %d",
				ErrSizeMismatch, c.name, actual, c.uncompressedSize)
		}
		report.TotalUncompressed += actual
		report.TotalCompressed += int64(c.compressedSize)

		if c.compressedSize >= uint64(p.RatioFloorBytes) {
			ratio := float64(actual) / float64(c.compressedSize)
			if ratio > p.MaxCompressionRatio {
				return nil, fmt.Errorf("%w: %q ratio %.1f exceeds %.1f",
					ErrCompressionBomb, c.name, ratio, p.MaxCompressionRatio)
			}
			if ratio > report.MaxObservedRatio {
				report.MaxObservedRatio = ratio
			}
		}
	}
	return report, nil
}

// VerifyReader consumes r into a bounded buffer and validates it. The reader
// is never trusted for size: the buffer is capped by MaxArchiveSize.
func VerifyReader(r io.Reader, policy Policy) (*Report, error) {
	p, err := policy.normalized()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	limited := io.LimitReader(r, p.MaxArchiveSize+1)
	n, err := io.Copy(&buf, limited)
	if err != nil {
		return nil, err
	}
	if n > p.MaxArchiveSize {
		return nil, fmt.Errorf("%w: archive exceeds %d bytes", ErrUnsupported, p.MaxArchiveSize)
	}
	return VerifyBytes(buf.Bytes(), p)
}

// quotaWriter counts actual decompressed bytes, enforces the per-entry and
// archive-wide quotas mid-stream, and feeds the CRC-32 hash simultaneously.
type quotaWriter struct {
	hash       hash.Hash32
	entryLimit int64
	totalLimit int64
	totalUsed  int64
	written    int64
}

func (w *quotaWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.written+int64(n) > w.entryLimit {
		return 0, fmt.Errorf("%w: entry limit %d exceeded", ErrEntryTooLarge, w.entryLimit)
	}
	if w.totalUsed+w.written+int64(n) > w.totalLimit {
		return 0, fmt.Errorf("%w: total limit %d exceeded", ErrTotalTooLarge, w.totalLimit)
	}
	w.hash.Write(p)
	w.written += int64(n)
	return n, nil
}

// verifyPayload streams one entry's payload, counting the bytes the
// decompressor actually emits and validating the CRC-32 against the CDH.
func verifyPayload(b []byte, l *lfh, c *cdh, p Policy, totalUsed int64) (int64, error) {
	payload := b[l.dataOffset : l.dataOffset+int64(c.compressedSize)]
	writer := &quotaWriter{
		hash:       crc32.NewIEEE(),
		entryLimit: p.MaxEntryUncompressed,
		totalLimit: p.MaxTotalUncompressed,
		totalUsed:  totalUsed,
	}

	switch c.method {
	case methodStored:
		if _, err := writer.Write(payload); err != nil {
			return 0, err
		}
	case methodDeflate:
		fr := flate.NewReader(bytes.NewReader(payload))
		n, copyErr := io.Copy(writer, fr)
		closeErr := fr.Close()
		if copyErr != nil {
			if errors.Is(copyErr, ErrEntryTooLarge) || errors.Is(copyErr, ErrTotalTooLarge) {
				return 0, copyErr
			}
			return 0, fmt.Errorf("%w: %q invalid deflate stream: %v", ErrBadSignature, c.name, copyErr)
		}
		if closeErr != nil {
			return 0, fmt.Errorf("%w: %q invalid deflate stream: %v", ErrBadSignature, c.name, closeErr)
		}
		_ = n
	default:
		return 0, fmt.Errorf("%w: method %d", ErrUnsupported, c.method)
	}

	if writer.hash.Sum32() != c.crc32 {
		return 0, fmt.Errorf("%w: %q expected %#08x got %#08x",
			ErrCRC, c.name, c.crc32, writer.hash.Sum32())
	}
	return writer.written, nil
}
