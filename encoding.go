package replayhandoff

import (
	"encoding/binary"
)

const wireVersion uint64 = 1

func appendLP(dst []byte, data []byte) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(data)))
	dst = append(dst, n[:]...)
	dst = append(dst, data...)
	return dst
}

func appendU64(dst []byte, v uint64) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], v)
	return append(dst, n[:]...)
}

func appendPartition(dst []byte, p Partition) []byte {
	dst = appendLP(dst, []byte(p.Context))
	dst = appendLP(dst, []byte(p.Sender))
	return dst
}

func appendInterval(dst []byte, iv Interval) []byte {
	dst = appendU64(dst, uint64(iv.Lo))
	return appendU64(dst, uint64(iv.Hi))
}

func appendIntervalSet(dst []byte, set IntervalSet) []byte {
	dst = appendU64(dst, uint64(len(set.spans)))
	for _, iv := range set.spans {
		dst = appendInterval(dst, iv)
	}
	return dst
}

func appendRoot(dst []byte, root Root) []byte {
	return append(dst, root[:]...)
}

func appendDigest(dst []byte, digest [32]byte) []byte {
	return append(dst, digest[:]...)
}
