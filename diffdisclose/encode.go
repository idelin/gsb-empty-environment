package diffdisclose

import (
	"bytes"
	"encoding/binary"
)

var magic = []byte("DDDPROOF")

// Encode 返回证明的确定性字节编码。相同逻辑输入恒得相同字节序列。
func (p *Proof) Encode() []byte {
	buf := bytes.NewBuffer(nil)
	buf.Write(magic)
	encodeManifest(buf, p.OldManifest)
	encodeManifest(buf, p.NewManifest)
	writeUvarint(buf, uint64(p.TreeHeight))
	encodeIntervals(buf, p.Authorized)
	encodeIntervals(buf, p.Revoked)
	writeUvarint(buf, uint64(len(p.Cache)))
	for i := range p.Cache {
		encodeCacheEntry(buf, p.Cache[i])
	}
	writeUvarint(buf, uint64(len(p.Records)))
	for i := range p.Records {
		encodeRecord(buf, p.Records[i])
	}
	return buf.Bytes()
}

func writeUvarint(buf *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	buf.Write(tmp[:n])
}

func writeBytesField(buf *bytes.Buffer, data []byte) {
	writeUvarint(buf, uint64(len(data)))
	buf.Write(data)
}

func writeDigest(buf *bytes.Buffer, d Digest) { buf.Write(d[:]) }

func encodeManifest(buf *bytes.Buffer, m Manifest) {
	writeBytesField(buf, []byte(m.Tenant))
	writeUvarint(buf, m.Version)
	writeDigest(buf, m.Parent)
	writeDigest(buf, m.Root)
	writeBytesField(buf, m.PublicKey)
	writeBytesField(buf, m.Signature)
}

func encodeIntervals(buf *bytes.Buffer, set IntervalSet) {
	writeUvarint(buf, uint64(len(set.items)))
	for _, iv := range set.items {
		writeUvarint(buf, iv.Lo)
		writeUvarint(buf, iv.Hi)
	}
}

func encodeCacheEntry(buf *bytes.Buffer, e CacheEntry) {
	writeDigest(buf, e.Manifest)
	buf.WriteByte(byte(e.Side))
	writeUvarint(buf, uint64(e.Height))
	packed, bitLen := e.Address.bytes()
	writeUvarint(buf, uint64(bitLen))
	writeBytesField(buf, packed)
	writeDigest(buf, e.Digest)
}

func encodeLeafPayload(buf *bytes.Buffer, p *leafPayload) {
	if p == nil {
		buf.WriteByte(sideNeither)
		return
	}
	buf.WriteByte(sideBoth)
	writeUvarint(buf, p.Key)
	writeBytesField(buf, p.Value)
	writeBytesField(buf, p.Salt)
}

func encodeRecord(buf *bytes.Buffer, r record) {
	buf.WriteByte(r.kind)
	switch r.kind {
	case recSplit, recEmpty, recEmptySubtree:
		// 无负载。
	case recSharedInternal, recBlindRevoked, recBlindOutside:
		writeDigest(buf, r.blindOld)
		writeDigest(buf, r.blindNew)
	case recLeafShared:
		writeUvarint(buf, r.key)
		writeDigest(buf, r.oldDigest)
		writeDigest(buf, r.newDigest)
	case recLeafUpdate:
		writeUvarint(buf, r.key)
		buf.WriteByte(r.present)
		writeDigest(buf, r.oldDigest)
		writeDigest(buf, r.newDigest)
		encodeLeafPayload(buf, r.oldLeaf)
		encodeLeafPayload(buf, r.newLeaf)
	case recCachedInternal:
		writeUvarint(buf, uint64(r.cacheIndex))
	case recCachedLeaf:
		writeUvarint(buf, uint64(r.cacheIndex))
		writeUvarint(buf, r.key)
		buf.WriteByte(r.present)
		writeDigest(buf, r.oldDigest)
		writeDigest(buf, r.newDigest)
	default:
		panic("diffdisclose: unknown record kind")
	}
}
