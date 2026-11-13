package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"io"
)

// Responder is the client-facing half of a PIR server.
type Responder interface {
	Respond(query []byte) (Response, error)
}

// ClientConfig pins the two expected server identities and the published
// database registration. Random must be the host random source; nil selects
// crypto/rand.Reader.
type ClientConfig struct {
	Registry  Registry
	HMACKey   []byte
	ServerIDs [2]ed25519.PublicKey
	Random    io.Reader
}

// Client is a two-server XOR-PIR client.
type Client struct {
	registry  Registry
	hmacKey   []byte
	serverIDs [2]ed25519.PublicKey
	random    io.Reader
}

// NewClient constructs a client with pinned server identities.
func NewClient(registry Registry, hmacKey []byte, serverIDs [2]ed25519.PublicKey, random io.Reader) (*Client, error) {
	return NewClientWithConfig(ClientConfig{
		Registry:  registry,
		HMACKey:   hmacKey,
		ServerIDs: serverIDs,
		Random:    random,
	})
}

// NewClientWithConfig constructs a client from cfg.
func NewClientWithConfig(cfg ClientConfig) (*Client, error) {
	if cfg.Registry.N < 1 || cfg.Registry.N > MaxRecords {
		return nil, errors.New("pir: invalid registry size")
	}
	if cfg.Registry.SnapshotID == "" {
		return nil, errors.New("pir: empty registry snapshot ID")
	}
	if len(cfg.HMACKey) == 0 {
		return nil, errors.New("pir: empty HMAC key")
	}
	for i := range cfg.ServerIDs {
		if len(cfg.ServerIDs[i]) != ed25519.PublicKeySize {
			return nil, errors.New("pir: invalid server identity")
		}
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}

	return &Client{
		registry: cfg.Registry,
		hmacKey:  append([]byte(nil), cfg.HMACKey...),
		serverIDs: [2]ed25519.PublicKey{
			append(ed25519.PublicKey(nil), cfg.ServerIDs[0]...),
			append(ed25519.PublicKey(nil), cfg.ServerIDs[1]...),
		},
		random: cfg.Random,
	}, nil
}

// Query retrieves record j. Every failure is reported as exactly
// ErrVerification, without indicating which check failed.
func (c *Client) Query(j int, first, second Responder) ([]byte, error) {
	if first == nil || second == nil || j < 0 || j >= c.registry.N {
		return nil, ErrVerification
	}

	q0, q1, err := GenerateQuery(c.random, c.registry.N, j)
	if err != nil {
		return nil, ErrVerification
	}

	r0, err := first.Respond(q0)
	if err != nil {
		return nil, ErrVerification
	}
	r1, err := second.Respond(q1)
	if err != nil {
		return nil, ErrVerification
	}

	if !c.verifyResponse(r0, q0, c.serverIDs[0]) ||
		!c.verifyResponse(r1, q1, c.serverIDs[1]) {
		return nil, ErrVerification
	}
	if bytes.Equal(r0.ServerID, r1.ServerID) {
		return nil, ErrVerification
	}

	row := make([]byte, RowSize)
	for i := 0; i < RowSize; i++ {
		row[i] = r0.Payload[i] ^ r1.Payload[i]
	}
	record := row[:RecordSize]
	if !VerifyRowTag(c.hmacKey, j, row) {
		return nil, ErrVerification
	}
	return append([]byte(nil), record...), nil
}

// QueryRecord is an alias for Query.
func (c *Client) QueryRecord(j int, first, second Responder) ([]byte, error) {
	return c.Query(j, first, second)
}

func (c *Client) verifyResponse(response Response, query []byte, expectedID ed25519.PublicKey) bool {
	if len(response.Payload) != RowSize ||
		len(response.ServerID) != ed25519.PublicKeySize ||
		len(response.Signature) != ed25519.SignatureSize {
		return false
	}
	if !bytes.Equal(response.ServerID, expectedID) {
		return false
	}
	if response.SnapshotID != c.registry.SnapshotID {
		return false
	}
	if response.MerkleRoot != c.registry.MerkleRoot {
		return false
	}

	message := ResponseSigningMessage(query, c.registry.N, response.Payload, response.SnapshotID)
	return verifySignature(response.ServerID, message, response.Signature)
}

// VerifyRowTag checks the row's 32-byte HMAC tag against key and index.
func VerifyRowTag(key []byte, index int, row []byte) bool {
	if len(key) == 0 || index < 0 || index >= MaxRecords || len(row) != RowSize {
		return false
	}
	expected := computeTag(key, uint32(index), row[:RecordSize])
	return hmac.Equal(row[RecordSize:], expected[:])
}
