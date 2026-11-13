package netsec

import (
	"fmt"
	"math"
)

// Validate checks record counts, offsets, and candidate lengths without
// running the recovery search.
func Validate(records []Record) error {
	return validateRecords(records)
}

func validateRecords(records []Record) error {
	if len(records) > MaxRecords {
		return fmt.Errorf("netsec: %d records exceed maximum %d", len(records), MaxRecords)
	}
	for i, record := range records {
		if record.Offset < 0 {
			return fmt.Errorf("netsec: record %d (%q): negative offset %d", i, record.ID, record.Offset)
		}
		if len(record.Ciphertext) > math.MaxInt-record.Offset {
			return fmt.Errorf("netsec: record %d (%q): keystream interval overflows int", i, record.ID)
		}
		if len(record.Candidates) > MaxCandidates {
			return fmt.Errorf("netsec: record %d (%q): %d candidates exceed maximum %d", i, record.ID, len(record.Candidates), MaxCandidates)
		}
		for j, candidate := range record.Candidates {
			if len(candidate) != len(record.Ciphertext) {
				return fmt.Errorf(
					"netsec: record %d (%q): candidate %d has length %d, ciphertext has length %d",
					i, record.ID, j, len(candidate), len(record.Ciphertext),
				)
			}
		}
	}
	return nil
}
