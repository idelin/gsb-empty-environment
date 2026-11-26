package nsec3

import "strings"

const maxLabelLen = 63
const maxNameLen = 255

// LowerName returns the canonical lowercase presentation form of a fully
// qualified domain name. The trailing dot is optional on input and always
// present on output; the root is returned as ".".
func LowerName(name string) (string, error) {
	labels, err := nameLabels(name)
	if err != nil {
		return "", err
	}
	if len(labels) == 0 {
		return ".", nil
	}
	return strings.Join(labels, ".") + ".", nil
}

// WireName encodes name into the canonical (lowercase, uncompressed) DNS
// wire format defined by RFC 1035 section 3.1 and RFC 4034 section 6.2.
// The trailing dot is optional; the root "." encodes to a single zero
// length octet.
func WireName(name string) ([]byte, error) {
	labels, err := nameLabels(name)
	if err != nil {
		return nil, err
	}
	wire := make([]byte, 0, len(name)+1)
	total := 0
	for _, label := range labels {
		if len(label) == 0 || len(label) > maxLabelLen {
			return nil, ErrInvalidName
		}
		total += len(label) + 1
		if total > maxNameLen {
			return nil, ErrInvalidName
		}
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
	}
	wire = append(wire, 0)
	return wire, nil
}

// nameLabels splits a presentation-format name into lowercase wire labels.
func nameLabels(name string) ([]string, error) {
	if name == "" {
		return nil, ErrInvalidName
	}
	// A trailing dot denotes the fully qualified form and is dropped
	// before splitting.
	trimmed := name
	if strings.HasSuffix(trimmed, ".") {
		trimmed = trimmed[:len(trimmed)-1]
		if trimmed == "" {
			return nil, nil // root
		}
	}
	parts := strings.Split(trimmed, ".")
	labels := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, ErrInvalidName
		}
		label := strings.ToLower(p)
		if len(label) > maxLabelLen {
			return nil, ErrInvalidName
		}
		labels = append(labels, label)
	}
	total := 0
	for _, label := range labels {
		total += len(label) + 1
	}
	if total > maxNameLen {
		return nil, ErrInvalidName
	}
	return labels, nil
}

// ancestors returns the strict ancestors of name from the closest (parent)
// to the furthest, each in canonical lowercase presentation form.
func ancestors(name string) ([]string, error) {
	labels, err := nameLabels(name)
	if err != nil {
		return nil, err
	}
	if len(labels) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(labels)-1)
	for i := 1; i < len(labels); i++ {
		out = append(out, strings.Join(labels[i:], ".")+".")
	}
	return out, nil
}

// nextCloserName returns the next closer name: the closest encloser with
// one additional label taken from the QNAME. For example with QNAME
// a.b.example and closest encloser example, it returns b.example.
func nextCloserName(qname, closestEncloser string) (string, error) {
	q, err := nameLabels(qname)
	if err != nil {
		return "", err
	}
	ce, err := nameLabels(closestEncloser)
	if err != nil {
		return "", err
	}
	if len(ce) >= len(q) {
		return "", ErrInvalidName
	}
	idx := len(q) - len(ce) - 1
	return strings.Join(q[idx:], ".") + ".", nil
}

// wildcardName returns "*.<closest encloser>" in canonical form.
func wildcardName(closestEncloser string) (string, error) {
	ce, err := nameLabels(closestEncloser)
	if err != nil {
		return "", err
	}
	if len(ce) == 0 {
		return "*.", nil
	}
	return "*." + strings.Join(ce, ".") + ".", nil
}
