package netsec

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	envelopeFormat = "netsec.state.envelope.v1"
	payloadFormat  = "netsec.state.payload.v1"
	payloadMagic   = "NSST1"
	maxHeaderSize  = 1 << 20
)

// ArtifactKind identifies the role of an encrypted artifact. The role is part
// of the key derivation and associated data, so a state file cannot be
// substituted for a backup, lock, or version record.
type ArtifactKind string

const (
	ArtifactState   ArtifactKind = "state"
	ArtifactVersion ArtifactKind = "version"
	ArtifactLock    ArtifactKind = "lock"
	ArtifactBackup  ArtifactKind = "backup"
)

// envelope is the only part of an artifact stored in cleartext. It carries no
// workspace, account, state content, or version metadata.
type envelope struct {
	Format     string `json:"format"`
	Generation uint64 `json:"generation"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// payloadHeader is authenticated and encrypted together with the payload. It
// makes binding mismatches explicit after decryption and provides defense in
// depth beyond the associated data.
type payloadHeader struct {
	Format      string `json:"format"`
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace"`
	AccountID   string `json:"account"`
	Generation  uint64 `json:"generation"`
	RecordID    string `json:"record"`
	Sequence    uint64 `json:"sequence"`
}

type recordContext struct {
	WorkspaceID string
	AccountID   string
	Kind        ArtifactKind
	RecordID    string
	Generation  uint64
	Sequence    uint64
}

func (rc recordContext) associatedData() []byte {
	return lengthPrefixed(
		envelopeFormat,
		string(rc.Kind),
		rc.WorkspaceID,
		rc.AccountID,
		strconv.FormatUint(rc.Generation, 10),
		rc.RecordID,
		strconv.FormatUint(rc.Sequence, 10),
	)
}

func (s *Store) seal(ctx context.Context, rc recordContext, plaintext []byte) ([]byte, error) {
	if rc.Generation == 0 {
		return nil, fmt.Errorf("%w: generation 0 is reserved", ErrKeyGeneration)
	}
	if rc.WorkspaceID == "" || rc.AccountID == "" {
		return nil, fmt.Errorf("%w: incomplete artifact binding", ErrInvalidConfig)
	}

	masterKey, err := s.keys.Key(ctx, rc.Generation)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyGeneration, err)
	}

	workspaceKey, err := hkdf.Key(
		sha256.New,
		masterKey,
		nil,
		lengthPrefixedInfo("workspace", rc.WorkspaceID, rc.AccountID),
		32,
	)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive workspace key: %w", err)
	}

	counter := s.nonceCounter.Add(1)
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)

	saltInput := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, saltInput); err != nil {
		return nil, fmt.Errorf("netsec: read random salt: %w", err)
	}
	saltSum := sha256.Sum256(append(saltInput, counterBytes[:]...))
	salt := saltSum[:]
	recordKey, err := hkdf.Key(
		sha256.New,
		workspaceKey,
		salt,
		lengthPrefixedInfo("record", string(rc.Kind), rc.RecordID),
		32,
	)
	clear(workspaceKey)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive record key: %w", err)
	}
	defer clear(recordKey)

	block, err := aes.NewCipher(recordKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceInput := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(s.rand, nonceInput); err != nil {
		return nil, fmt.Errorf("netsec: read random nonce: %w", err)
	}
	nonceSum := sha256.Sum256(append(nonceInput, counterBytes[:]...))
	nonce := nonceSum[:aead.NonceSize()]

	header := payloadHeader{
		Format:      payloadFormat,
		Kind:        string(rc.Kind),
		WorkspaceID: rc.WorkspaceID,
		AccountID:   rc.AccountID,
		Generation:  rc.Generation,
		RecordID:    rc.RecordID,
		Sequence:    rc.Sequence,
	}
	payload, err := encodePayload(header, plaintext)
	if err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, payload, rc.associatedData())
	clear(payload)

	raw, err := json.Marshal(envelope{
		Format:     envelopeFormat,
		Generation: rc.Generation,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return nil, fmt.Errorf("netsec: encode envelope: %w", err)
	}
	return raw, nil
}

