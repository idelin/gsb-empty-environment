package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseTable parses device-mapper table text. Each non-empty, non-comment line
// must contain a start sector, a length in sectors, a target type, and at least
// one target parameter.
func ParseTable(raw string) ([]Segment, error) {
	lines := strings.Split(raw, "\n")
	segments := make([]Segment, 0, len(lines))

	for i, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("%w: line %d: expected start, length, target, and parameters", ErrMalformedTable, i+1)
		}

		start, err := parseUint(fields[0])
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: invalid start sector %q", ErrMalformedTable, i+1, fields[0])
		}
		length, err := parseUint(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: invalid length %q", ErrMalformedTable, i+1, fields[1])
		}

		segments = append(segments, Segment{
			Start:      start,
			Length:     length,
			TargetType: fields[2],
			Params:     append([]string(nil), fields[3:]...),
		})
	}

	if len(segments) == 0 {
		return nil, ErrEmptyTable
	}
	return segments, nil
}

func parseUint(value string) (uint64, error) {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseUint(value, 10, 64)
}
