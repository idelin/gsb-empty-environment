package triestore

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
)

// On-disk node layout (all integers little-endian):
//
//	magic      4 bytes  "TRN1"
//	flags      1 byte   bit0: hasValue
//	valueLen   uint32
//	value      valueLen bytes
//	childCount uint16
//	children   childCount * (1 byte keyByte + 32 bytes childID), sorted by keyByte
//	crc32      4 bytes  IEEE checksum of everything above
//
// A node's ID is the hex SHA-256 of its payload (everything before the CRC),
// so a reference is self-verifying: recompute the hash and compare with the ID.
const nodeMagic = "TRN1"

const hashLen = sha256.Size

// emptyID is the reference used for "no node" (the empty tree).
const emptyID = ""

type node struct {
	hasValue bool
	value    []byte
	children map[byte]string // keyByte -> child node ID (hex)
}

func newNode() *node {
	return &node{children: make(map[byte]string)}
}

func (n *node) clone() *node {
	c := &node{hasValue: n.hasValue, value: n.value, children: make(map[byte]string, len(n.children))}
	for b, id := range n.children {
		c.children[b] = id
	}
	return c
}

// encode returns payload || crc32(payload).
func (n *node) encode() []byte {
	var buf bytes.Buffer
	buf.WriteString(nodeMagic)
	var flags byte
	if n.hasValue {
		flags |= 1
	}
	buf.WriteByte(flags)
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(n.value)))
	buf.Write(tmp[:])
	buf.Write(n.value)

	keys := make([]int, 0, len(n.children))
	for b := range n.children {
		keys = append(keys, int(b))
	}
	sort.Ints(keys)
	binary.LittleEndian.PutUint16(tmp[:2], uint16(len(keys)))
	buf.Write(tmp[:2])
	for _, k := range keys {
		buf.WriteByte(byte(k))
		raw, err := hex.DecodeString(n.children[byte(k)])
		if err != nil || len(raw) != hashLen {
			panic(fmt.Sprintf("triestore: invalid child id %q", n.children[byte(k)]))
		}
		buf.Write(raw)
	}

	payload := buf.Bytes()
	crc := crc32.ChecksumIEEE(payload)
	binary.LittleEndian.PutUint32(tmp[:], crc)
	return append(payload, tmp[:]...)
}

// decodeNode verifies the CRC and parses a node from data (payload || crc32).
func decodeNode(data []byte) (*node, error) {
	if len(data) < 4+1+4+2+4 {
		return nil, errors.New("triestore: node too short")
	}
	payload, crcBytes := data[:len(data)-4], data[len(data)-4:]
	want := binary.LittleEndian.Uint32(crcBytes)
	if got := crc32.ChecksumIEEE(payload); got != want {
		return nil, fmt.Errorf("triestore: crc mismatch (stored %08x, computed %08x)", want, got)
	}
	if string(payload[:4]) != nodeMagic {
		return nil, fmt.Errorf("triestore: bad magic %q", payload[:4])
	}
	r := payload[4:]
	n := newNode()
	n.hasValue = r[0]&1 != 0
	r = r[1:]
	vlen := binary.LittleEndian.Uint32(r[:4])
	r = r[4:]
	if uint32(len(r)) < vlen+2 {
		return nil, errors.New("triestore: truncated value")
	}
	n.value = append([]byte(nil), r[:vlen]...)
	r = r[vlen:]
	count := binary.LittleEndian.Uint16(r[:2])
	r = r[2:]
	if len(r) != int(count)*(1+hashLen) {
		return nil, fmt.Errorf("triestore: child table size mismatch (want %d, got %d)", int(count)*(1+hashLen), len(r))
	}
	for i := 0; i < int(count); i++ {
		b := r[0]
		n.children[b] = hex.EncodeToString(r[1 : 1+hashLen])
		r = r[1+hashLen:]
	}
	return n, nil
}

// payloadID returns the node ID (hex SHA-256) of an encoded payload.
func payloadID(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
