package netsec

import (
	"errors"
	"fmt"
)

// Domain describes the ordered finite domain of the sensitive attribute.
// Domain positions are 0..Size()-1 in the declared order; the ground distance
// between adjacent positions is one.
type Domain struct {
	labels []string
	index  map[string]int
}

// NewDomain builds a domain from labels given in ascending sensitive order.
// Labels must be non-empty and unique.
func NewDomain(labels []string) (*Domain, error) {
	if len(labels) == 0 {
		return nil, errors.New("netsec: domain must contain at least one value")
	}
	index := make(map[string]int, len(labels))
	ordered := make([]string, len(labels))
	for i, label := range labels {
		if _, exists := index[label]; exists {
			return nil, fmt.Errorf("netsec: duplicate domain value %q", label)
		}
		index[label] = i
		ordered[i] = label
	}
	return &Domain{labels: ordered, index: index}, nil
}

// Size returns the number of values in the domain.
func (d *Domain) Size() int {
	return len(d.labels)
}

// Index resolves a label to its ordered position.
func (d *Domain) Index(label string) (int, bool) {
	pos, ok := d.index[label]
	return pos, ok
}

// Label resolves an ordered position back to its label.
func (d *Domain) Label(pos int) (string, bool) {
	if pos < 0 || pos >= len(d.labels) {
		return "", false
	}
	return d.labels[pos], true
}
