package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

const (
	// RecordSize is the fixed plaintext record length in bytes.
	RecordSize = 32
	// TagSize is the length of an HMAC-SHA256 row tag.
	TagSize = 32
	// RowSize is the length of one database row: record || tag.
	RowSize = RecordSize + TagSize
	// MaxRecords is the largest supported database.
	MaxRecords = 4096
)

var (
	// ErrVerification is the only error returned by Client.Query. Every
	// malformed query, transport failure, signature failure, identity
	// mismatch, snapshot mismatch, or row-tag failure collapses to this value
	// so the client does not expose which check failed.
	ErrVerification = errors.New("pir: request failed")

	// ErrMalformedQuery is returned by servers for invalid query vectors.
	ErrMalformedQuery = errors.New("pir: malformed query vector")
)

const (
	queryDigestDomain     = "example.org/netsec/q0105/query/v1\x00"
	responseSigningDomain = "example.org/netsec/q0105/response/v1\x00"
)

// Registry is the public database registration pinned by the client. The
// snapshot ID identifies the database version and the Merkle root binds all
// rows in that version. The root is not a substitute for per-row tags.
type Registry struct {
	SnapshotID string
	MerkleRoot [32]byte
	N          int
}

// Response is one server's reply. Payload is the bytewise XOR of every row
// selected by the query vector. Signature is an Ed25519 signature over the
// query digest, Payload, and SnapshotID.
type Response struct {
	Payload    []byte
	SnapshotID string
	MerkleRoot [32]byte
	ServerID   []byte
	Signature  []byte
}

// Clone returns a deep copy of r.
func (r Response) Clone() Response {
	out := r
	out.Payload = append([]byte(nil), r.Payload...)
	out.ServerID = append([]byte(nil), r.ServerID...)
	out.Signature = append([]byte(nil), r.Signature...)
	return out
}

// QueryLength returns the fixed query-vector length ceil(n/8).
func QueryLength(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 7) / 8
}

// RecordTag computes tag_i = HMAC-SHA256(key, big-endian index || record).
// The returned slice is a fresh copy.
func RecordTag(key []byte, index int, record []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("pir: empty HMAC key")
	}
	if index < 0 || index >= MaxRecords {
		return nil, errors.New("pir: index out of range")
	}
	if len(record) != RecordSize {
		return nil, errors.New("pir: record must be 32 bytes")
	}
	tag := computeTag(key, uint32(index), record)
	return append([]byte(nil), tag[:]...), nil
}

func computeTag(key []byte, index uint32, record []byte) [TagSize]byte {
	var message [4 + RecordSize]byte
	binary.BigEndian.PutUint32(message[:4], index)
	copy(message[4:], record)

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message[:])

	var tag [TagSize]byte
	copy(tag[:], mac.Sum(nil))
	return tag
}

// GenerateQuery draws q0 uniformly from the host random source, masks the
// trailing bits beyond N to zero, and computes q1 = q0 XOR e_j. Bit i lives at
// byte i/8, bit position i%8 (least significant bit first).
func GenerateQuery(random io.Reader, n, j int) (q0, q1 []byte, err error) {
	if random == nil {
		return nil, nil, errors.New("pir: nil random source")
	}
	if n < 1 || n > MaxRecords {
		return nil, nil, errors.New("pir: invalid database size")
	}
	if j < 0 || j >= n {
		return nil, nil, errors.New("pir: index out of range")
	}

	q0 = make([]byte, QueryLength(n))
	if _, err := io.ReadFull(random, q0); err != nil {
		return nil, nil, err
	}
	maskTrailingBits(q0, n)

	q1 = append([]byte(nil), q0...)
	q1[j/8] ^= byte(1 << uint(j%8))
	return q0, q1, nil
}

func maskTrailingBits(query []byte, n int) {
	if rem := n % 8; rem != 0 {
		query[len(query)-1] &= byte(1<<uint(rem)) - 1
	}
}

func validQuery(query []byte, n int) bool {
	if n < 1 || n > MaxRecords || len(query) != QueryLength(n) {
		return false
	}
	if rem := n % 8; rem != 0 {
		if query[len(query)-1]>>uint(rem) != 0 {
			return false
		}
	}
	return true
}

func bitSet(query []byte, i int) bool {
	return query[i/8]&(1<<uint(i%8)) != 0
}

// QueryDigest binds the database size, vector length, and vector contents.
func QueryDigest(query []byte, n int) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(queryDigestDomain))

	var lengths [8]byte
	binary.BigEndian.PutUint32(lengths[:4], uint32(n))
	binary.BigEndian.PutUint32(lengths[4:], uint32(len(query)))
	_, _ = h.Write(lengths[:])
	_, _ = h.Write(query)

	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// ResponseSigningMessage returns the exact byte string signed by a server:
// the query digest, the response payload, and the snapshot ID, with the
// domain and lengths framed by this package.
func ResponseSigningMessage(query []byte, n int, payload []byte, snapshotID string) []byte {
	digest := QueryDigest(query, n)

	message := make([]byte, 0, len(responseSigningDomain)+32+8+len(payload)+4+len(snapshotID))
	message = append(message, []byte(responseSigningDomain)...)
	message = append(message, digest[:]...)

	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	message = append(message, length[:]...)
	message = append(message, payload...)

	binary.BigEndian.PutUint32(length[:], uint32(len(snapshotID)))
	message = append(message, length[:]...)
	message = append(message, snapshotID...)
	return message
}

// VerifyResponseSignature checks a response signature against the server
// public key carried in the response. Identity pinning is the client's job.
func VerifyResponseSignature(response Response, query []byte, n int) bool {
	if len(response.ServerID) != 32 || len(response.Signature) != 64 {
		return false
	}
	return verifySignature(response.ServerID, ResponseSigningMessage(query, n, response.Payload, response.SnapshotID), response.Signature)
}
