// Package auditlog implements hash-chained audit logs and a conservative
// merge algorithm for replicas that may be incomplete, duplicated, or
// tampered with.
package auditlog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// HashSize is the byte length of a chain hash (SHA-256).
const HashSize = sha256.Size

// Hash is a fixed-size chain hash.
type Hash [HashSize]byte

// ZeroHash is the PrevHash of the genesis entry.
var ZeroHash Hash

// Entry is a single audit record. Hash commits to Seq, Payload and PrevHash,
// so any modification is detectable and each entry is bound to its
// predecessor.
type Entry struct {
	Seq      uint64 `json:"seq"`
	Payload  string `json:"payload"`
	PrevHash Hash   `json:"prev_hash"`
	Hash     Hash   `json:"hash"`
}

// ComputeHash derives the chain hash for the given fields.
func ComputeHash(seq uint64, payload string, prevHash Hash) Hash {
	h := sha256.New()
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], seq)
	h.Write(seqBuf[:])
	h.Write([]byte(payload))
	h.Write(prevHash[:])
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// VerifySelf reports whether e.Hash matches the recomputed hash of its
// fields. A mismatch means the entry was tampered with or corrupted.
func (e Entry) VerifySelf() bool {
	return e.Hash == ComputeHash(e.Seq, e.Payload, e.PrevHash)
}

// Append returns the next entry extending chain with payload. Calling it on
// an empty slice produces the genesis entry (Seq 1, PrevHash ZeroHash).
func Append(chain []Entry, payload string) Entry {
	e := Entry{Seq: 1, Payload: payload, PrevHash: ZeroHash}
	if n := len(chain); n > 0 {
		last := chain[n-1]
		e.Seq = last.Seq + 1
		e.PrevHash = last.Hash
	}
	e.Hash = ComputeHash(e.Seq, e.Payload, e.PrevHash)
	return e
}

// MakeChain builds a valid chain of n entries with payloads
// "payloadPrefix-1" .. "payloadPrefix-n".
func MakeChain(n int, payloadPrefix string) []Entry {
	var chain []Entry
	for i := 1; i <= n; i++ {
		chain = append(chain, Append(chain, fmt.Sprintf("%s-%d", payloadPrefix, i)))
	}
	return chain
}

// MarshalJSON encodes hashes as hex for readability.
func (h Hash) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(h[:]))
}

// UnmarshalJSON decodes a hex hash.
func (h *Hash) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("auditlog: bad hash hex: %v", err)
	}
	if len(raw) != HashSize {
		return fmt.Errorf("auditlog: hash must be %d bytes, got %d", HashSize, len(raw))
	}
	copy(h[:], raw)
	return nil
}

// Hex renders the hash as a hex string.
func (h Hash) Hex() string { return hex.EncodeToString(h[:]) }
