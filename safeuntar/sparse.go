package safeuntar

import (
	"archive/tar"
	"fmt"
	"strconv"
	"strings"
)

// GNU PAX sparse header keywords, mirrored from archive/tar. Only the
// 0.0/0.1 map encoding is visible through the public Header.PAXRecords API;
// archive/tar folds repeated GNU.sparse.offset/numbytes records into a single
// GNU.sparse.map value while parsing.
const (
	paxGNUSparsePrefix    = "GNU.sparse."
	paxGNUSparseNumBlocks = "GNU.sparse.numblocks"
	paxGNUSparseMap       = "GNU.sparse.map"
	paxGNUSparseMajor     = "GNU.sparse.major"
	paxGNUSparseMinor     = "GNU.sparse.minor"
	paxGNUSparseSize      = "GNU.sparse.size"
	paxGNUSparseRealSize  = "GNU.sparse.realsize"
)

type sparseInterval struct {
	offset int64
	length int64
}

// validateDeclaredSparseMap parses the sparse intervals advertised in the
// entry's PAX records and checks that they are ordered, non-overlapping and
// contained in the declared logical size.
//
// The 1.0 map travels in the data stream and is not visible here; archive/tar
// validates it while reading, and the old GNU header map is handled entirely
// inside the standard library. In both cases io.Copy still enforces the
// declared-vs-actual byte consistency for the materialized file.
func validateDeclaredSparseMap(hdr *tar.Header) error {
	hasSparseRecord := false
	for key := range hdr.PAXRecords {
		if strings.HasPrefix(key, paxGNUSparsePrefix) {
			hasSparseRecord = true
			break
		}
	}
	if !hasSparseRecord {
		return nil
	}

	mapValue, hasMap := hdr.PAXRecords[paxGNUSparseMap]
	if !hasMap {
		// GNU.sparse 1.0 stores its map inside the data stream rather than in
		// PAX records. The standard library owns that validation.
		if hdr.PAXRecords[paxGNUSparseMajor] == "1" && hdr.PAXRecords[paxGNUSparseMinor] == "0" {
			return nil
		}
		return fmt.Errorf("%w: %q advertises GNU sparse headers without a usable map", ErrMalformedArchive, hdr.Name)
	}

	fields := strings.Split(mapValue, ",")
	if len(fields) == 0 || len(fields)%2 != 0 {
		return fmt.Errorf("%w: sparse map of %q has an odd number of fields", ErrMalformedArchive, hdr.Name)
	}
	if declared := hdr.PAXRecords[paxGNUSparseNumBlocks]; declared != "" {
		n, err := strconv.Atoi(declared)
		if err != nil || n < 0 || n != len(fields)/2 {
			return fmt.Errorf("%w: %q declares %s sparse blocks but map contains %d", ErrMalformedArchive, hdr.Name, declared, len(fields)/2)
		}
	}

	intervals := make([]sparseInterval, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		offset, err := strconv.ParseInt(fields[i], 10, 64)
		if err != nil {
			return fmt.Errorf("%w: sparse offset of %q: %v", ErrMalformedArchive, hdr.Name, err)
		}
		length, err := strconv.ParseInt(fields[i+1], 10, 64)
		if err != nil {
			return fmt.Errorf("%w: sparse length of %q: %v", ErrMalformedArchive, hdr.Name, err)
		}
		if offset < 0 || length <= 0 {
			return fmt.Errorf("%w: sparse interval of %q uses non-positive values", ErrMalformedArchive, hdr.Name)
		}
		intervals = append(intervals, sparseInterval{offset: offset, length: length})
	}

	if !validSparseIntervals(intervals, hdr.Size) {
		return fmt.Errorf("%w: sparse map of %q is out of order, overlaps or exceeds declared size %d", ErrMalformedArchive, hdr.Name, hdr.Size)
	}

	for _, sizeKey := range []string{paxGNUSparseSize, paxGNUSparseRealSize} {
		if value := hdr.PAXRecords[sizeKey]; value != "" {
			declared, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("%w: %s of %q: %v", ErrMalformedArchive, sizeKey, hdr.Name, err)
			}
			if declared != hdr.Size {
				return fmt.Errorf("%w: %s=%d disagrees with header size %d for %q", ErrMalformedArchive, sizeKey, declared, hdr.Size, hdr.Name)
			}
		}
	}
	return nil
}

// validSparseIntervals mirrors the checks performed by archive/tar: intervals
// must be sorted, strictly non-overlapping and fully inside [0, size).
func validSparseIntervals(intervals []sparseInterval, size int64) bool {
	var previousEnd int64
	for _, interval := range intervals {
		end := interval.offset + interval.length
		if interval.offset < previousEnd || end < interval.offset || end > size {
			return false
		}
		previousEnd = end
	}
	return true
}
