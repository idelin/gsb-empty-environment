package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
)

// Server holds one copy of the published database and an Ed25519 identity.
// It never receives the HMAC key.
type Server struct {
	db         *Database
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

// GenerateServerIdentity creates a fresh Ed25519 key pair for a PIR server.
func GenerateServerIdentity() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// NewServer constructs a server over db. Both servers must be given the same
// published database (and therefore the same snapshot ID and Merkle root).
func NewServer(db *Database, privateKey ed25519.PrivateKey) (*Server, error) {
	if db == nil {
		return nil, errors.New("pir: nil database")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("pir: invalid Ed25519 private key")
	}

	key := append(ed25519.PrivateKey(nil), privateKey...)
	publicKey := append(ed25519.PublicKey(nil), key.Public().(ed25519.PublicKey)...)
	return &Server{db: db, privateKey: key, publicKey: publicKey}, nil
}

// PublicKey returns a copy of the server identity.
func (s *Server) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), s.publicKey...)
}

// Registry returns the database registration served by this server.
func (s *Server) Registry() Registry {
	return s.db.registry
}

// Respond validates a query vector, XORs all selected rows, and signs the
// query digest, response payload, and snapshot ID.
func (s *Server) Respond(query []byte) (Response, error) {
	n := s.db.registry.N
	if !validQuery(query, n) {
		return Response{}, ErrMalformedQuery
	}

	var payload [RowSize]byte
	for i := 0; i < n; i++ {
		if !bitSet(query, i) {
			continue
		}
		for k := 0; k < RowSize; k++ {
			payload[k] ^= s.db.rows[i][k]
		}
	}
	return s.signResponse(query, payload[:], s.db.registry.SnapshotID), nil
}

// signResponse is also used by the malicious fixture to produce responses
// whose signatures are genuinely valid for tampered payloads.
func (s *Server) signResponse(query, payload []byte, snapshotID string) Response {
	message := ResponseSigningMessage(query, s.db.registry.N, payload, snapshotID)
	signature := ed25519.Sign(s.privateKey, message)

	return Response{
		Payload:    append([]byte(nil), payload...),
		SnapshotID: snapshotID,
		MerkleRoot: s.db.registry.MerkleRoot,
		ServerID:   append([]byte(nil), s.publicKey...),
		Signature:  append([]byte(nil), signature...),
	}
}

func verifySignature(publicKey, message, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, signature)
}
