package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// ParseMountPoints reads /proc/self/mountinfo formatted text and returns the
// decoded, deduplicated mount point paths.
//
// Each record is a space-separated sequence of fields. The optional fields end
// with a standalone "-" field and separate the leading fields from the
// filesystem type, device and super options. Only the mount point (the fifth
// field, index 4) is needed; path names encode space, tab, newline and
// backslash as three-digit octal escapes.
func ParseMountPoints(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)

	seen := make(map[string]struct{})
	var mountPoints []string

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Fields(line)

		sep := -1
		for i, field := range fields {
			if field == "-" {
				sep = i
				break
			}
		}
		if sep < 0 {
			return nil, fmt.Errorf("netsec: mountinfo line %d: missing optional-field separator \"-\"", lineNo)
		}
		if sep < 5 || sep+3 > len(fields) {
			return nil, fmt.Errorf("netsec: mountinfo line %d: malformed record", lineNo)
		}

		mountPoint, err := unescapeOctal(fields[4])
		if err != nil {
			return nil, fmt.Errorf("netsec: mountinfo line %d: mount point: %w", lineNo, err)
		}
		if !path.IsAbs(mountPoint) {
			return nil, fmt.Errorf("netsec: mountinfo line %d: mount point %q is not absolute", lineNo, mountPoint)
		}

		mountPoint = path.Clean(mountPoint)
		if _, ok := seen[mountPoint]; ok {
			continue
		}
		seen[mountPoint] = struct{}{}
		mountPoints = append(mountPoints, mountPoint)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("netsec: reading mountinfo: %w", err)
	}

	return mountPoints, nil
}

// unescapeOctal decodes the three-digit octal escaping used for mountinfo path
// names. Space, tab, newline and backslash appear as \040, \011, \012 and
// \134 respectively, but every backslash must introduce a valid octal escape.
func unescapeOctal(s string) (string, error) {
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}

	var builder strings.Builder
	builder.Grow(len(s))

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '\\' {
			builder.WriteByte(ch)
			continue
		}
		if i+3 >= len(s) {
			return "", errors.New("truncated octal escape")
		}

		var value byte
		for j := 1; j <= 3; j++ {
			digit := s[i+j]
			if digit < '0' || digit > '7' {
				return "", fmt.Errorf("invalid octal escape %q", s[i:i+4])
			}
			value = value*8 + (digit - '0')
		}
		builder.WriteByte(value)
		i += 3
	}

	return builder.String(), nil
}
