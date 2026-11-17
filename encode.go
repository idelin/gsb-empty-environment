package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
)

type encoder struct {
	h hash.Hash
}

func newEncoder(tag string) *encoder {
	e := &encoder{h: sha256.New()}
	e.bytes([]byte(tag))
	return e
}

func (e *encoder) bytes(p []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(p)))
	_, _ = e.h.Write(size[:])
	_, _ = e.h.Write(p)
}

func (e *encoder) string(s string) {
	e.bytes([]byte(s))
}

func (e *encoder) uint64(v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = e.h.Write(buf[:])
}

func (e *encoder) byte(v byte) {
	_, _ = e.h.Write([]byte{v})
}

func (e *encoder) digest(d Digest) {
	_, _ = e.h.Write(d[:])
}

func (e *encoder) sum() Digest {
	var d Digest
	copy(d[:], e.h.Sum(nil))
	return d
}

func computeProgramDigest(version string, root *Expr, rules map[string]Rule) Digest {
	e := newEncoder("netsec.program.v1")
	e.string(version)

	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	e.uint64(uint64(len(ids)))
	for _, id := range ids {
		rule := rules[id]
		e.string(rule.ID)
		e.string(rule.Version)
	}

	writeExpr(e, root, rules)
	return e.sum()
}

func writeExpr(e *encoder, expr *Expr, rules map[string]Rule) {
	if expr == nil {
		e.byte(0)
		return
	}
	switch expr.kind {
	case KindRule:
		e.byte(1)
		e.string(expr.ruleID)
		e.string(rules[expr.ruleID].Version)
	case KindAnd:
		e.byte(2)
		e.uint64(uint64(len(expr.children)))
		for _, child := range expr.children {
			writeExpr(e, child, rules)
		}
	case KindOr:
		e.byte(3)
		e.uint64(uint64(len(expr.children)))
		for _, child := range expr.children {
			writeExpr(e, child, rules)
		}
	case KindNot:
		e.byte(4)
		if len(expr.children) == 1 {
			writeExpr(e, expr.children[0], rules)
		} else {
			e.byte(0)
		}
	default:
		e.byte(255)
	}
}
