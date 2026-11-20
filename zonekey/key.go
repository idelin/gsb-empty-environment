// Package zonekey preserves the on-disk v1 index key format.
package zonekey

import (
	"errors"
	"strings"
)

var ErrName = errors.New("invalid v1 zone name")

// EncodeV1 stores reversed ASCII labels, terminated by 00, then FF.
func EncodeV1(name string) ([]byte, error) {
	if name == "." {
		return []byte{0xff}, nil
	}
	name = strings.TrimSuffix(name, ".")
	labels := strings.Split(name, ".")
	out := make([]byte, 0, len(name)+2)
	for i := len(labels) - 1; i >= 0; i-- {
		label := labels[i]
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return nil, ErrName
		}
		for k := 0; k < len(label); k++ {
			c := label[k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, ErrName
			}
			out = append(out, c)
		}
		out = append(out, 0)
	}
	if len(out)+1 > 255 {
		return nil, ErrName
	}
	return append(out, 0xff), nil
}
