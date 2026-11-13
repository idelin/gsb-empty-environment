package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// WireBucket is the server-visible representation of one bucket.  Node,
// Generation, and Txn are cleartext but authenticated as AEAD additional
// data.
type WireBucket struct {
	Node       int
	Generation uint64
	Txn        uint64
	Nonce      []byte
	Ciphertext []byte
}

func (b WireBucket) clone() WireBucket {
	out := b
	out.Nonce = append([]byte(nil), b.Nonce...)
	out.Ciphertext = append([]byte(nil), b.Ciphertext...)
	return out
}

func (b WireBucket) equal(other WireBucket) bool {
	return b.Node == other.Node &&
		b.Generation == other.Generation &&
		b.Txn == other.Txn &&
		bytes.Equal(b.Nonce, other.Nonce) &&
		bytes.Equal(b.Ciphertext, other.Ciphertext)
}

// RootReceipt is the server's signature over the current tree root and
// monotonic server transaction number.
type RootReceipt struct {
	TreeID      string
	Transaction uint64
	Root        [32]byte
	Signature   []byte
}

func (r RootReceipt) clone() RootReceipt {
	r.Signature = append([]byte(nil), r.Signature...)
	return r
}

// BucketAdditionalData returns the canonical AEAD associated data for one
// bucket.  It binds the tree identity, node index, bucket generation, and
// the client transaction that created the bucket ciphertext.
func BucketAdditionalData(treeID string, node int, generation, txn uint64) []byte {
	var buf bytes.Buffer
	buf.WriteString("pathoram-bucket-ad-v1")
	writeStringField(&buf, treeID)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(node))
	buf.Write(n[:])
	binary.BigEndian.PutUint64(n[:], generation)
	buf.Write(n[:])
	binary.BigEndian.PutUint64(n[:], txn)
	buf.Write(n[:])
	return buf.Bytes()
}

// BucketDigest is the per-bucket digest XORed into the tree root.  The root
// accumulator is order-independent, which lets a client update it from one
// path without downloading the whole tree.
func BucketDigest(treeID string, bucket WireBucket) [32]byte {
	h := sha256.New()
	h.Write([]byte("pathoram-bucket-digest-v1"))
	writeStringField(h, treeID)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(bucket.Node))
	h.Write(n[:])
	binary.BigEndian.PutUint64(n[:], bucket.Generation)
	h.Write(n[:])
	binary.BigEndian.PutUint64(n[:], bucket.Txn)
	h.Write(n[:])
	binary.BigEndian.PutUint64(n[:], uint64(len(bucket.Nonce)))
	h.Write(n[:])
	h.Write(bucket.Nonce)
	binary.BigEndian.PutUint64(n[:], uint64(len(bucket.Ciphertext)))
	h.Write(n[:])
	h.Write(bucket.Ciphertext)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func xorRoot(dst *[32]byte, digest [32]byte) {
	for i := range dst {
		dst[i] ^= digest[i]
	}
}

func encodeBucketPlain(cfg Config, entries []Block) ([]byte, error) {
	if len(entries) > cfg.BucketCapacity {
		return nil, fmt.Errorf("%w: bucket has %d entries, capacity %d", ErrIntegrity, len(entries), cfg.BucketCapacity)
	}
	sorted := append([]Block(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].ID == sorted[i].ID {
			return nil, fmt.Errorf("%w: duplicate block %d in bucket", ErrIntegrity, sorted[i].ID)
		}
	}

	size := 4 + 2 + len(sorted)*(8+8+cfg.BlockDataSize+32)
	out := make([]byte, 0, size)
	out = append(out, 'P', 'O', 'B', '1')
	var n [8]byte
	binary.BigEndian.PutUint16(n[:2], uint16(len(sorted)))
	out = append(out, n[:2]...)
	for _, b := range sorted {
		if len(b.Data) != cfg.BlockDataSize {
			return nil, fmt.Errorf("%w: block %d has data length %d, want %d", ErrIntegrity, b.ID, len(b.Data), cfg.BlockDataSize)
		}
		binary.BigEndian.PutUint64(n[:], uint64(b.ID))
		out = append(out, n[:]...)
		binary.BigEndian.PutUint64(n[:], b.Version)
		out = append(out, n[:]...)
		out = append(out, b.Data...)
		out = append(out, b.MAC[:]...)
	}
	return out, nil
}