func (s *Store) open(ctx context.Context, rc recordContext, raw []byte) ([]byte, error) {
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if env.Generation != rc.Generation {
		return nil, fmt.Errorf("%w: envelope generation %d does not match expected %d", ErrIntegrity, env.Generation, rc.Generation)
	}

	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid salt", ErrIntegrity)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid nonce", ErrIntegrity)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ciphertext", ErrIntegrity)
	}

	masterKey, err := s.keys.Key(ctx, rc.Generation)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyGeneration, err)
	}
	workspaceKey, err := hkdf.Key(
		sha256.New,
		masterKey,
		nil,
		lengthPrefixedInfo("workspace", rc.WorkspaceID, rc.AccountID),
		32,
	)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive workspace key: %w", err)
	}
	recordKey, err := hkdf.Key(
		sha256.New,
		workspaceKey,
		salt,
		lengthPrefixedInfo("record", string(rc.Kind), rc.RecordID),
		32,
	)
	clear(workspaceKey)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive record key: %w", err)
	}
	defer clear(recordKey)

	block, err := aes.NewCipher(recordKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: invalid nonce length", ErrIntegrity)
	}
	payload, err := aead.Open(nil, nonce, ciphertext, rc.associatedData())
	if err != nil {
		return nil, fmt.Errorf("%w: artifact is not authentic for this workspace, account, generation, or role", ErrIntegrity)
	}
	defer clear(payload)

	header, data, err := decodePayload(payload)
	if err != nil {
		return nil, err
	}
	switch {
	case header.Format != payloadFormat:
		return nil, fmt.Errorf("%w: unsupported payload format", ErrIntegrity)
	case header.Kind != string(rc.Kind):
		return nil, fmt.Errorf("%w: artifact role mismatch", ErrIntegrity)
	case header.WorkspaceID != rc.WorkspaceID:
		return nil, fmt.Errorf("%w: artifact workspace mismatch", errors.Join(ErrIntegrity, ErrWorkspaceMismatch))
	case header.AccountID != rc.AccountID:
		return nil, fmt.Errorf("%w: artifact account mismatch", errors.Join(ErrIntegrity, ErrAccountMismatch))
	case header.Generation != rc.Generation:
		return nil, fmt.Errorf("%w: artifact generation mismatch", ErrIntegrity)
	case header.RecordID != rc.RecordID:
		return nil, fmt.Errorf("%w: artifact record mismatch", ErrIntegrity)
	case header.Sequence != rc.Sequence:
		return nil, fmt.Errorf("%w: artifact sequence mismatch", ErrIntegrity)
	}

	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func parseEnvelope(raw []byte) (envelope, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return envelope{}, fmt.Errorf("%w: malformed envelope", ErrIntegrity)
	}
	if env.Format != envelopeFormat {
		return envelope{}, fmt.Errorf("%w: unsupported envelope format", ErrIntegrity)
	}
	if env.Generation == 0 || env.Salt == "" || env.Nonce == "" || env.Ciphertext == "" {
		return envelope{}, fmt.Errorf("%w: incomplete envelope", ErrIntegrity)
	}
	return env, nil
}

func encodePayload(header payloadHeader, data []byte) ([]byte, error) {
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("netsec: encode payload header: %w", err)
	}
	if len(headerBytes) > maxHeaderSize {
		return nil, fmt.Errorf("%w: payload header too large", ErrIntegrity)
	}
	var buf bytes.Buffer
	buf.WriteString(payloadMagic)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(headerBytes)))
	buf.Write(length[:])
	buf.Write(headerBytes)
	buf.Write(data)
	return buf.Bytes(), nil
}

func decodePayload(payload []byte) (payloadHeader, []byte, error) {
	if len(payload) < len(payloadMagic)+4 {
		return payloadHeader{}, nil, fmt.Errorf("%w: truncated payload", ErrIntegrity)
	}
	if string(payload[:len(payloadMagic)]) != payloadMagic {
		return payloadHeader{}, nil, fmt.Errorf("%w: invalid payload magic", ErrIntegrity)
	}
	offset := len(payloadMagic)
	headerLen := binary.BigEndian.Uint32(payload[offset : offset+4])
	offset += 4
	if headerLen == 0 || headerLen > maxHeaderSize || uint64(offset)+uint64(headerLen) > uint64(len(payload)) {
		return payloadHeader{}, nil, fmt.Errorf("%w: invalid payload header length", ErrIntegrity)
	}
	var header payloadHeader
	if err := json.Unmarshal(payload[offset:offset+int(headerLen)], &header); err != nil {
		return payloadHeader{}, nil, fmt.Errorf("%w: malformed payload header", ErrIntegrity)
	}
	data := payload[offset+int(headerLen):]
	return header, data, nil
}

func lengthPrefixed(parts ...string) []byte {
	var buf bytes.Buffer
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		buf.Write(length[:])
		buf.WriteString(part)
	}
	return buf.Bytes()
}

func lengthPrefixedInfo(label string, parts ...string) string {
	raw := lengthPrefixed(append([]string{"netsec.state.v1", label}, parts...)...)
	return string(raw)
}
