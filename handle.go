package netsec

import (
	"crypto/rand"
	"encoding/binary"
)

// ResourceType identifies the kind of host resource a handle refers to.
type ResourceType uint8

const (
	ResourceFile ResourceType = iota + 1
	ResourceNetwork
)

func (t ResourceType) valid() bool {
	return t == ResourceFile || t == ResourceNetwork
}

// Op is a bitmask of operations a capability may allow.
type Op uint32

const (
	OpRead Op = 1 << iota
	OpWrite
	OpConnect
	OpListen
)

const knownOps = OpRead | OpWrite | OpConnect | OpListen

// InstanceID identifies a plugin instance registered with the host.
type InstanceID [16]byte

// HandleLen is the exact byte length of an encoded Handle.
const HandleLen = 1 + 16 + 8 + 1 + 4 + 16

// Handle is an opaque capability token. Plugins may store, copy and
// transmit it as arbitrary bytes, but only the host can mint a valid one:
// every byte pattern is validated against the host registry on use.
type Handle [HandleLen]byte

const handleVersion byte = 1

type handleID [16]byte
type resourceID [16]byte

func randomID() [16]byte {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return id
}

func encodeHandle(id handleID, generation uint64, rt ResourceType, ops Op, inst InstanceID) Handle {
	var h Handle
	h[0] = handleVersion
	copy(h[1:17], id[:])
	binary.BigEndian.PutUint64(h[17:25], generation)
	h[25] = byte(rt)
	binary.BigEndian.PutUint32(h[26:30], uint32(ops))
	copy(h[30:46], inst[:])
	return h
}

type parsedHandle struct {
	id         handleID
	generation uint64
	rtype      ResourceType
	ops        Op
	instance   InstanceID
}

func parseHandle(h Handle) (parsedHandle, bool) {
	if h[0] != handleVersion {
		return parsedHandle{}, false
	}
	var p parsedHandle
	copy(p.id[:], h[1:17])
	p.generation = binary.BigEndian.Uint64(h[17:25])
	p.rtype = ResourceType(h[25])
	p.ops = Op(binary.BigEndian.Uint32(h[26:30]))
	copy(p.instance[:], h[30:46])
	return p, true
}
