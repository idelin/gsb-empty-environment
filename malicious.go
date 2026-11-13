package netsec

import (
	"crypto/ed25519"
	"errors"
)

// Attack selects a malicious-fixture behavior. The fixture always keeps the
// server's real private key so tests exercise the actual signature and HMAC
// checks rather than a synthetic error path.
type Attack int

const (
	AttackNone Attack = iota
	// AttackFlipPayloadSigned flips one response bit and re-signs it. The
	// Ed25519 signature is valid; only the final row tag catches the change.
	AttackFlipPayloadSigned
	// AttackFlipPayloadUnsigned flips one response bit after signing.
	AttackFlipPayloadUnsigned
	// AttackChangeSnapshot changes the snapshot ID and produces a valid
	// signature for the changed value.
	AttackChangeSnapshot
	// AttackChangeMerkleRoot replaces the advertised root.
	AttackChangeMerkleRoot
	// AttackCorruptSignature damages the signature.
	AttackCorruptSignature
	// AttackReplay returns a previously signed response for another query.
	AttackReplay
)

// MaliciousServer wraps an otherwise honest server with one attack behavior.
type MaliciousServer struct {
	Server *Server
	Attack Attack
	Replay Response
}

// NewMaliciousServer constructs a fixture for attack.
func NewMaliciousServer(server *Server, attack Attack) *MaliciousServer {
	return &MaliciousServer{Server: server, Attack: attack}
}

// NewReplayServer constructs a fixture that always returns cached, a genuine
// response produced for a different query.
func NewReplayServer(server *Server, cached Response) *MaliciousServer {
	return &MaliciousServer{Server: server, Attack: AttackReplay, Replay: cached.Clone()}
}

// Respond returns the attacked response.
func (m *MaliciousServer) Respond(query []byte) (Response, error) {
	if m == nil || m.Server == nil {
		return Response{}, errors.New("pir: nil malicious server")
	}
	if m.Attack == AttackReplay {
		if len(m.Replay.Payload) == 0 {
			return Response{}, errors.New("pir: replay response not configured")
		}
		return m.Replay.Clone(), nil
	}

	response, err := m.Server.Respond(query)
	if err != nil {
		return Response{}, err
	}

	switch m.Attack {
	case AttackNone:
		return response, nil
	case AttackFlipPayloadSigned:
		response.Payload[0] ^= 0x01
		signed := m.Server.signResponse(query, response.Payload, response.SnapshotID)
		signed.MerkleRoot = response.MerkleRoot
		return signed, nil
	case AttackFlipPayloadUnsigned:
		response.Payload[0] ^= 0x01
		return response, nil
	case AttackChangeSnapshot:
		response.SnapshotID = response.SnapshotID + "-tampered"
		signed := m.Server.signResponse(query, response.Payload, response.SnapshotID)
		signed.MerkleRoot = response.MerkleRoot
		return signed, nil
	case AttackChangeMerkleRoot:
		response.MerkleRoot[0] ^= 0x01
		return response, nil
	case AttackCorruptSignature:
		response.Signature[0] ^= 0x01
		return response, nil
	default:
		return Response{}, errors.New("pir: unknown attack")
	}
}

// NewSameIdentityServers creates two servers with the same Ed25519 identity.
// The client must reject the pair even though both signatures verify.
func NewSameIdentityServers(db *Database, privateKey ed25519.PrivateKey) (*Server, *Server, error) {
	first, err := NewServer(db, privateKey)
	if err != nil {
		return nil, nil, err
	}
	second, err := NewServer(db, privateKey)
	if err != nil {
		return nil, nil, err
	}
	return first, second, nil
}
