package netsec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidateLocation validates an external-initializer location.
//
// A location is a slash-separated sequence of UTF-8 path segments. It must be
// relative: empty segments, "." and "..", absolute paths, NUL bytes,
// backslashes, drive-letter prefixes, and percent escapes are rejected.
// Rejecting percent escapes (rather than decoding them) prevents alternate
// spellings of dot segments from reaching the filesystem.
func ValidateLocation(location string) error {
	_, err := splitLocation(location)
	return err
}

func splitLocation(location string) ([]string, error) {
	if location == "" {
		return nil, fmt.Errorf("%w: empty location", ErrInvalidLocation)
	}
	if !utf8.ValidString(location) {
		return nil, fmt.Errorf("%w: location is not valid UTF-8", ErrInvalidLocation)
	}
	if strings.IndexByte(location, 0) >= 0 {
		return nil, fmt.Errorf("%w: location contains NUL", ErrInvalidLocation)
	}
	if strings.Contains(location, `\`) {
		return nil, fmt.Errorf("%w: location contains a backslash", ErrInvalidLocation)
	}
	if strings.Contains(location, "%") {
		return nil, fmt.Errorf("%w: location contains a percent escape", ErrInvalidLocation)
	}
	if strings.HasPrefix(location, "/") {
		return nil, fmt.Errorf("%w: absolute location", ErrInvalidLocation)
	}
	if len(location) >= 2 && location[1] == ':' &&
		((location[0] >= 'a' && location[0] <= 'z') || (location[0] >= 'A' && location[0] <= 'Z')) {
		return nil, fmt.Errorf("%w: drive-letter location", ErrInvalidLocation)
	}

	parts := strings.Split(location, "/")
	for _, part := range parts {
		switch part {
		case "":
			return nil, fmt.Errorf("%w: empty path segment", ErrInvalidLocation)
		case ".", "..":
			return nil, fmt.Errorf("%w: dot path segment", ErrInvalidLocation)
		}
		// A NUL can only appear inside a segment, but checking here also
		// guards future callers that construct segments directly.
		if strings.IndexByte(part, 0) >= 0 {
			return nil, fmt.Errorf("%w: NUL in path segment", ErrInvalidLocation)
		}
	}
	return parts, nil
}
