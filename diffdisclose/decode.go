package diffdisclose

import (
	"bytes"
	"encoding/binary"
)

// DecodeProof 解析规范编码，任何长度、标签或冗余不一致都直接拒绝。
func DecodeProof(data []byte) (*Proof, error) {
	r := &reader{buf: data}
	if !r.take(len(magic)) || !bytes.Equal(r.last, magic) {
		return nil, errMalformedProof
	}
	p := &Proof{}
	var err error
	if p.OldManifest, err = decodeManifest(r); err != nil {
		return nil, err
	}
	if p.NewManifest, err = decodeManifest(r); err != nil {
		return nil, err
	}
	height64, ok := r.uvarint()
	if !ok || height64 == 0 || height64 > MaxHeight {
		return nil, errMalformedProof
	}
	p.TreeHeight = int(height64)
	if p.Authorized, err = decodeIntervals(r, true); err != nil {
		return nil, err
	}
	if p.Revoked, err = decodeIntervals(r, false); err != nil {
		return nil, err
	}
	cacheCount, ok := r.uvarint()
	if !ok || cacheCount > 1<<20 {
		return nil, errMalformedProof
	}
	p.Cache = make([]CacheEntry, cacheCount)
	for i := range p.Cache {
		if p.Cache[i], err = decodeCacheEntry(r); err != nil {
			return nil, err
		}
	}
	recordCount, ok := r.uvarint()
	if !ok || recordCount > 1<<28 {
		return nil, errMalformedProof
	}
	p.Records = make([]record, recordCount)
	for i := range p.Records {
		if p.Records[i], err = decodeRecord(r); err != nil {
			return nil, err
		}
	}
	if len(r.buf) != 0 {
		return nil, errMalformedProof
	}
	return p, nil
}

type reader struct {
	buf  []byte
	last []byte
}

func (r *reader) take(n int) bool {
	if n < 0 || len(r.buf) < n {
		return false
	}
	r.last = r.buf[:n]
	r.buf = r.buf[n:]
	return true
}

func (r *reader) uvarint() (uint64, bool) {
	v, n := binary.Uvarint(r.buf)
	if n <= 0 {
		return 0, false
	}
	r.buf = r.buf[n:]
	return v, true
}

func (r *reader) byteVal() (byte, bool) {
	if !r.take(1) {
		return 0, false
	}
	return r.last[0], true
}

func (r *reader) digestVal() (Digest, bool) {
	if !r.take(DigestSize) {
		return Digest{}, false
	}
	var d Digest
	copy(d[:], r.last)
	return d, true
}

func (r *reader) bytesField() ([]byte, bool) {
	n, ok := r.uvarint()
	if !ok || n > 1<<28 || !r.take(int(n)) {
		return nil, false
	}
	out := make([]byte, n)
	copy(out, r.last)
	return out, true
}

func decodeManifest(r *reader) (Manifest, error) {
	tenant, ok := r.bytesField()
	if !ok {
		return Manifest{}, errMalformedProof
	}
	version, ok := r.uvarint()
	if !ok {
		return Manifest{}, errMalformedProof
	}
	parent, ok := r.digestVal()
	if !ok {
		return Manifest{}, errMalformedProof
	}
	root, ok := r.digestVal()
	if !ok {
		return Manifest{}, errMalformedProof
	}
	pub, ok := r.bytesField()
	if !ok {
		return Manifest{}, errMalformedProof
	}
	sig, ok := r.bytesField()
	if !ok {
		return Manifest{}, errMalformedProof
	}
	return Manifest{
		Tenant:    string(tenant),
		Version:   version,
		Parent:    parent,
		Root:      root,
		PublicKey: pub,
		Signature: sig,
	}, nil
}