func decodeBucketPlain(cfg Config, plain []byte) ([]Block, error) {
	if len(plain) < 6 || !bytes.Equal(plain[:4], []byte("POB1")) {
		return nil, fmt.Errorf("%w: bad bucket header", ErrIntegrity)
	}
	count := int(binary.BigEndian.Uint16(plain[4:6]))
	if count > cfg.BucketCapacity {
		return nil, fmt.Errorf("%w: bucket count %d exceeds capacity", ErrIntegrity, count)
	}
	entrySize := 8 + 8 + cfg.BlockDataSize + 32
	if len(plain) != 6+count*entrySize {
		return nil, fmt.Errorf("%w: bucket length %d inconsistent with count %d", ErrIntegrity, len(plain), count)
	}
	out := make([]Block, 0, count)
	offset := 6
	for i := 0; i < count; i++ {
		id := BlockID(binary.BigEndian.Uint64(plain[offset : offset+8]))
		version := binary.BigEndian.Uint64(plain[offset+8 : offset+16])
		dataStart := offset + 16
		dataEnd := dataStart + cfg.BlockDataSize
		data := append([]byte(nil), plain[dataStart:dataEnd]...)
		var mac [32]byte
		copy(mac[:], plain[dataEnd:dataEnd+32])
		block := Block{ID: id, Version: version, Data: data, MAC: mac}
		if !block.validMAC(cfg.MACKey, cfg.TreeID) {
			return nil, fmt.Errorf("%w: bad block MAC for block %d", ErrIntegrity, id)
		}
		if len(out) > 0 && out[len(out)-1].ID >= id {
			return nil, fmt.Errorf("%w: bucket block IDs not strictly increasing", ErrIntegrity)
		}
		out = append(out, block)
		offset += entrySize
	}
	return out, nil
}

func deriveBucketNonce(aead BucketAEAD, ad, plain []byte) []byte {
	h := sha256.New()
	h.Write([]byte("pathoram-bucket-nonce-v1"))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(ad)))
	h.Write(n[:])
	h.Write(ad)
	binary.BigEndian.PutUint64(n[:], uint64(len(plain)))
	h.Write(n[:])
	h.Write(plain)
	sum := h.Sum(nil)
	return append([]byte(nil), sum[:aead.NonceSize()]...)
}

func sealBucket(cfg Config, node int, generation, txn uint64, entries []Block) (WireBucket, error) {
	plain, err := encodeBucketPlain(cfg, entries)
	if err != nil {
		return WireBucket{}, err
	}
	ad := BucketAdditionalData(cfg.TreeID, node, generation, txn)
	nonce := deriveBucketNonce(cfg.AEAD, ad, plain)
	ciphertext := cfg.AEAD.Seal(nil, nonce, plain, ad)
	return WireBucket{
		Node:       node,
		Generation: generation,
		Txn:        txn,
		Nonce:      nonce,
		Ciphertext: append([]byte(nil), ciphertext...),
	}, nil
}

func openBucket(cfg Config, bucket WireBucket) ([]Block, error) {
	if len(bucket.Nonce) != cfg.AEAD.NonceSize() {
		return nil, fmt.Errorf("%w: bad bucket nonce length", ErrIntegrity)
	}
	ad := BucketAdditionalData(cfg.TreeID, bucket.Node, bucket.Generation, bucket.Txn)
	plain, err := cfg.AEAD.Open(nil, bucket.Nonce, bucket.Ciphertext, ad)
	if err != nil {
		return nil, fmt.Errorf("%w: bucket %d AEAD: %v", ErrIntegrity, bucket.Node, err)
	}
	return decodeBucketPlain(cfg, plain)
}

func receiptMessage(receipt RootReceipt) []byte {
	h := sha256.New()
	h.Write([]byte("pathoram-root-receipt-v1"))
	writeStringField(h, receipt.TreeID)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], receipt.Transaction)
	h.Write(n[:])
	h.Write(receipt.Root[:])
	return h.Sum(nil)
}

// SignRootReceipt signs a root receipt with an ed25519 key.  It is primarily
// used by hosts implementing this package's storage contract.
func SignRootReceipt(privateKey ed25519.PrivateKey, receipt RootReceipt) (RootReceipt, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return RootReceipt{}, fmt.Errorf("%w: bad receipt signing key", ErrInvalidConfig)
	}
	receipt.Signature = ed25519.Sign(privateKey, receiptMessage(receipt))
	return receipt, nil
}

// VerifyRootReceipt verifies a server's signature over a root receipt.
func VerifyRootReceipt(publicKey ed25519.PublicKey, receipt RootReceipt) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: bad receipt public key", ErrInvalidConfig)
	}
	if len(receipt.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(publicKey, receiptMessage(receipt), receipt.Signature) {
		return ErrReceipt
	}
	return nil
}
