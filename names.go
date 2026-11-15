package netsec

import (
	"errors"
	"strings"
)

// DNS wire-name limits from RFC 1035 section 2.3.4.
const (
	maxLabelLen    = 63
	maxNameWireLen = 255
)

var (
	// ErrMalformedName is returned when a name cannot be parsed as a
	// sequence of valid presentation-format labels.
	ErrMalformedName = errors.New("netsec: malformed domain name")
	// ErrRootName is returned when an operation needs a non-root name.
	ErrRootName = errors.New("netsec: root name has no labels")
)

// splitLabels splits a presentation-format domain name into its labels,
// without the empty root label. The root ("." or "") yields an empty slice
// and no error. Names are compared case-insensitively in DNS, so the
// returned labels retain their original spelling; callers that need
// canonical comparison must lower-case them explicitly.
func splitLabels(name string) ([]string, error) {
	if name == "" || name == "." {
		return nil, nil
	}

	trimmed := strings.TrimSuffix(name, ".")
	if trimmed == "" {
		return nil, nil
	}

	labels := strings.Split(trimmed, ".")

	// Each label contributes its octets plus one length octet on the wire.
	wireLen := 1 // terminating zero-length root label.
	for index, label := range labels {
		if label == "" {
			return nil, ErrMalformedName
		}
		if len(label) > maxLabelLen {
			return nil, ErrMalformedName
		}
		if strings.Contains(label, "*") {
			// RFC 4592 section 2.1.1: "*" is a label on its own and may
			// only appear as the leftmost label.
			if label != "*" || index != 0 {
				return nil, ErrMalformedName
			}
		}
		wireLen += len(label) + 1
	}
	if wireLen > maxNameWireLen {
		return nil, ErrMalformedName
	}
	return labels, nil
}

// LabelCount returns the number of labels in name, excluding the root label.
// For example both "a.b.example." and "a.b.example" return 4.
func LabelCount(name string) (int, error) {
	labels, err := splitLabels(name)
	if err != nil {
		return 0, err
	}
	return len(labels), nil
}

// joinName joins labels into a presentation-format name terminated by ".".
func joinName(labels []string) string {
	if len(labels) == 0 {
		return "."
	}
	return strings.Join(labels, ".") + "."
}

// lowerLabels returns a copy of labels folded to lower case.
func lowerLabels(labels []string) []string {
	out := make([]string, len(labels))
	for i, label := range labels {
		out[i] = strings.ToLower(label)
	}
	return out
}
