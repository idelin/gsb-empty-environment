package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Common DNS RR type codes used by the cache's NODATA checks.
const (
	TypeA      uint16 = 1
	TypeNS     uint16 = 2
	TypeCNAME  uint16 = 5
	TypeSOA    uint16 = 6
	TypeMX     uint16 = 15
	TypeTXT    uint16 = 16
	TypeAAAA   uint16 = 28
	TypeDS     uint16 = 43
	TypeRRSIG  uint16 = 46
	TypeNSEC   uint16 = 47
	TypeDNSKEY uint16 = 48
	TypeANY    uint16 = 255
)

// TypeBitmap is the set of RR types present at an NSEC owner.  It is stored in
// numeric order with duplicates removed.
type TypeBitmap []uint16

// Normalize returns a sorted, duplicate-free copy.  Type zero is not valid in
// an NSEC type bitmap and produces an error.
func (b TypeBitmap) Normalize() (TypeBitmap, error) {
	out := append(TypeBitmap(nil), b...)
	for _, t := range out {
		if t == 0 {
			return nil, fmt.Errorf("netsec: type 0 is not valid in an NSEC bitmap")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) < 2 {
		return out, nil
	}
	dst := 1
	for src := 1; src < len(out); src++ {
		if out[src] != out[dst-1] {
			out[dst] = out[src]
			dst++
		}
	}
	return out[:dst], nil
}

// Has reports whether t is present.
func (b TypeBitmap) Has(t uint16) bool {
	for _, v := range b {
		if v == t {
			return true
		}
	}
	return false
}

// Equal reports set equality, independently of ordering and duplicates.
func (b TypeBitmap) Equal(other TypeBitmap) bool {
	x, err := b.Normalize()
	if err != nil {
		return false
	}
	y, err := other.Normalize()
	if err != nil {
		return false
	}
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// Clone returns an independent copy.
func (b TypeBitmap) Clone() TypeBitmap {
	if b == nil {
		return nil
	}
	return append(TypeBitmap(nil), b...)
}

func (b TypeBitmap) String() string {
	parts := make([]string, len(b))
	for i, t := range b {
		parts[i] = fmt.Sprintf("%d", t)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