func decodeIntervals(r *reader, merge bool) (IntervalSet, error) {
	n, ok := r.uvarint()
	if !ok || n > 1<<20 {
		return IntervalSet{}, errMalformedProof
	}
	ivs := make([]Interval, n)
	for i := range ivs {
		lo, ok := r.uvarint()
		if !ok {
			return IntervalSet{}, errMalformedProof
		}
		hi, ok := r.uvarint()
		if !ok {
			return IntervalSet{}, errMalformedProof
		}
		ivs[i] = Interval{Lo: lo, Hi: hi}
	}
	var set IntervalSet
	var err error
	if merge {
		set, err = NewIntervalSet(ivs)
	} else {
		set, err = newRevocationSet(ivs)
	}
	if err != nil {
		return IntervalSet{}, errMalformedProof
	}
	return set, nil
}

func decodeCacheEntry(r *reader) (CacheEntry, error) {
	manifest, ok := r.digestVal()
	if !ok {
		return CacheEntry{}, errMalformedProof
	}
	side, ok := r.byteVal()
	if !ok {
		return CacheEntry{}, errMalformedProof
	}
	height64, ok := r.uvarint()
	if !ok || height64 > MaxHeight {
		return CacheEntry{}, errMalformedProof
	}
	bitLen64, ok := r.uvarint()
	if !ok || bitLen64 > MaxHeight {
		return CacheEntry{}, errMalformedProof
	}
	packed, ok := r.bytesField()
	if !ok {
		return CacheEntry{}, errMalformedProof
	}
	address, err := parseAddr(packed, int(bitLen64))
	if err != nil {
		return CacheEntry{}, errMalformedProof
	}
	digest, ok := r.digestVal()
	if !ok {
		return CacheEntry{}, errMalformedProof
	}
	return CacheEntry{
		Manifest: manifest,
		Side:     CacheSide(side),
		Address:  address,
		Height:   int(height64),
		Digest:   digest,
	}, nil
}

func decodeLeafPayload(r *reader) (*leafPayload, bool) {
	tag, ok := r.byteVal()
	if !ok {
		return nil, false
	}
	if tag == sideNeither {
		return nil, true
	}
	if tag != sideBoth {
		return nil, false
	}
	key, ok := r.uvarint()
	if !ok {
		return nil, false
	}
	value, ok := r.bytesField()
	if !ok {
		return nil, false
	}
	salt, ok := r.bytesField()
	if !ok || len(salt) != SaltSize {
		return nil, false
	}
	return &leafPayload{Key: key, Value: value, Salt: salt}, true
}

func decodeRecord(r *reader) (record, error) {
	kind, ok := r.byteVal()
	if !ok {
		return record{}, errMalformedProof
	}
	rec := record{kind: kind, cacheIndex: -1}
	switch kind {
	case recSplit, recEmpty, recEmptySubtree:
		return rec, nil
	case recSharedInternal, recBlindRevoked, recBlindOutside:
		if rec.blindOld, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.blindNew, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		return rec, nil
	case recLeafShared:
		if rec.key, ok = r.uvarint(); !ok {
			return record{}, errMalformedProof
		}
		if rec.oldDigest, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.newDigest, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		rec.present = sideBoth
		return rec, nil
	case recLeafUpdate:
		if rec.key, ok = r.uvarint(); !ok {
			return record{}, errMalformedProof
		}
		if rec.present, ok = r.byteVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.oldDigest, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.newDigest, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.oldLeaf, ok = decodeLeafPayload(r); !ok {
			return record{}, errMalformedProof
		}
		if rec.newLeaf, ok = decodeLeafPayload(r); !ok {
			return record{}, errMalformedProof
		}
		return rec, nil
	case recCachedInternal:
		idx, ok := r.uvarint()
		if !ok || idx > 1<<20 {
			return record{}, errMalformedProof
		}
		rec.cacheIndex = int(idx)
		return rec, nil
	case recCachedLeaf:
		idx, ok := r.uvarint()
		if !ok || idx > 1<<20 {
			return record{}, errMalformedProof
		}
		rec.cacheIndex = int(idx)
		if rec.key, ok = r.uvarint(); !ok {
			return record{}, errMalformedProof
		}
		if rec.present, ok = r.byteVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.oldDigest, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		if rec.newDigest, ok = r.digestVal(); !ok {
			return record{}, errMalformedProof
		}
		return rec, nil
	default:
		return record{}, errMalformedProof
	}
}
