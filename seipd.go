package netsec

import (
	"encoding/binary"
	"fmt"
	"math"
)

const packetHeaderSize = 1 + 1 + 1 + 1 + SaltSize

// Message is a v2 SEIPD message: a packet header followed by encrypted
// chunks.
type Message struct {
	Version   byte
	Cipher    CipherFunction
	Mode      AEADMode
	ChunkSize int
	Salt      []byte
	Chunks    []Chunk
}

// SEIPD and Packet are aliases for Message.
type SEIPD = Message
type Packet = Message

// Decrypt derives the message key from sessionKey and decrypts the message.
// An optional policy overrides the default AllowSequenceReuse behavior.
func (m *Message) Decrypt(sessionKey []byte, policies ...SequencePolicy) ([]byte, error) {
	if len(policies) > 1 {
		return nil, fmt.Errorf("netsec: expected at most one SequencePolicy")
	}
	policy := AllowSequenceReuse
	if len(policies) == 1 {
		policy = policies[0]
	}
	c, err := NewCipher(sessionKey, Config{
		Cipher:    m.Cipher,
		Mode:      m.Mode,
		ChunkSize: m.ChunkSize,
		Salt:      m.Salt,
		Policy:    policy,
	})
	if err != nil {
		return nil, err
	}
	return c.Decrypt(m.Chunks)
}

// DecryptWithConfig decrypts the message with an explicit config. Only the
// Policy and Strict fields are used; the message header supplies cipher, mode,
// chunk size, and salt.
func (m *Message) DecryptWithConfig(sessionKey []byte, cfg Config) ([]byte, error) {
	c, err := NewCipher(sessionKey, Config{
		Cipher:    m.Cipher,
		Mode:      m.Mode,
		ChunkSize: m.ChunkSize,
		Salt:      m.Salt,
		Policy:    cfg.Policy,
		Strict:    cfg.Strict,
	})
	if err != nil {
		return nil, err
	}
	return c.Decrypt(m.Chunks)
}

// Validate checks the message's chunk sequence. The default policy is
// AllowSequenceReuse.
func (m *Message) Validate(policies ...SequencePolicy) error {
	if len(policies) > 1 {
		return fmt.Errorf("netsec: expected at most one SequencePolicy")
	}
	policy := AllowSequenceReuse
	if len(policies) == 1 {
		policy = policies[0]
	}
	return ValidateSequences(m.Chunks, policy)
}

// MarshalBinary serializes the message in the v2 SEIPD chunked format. Each
// chunk is encoded as its 16-byte tag followed by its ciphertext; sequence
// numbers are implied by position. Use MarshalIndexed when sequence numbers
// must be preserved.
func (m *Message) MarshalBinary() ([]byte, error) {
	if err := ValidateSequences(m.Chunks, RequireSequential); err != nil {
		return nil, err
	}
	header, err := m.marshalHeader()
	if err != nil {
		return nil, err
	}
	out := header
	for _, chunk := range m.Chunks {
		if len(chunk.Tag) != TagSize {
			return nil, fmt.Errorf("%w: tag length %d", ErrMalformedPacket, len(chunk.Tag))
		}
		data := chunk.ciphertext()
		if len(data) > m.ChunkSize {
			return nil, fmt.Errorf("%w: got %d bytes, limit %d", ErrChunkTooLarge, len(data), m.ChunkSize)
		}
		out = append(out, chunk.Tag...)
		out = append(out, data...)
	}
	return out, nil
}

// MarshalSEIPD is an alias for MarshalBinary.
func (m *Message) MarshalSEIPD() ([]byte, error) {
	return m.MarshalBinary()
}

// MarshalIndexed serializes the message with an explicit sequence number for
// every chunk. This format preserves duplicate/reordered sequence numbers and
// is intended for reproducing and testing the chunk-sequence behavior.
func (m *Message) MarshalIndexed() ([]byte, error) {
	header, err := m.marshalHeader()
	if err != nil {
		return nil, err
	}
	if uint64(len(m.Chunks)) > math.MaxUint32 {
		return nil, fmt.Errorf("netsec: too many chunks")
	}
	out := header
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(m.Chunks)))
	out = append(out, count[:]...)
	for _, chunk := range m.Chunks {
		sequence, err := chunk.sequence()
		if err != nil {
			return nil, err
		}
		if len(chunk.Tag) != TagSize {
			return nil, fmt.Errorf("%w: tag length %d", ErrMalformedPacket, len(chunk.Tag))
		}
		data := chunk.ciphertext()
		if len(data) > math.MaxUint32 {
			return nil, fmt.Errorf("%w: ciphertext too large", ErrChunkTooLarge)
		}
		var seq [8]byte
		binary.BigEndian.PutUint64(seq[:], sequence)
		out = append(out, seq[:]...)
		flags := byte(0)
		if chunk.IsFinal() {
			flags = 1
		}
		out = append(out, flags, byte(TagSize))
		out = append(out, chunk.Tag...)
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(data)))
		out = append(out, length[:]...)
		out = append(out, data...)
	}
	return out, nil
}

// UnmarshalBinary parses a v2 SEIPD message in the positional chunk format.
func (m *Message) UnmarshalBinary(data []byte) error {
	header, body, err := parseHeader(data)
	if err != nil {
		return err
	}
	fullRecord := header.chunkSize + TagSize
	if len(body) == 0 {
		return fmt.Errorf("%w: no chunks", ErrMalformedPacket)
	}

	records := make([][]byte, 0, len(body)/fullRecord+1)
	remaining := body
	for len(remaining) > fullRecord {
		records = append(records, remaining[:fullRecord])
		remaining = remaining[fullRecord:]
	}
	if len(remaining) == 0 || len(remaining) < TagSize {
		return fmt.Errorf("%w: truncated chunk", ErrMalformedPacket)
	}
	records = append(records, remaining)

	chunks := make([]Chunk, len(records))
	for i, record := range records {
		final := i == len(records)-1
		if !final && len(record) != fullRecord {
			return fmt.Errorf("%w: short non-final chunk", ErrMalformedPacket)
		}
		sequence := uint64(i)
		if final {
			sequence = FinalSequence
		}
		chunks[i] = Chunk{
			Sequence:   sequence,
			Index:      sequence,
			Final:      final,
			Tag:        append([]byte(nil), record[:TagSize]...),
			Ciphertext: append([]byte(nil), record[TagSize:]...),
		}
	}

	*m = Message{
		Version:   header.version,
		Cipher:    header.cipher,
		Mode:      header.mode,
		ChunkSize: header.chunkSize,
		Salt:      header.salt,
		Chunks:    chunks,
	}
	return nil
}

// UnmarshalIndexed parses a message produced by MarshalIndexed.
func (m *Message) UnmarshalIndexed(data []byte) error {
	header, body, err := parseHeader(data)
	if err != nil {
		return err
	}
	if len(body) < 4 {
		return fmt.Errorf("%w: missing chunk count", ErrMalformedPacket)
	}
	count := binary.BigEndian.Uint32(body[:4])
	body = body[4:]
	if uint64(count) > uint64(len(body))/30 {
		return fmt.Errorf("%w: impossible chunk count %d", ErrMalformedPacket, count)
	}
	chunks := make([]Chunk, 0, count)
	for i := uint32(0); i < count; i++ {
		if len(body) < 8+1+1 {
			return fmt.Errorf("%w: truncated chunk header", ErrMalformedPacket)
		}
		sequence := binary.BigEndian.Uint64(body[:8])
		flags := body[8]
		tagLength := int(body[9])
		body = body[10:]
		if flags&^byte(1) != 0 || tagLength != TagSize {
			return fmt.Errorf("%w: invalid chunk flags or tag length", ErrMalformedPacket)
		}
		if len(body) < tagLength+4 {
			return fmt.Errorf("%w: truncated chunk", ErrMalformedPacket)
		}
		tag := append([]byte(nil), body[:tagLength]...)
		body = body[tagLength:]
		dataLength := binary.BigEndian.Uint32(body[:4])
		body = body[4:]
		if uint64(len(body)) < uint64(dataLength) {
			return fmt.Errorf("%w: truncated ciphertext", ErrMalformedPacket)
		}
		ciphertext := append([]byte(nil), body[:dataLength]...)
		body = body[dataLength:]
		chunks = append(chunks, Chunk{
			Sequence:   sequence,
			Index:      sequence,
			Final:      flags&1 != 0 || sequence == FinalSequence,
			Tag:        tag,
			Ciphertext: ciphertext,
		})
	}
	if len(body) != 0 {
		return fmt.Errorf("%w: trailing data", ErrMalformedPacket)
	}
	*m = Message{
		Version:   header.version,
		Cipher:    header.cipher,
		Mode:      header.mode,
		ChunkSize: header.chunkSize,
		Salt:      header.salt,
		Chunks:    chunks,
	}
	return nil
}

// ParseMessage parses a positional v2 SEIPD message.
func ParseMessage(data []byte) (*Message, error) {
	message := new(Message)
	if err := message.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return message, nil
}

// ParseSEIPD is an alias for ParseMessage.
func ParseSEIPD(data []byte) (*Message, error) {
	return ParseMessage(data)
}

// ParseIndexed parses a message produced by MarshalIndexed.
func ParseIndexed(data []byte) (*Message, error) {
	message := new(Message)
	if err := message.UnmarshalIndexed(data); err != nil {
		return nil, err
	}
	return message, nil
}

type parsedHeader struct {
	version   byte
	cipher    CipherFunction
	mode      AEADMode
	chunkSize int
	salt      []byte
}

func parseHeader(data []byte) (parsedHeader, []byte, error) {
	if len(data) < packetHeaderSize {
		return parsedHeader{}, nil, fmt.Errorf("%w: short header", ErrMalformedPacket)
	}
	if data[0] != Version2 {
		return parsedHeader{}, nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedPacket, data[0])
	}
	cipherFn := CipherFunction(data[1])
	if cipherFn.KeySize() == 0 {
		return parsedHeader{}, nil, fmt.Errorf("%w: %d", ErrUnsupportedCipher, data[1])
	}
	mode := AEADMode(data[2])
	if mode < AEADModeEAX || mode > AEADModeGCM {
		return parsedHeader{}, nil, fmt.Errorf("%w: %d", ErrUnsupportedMode, data[2])
	}
	chunkSize, err := chunkSizeFromExponent(data[3])
	if err != nil {
		return parsedHeader{}, nil, err
	}
	return parsedHeader{
		version:   data[0],
		cipher:    cipherFn,
		mode:      mode,
		chunkSize: chunkSize,
		salt:      append([]byte(nil), data[4:packetHeaderSize]...),
	}, data[packetHeaderSize:], nil
}

func (m *Message) marshalHeader() ([]byte, error) {
	version := m.Version
	if version == 0 {
		version = Version2
	}
	if version != Version2 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedPacket, version)
	}
	if m.Cipher.KeySize() == 0 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedCipher, uint8(m.Cipher))
	}
	if m.Mode != AEADModeGCM {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMode, m.Mode)
	}
	chunkSize := m.ChunkSize
	if chunkSize == 0 {
		chunkSize = DefaultChunkSize
	}
	exponent, err := chunkExponent(chunkSize)
	if err != nil {
		return nil, err
	}
	if len(m.Salt) != SaltSize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidSalt, len(m.Salt), SaltSize)
	}
	header := make([]byte, 0, packetHeaderSize)
	header = append(header, version, byte(m.Cipher), byte(m.Mode), exponent)
	header = append(header, m.Salt...)
	return header, nil
}

// Encrypt seals plaintext with a fresh salt and returns a positional v2
// SEIPD message.
func Encrypt(sessionKey, plaintext []byte, configs ...Config) ([]byte, error) {
	message, err := EncryptMessage(sessionKey, plaintext, configs...)
	if err != nil {
		return nil, err
	}
	return message.MarshalBinary()
}

// EncryptMessage seals plaintext and returns the in-memory message.
func EncryptMessage(sessionKey, plaintext []byte, configs ...Config) (*Message, error) {
	c, err := NewCipher(sessionKey, configs...)
	if err != nil {
		return nil, err
	}
	return c.Encrypt(plaintext)
}

// Decrypt opens a positional v2 SEIPD message. The config only contributes
// the sequence policy; cipher, mode, chunk size, and salt are read from the
// message header.
func Decrypt(sessionKey, ciphertext []byte, configs ...Config) ([]byte, error) {
	message, err := ParseMessage(ciphertext)
	if err != nil {
		return nil, err
	}
	return DecryptMessage(sessionKey, message, configs...)
}

// DecryptMessage opens an in-memory message.
func DecryptMessage(sessionKey []byte, message *Message, configs ...Config) ([]byte, error) {
	if len(configs) > 1 {
		return nil, fmt.Errorf("netsec: expected at most one Config")
	}
	policy := AllowSequenceReuse
	if len(configs) == 1 {
		policy = configs[0].Policy
		if configs[0].Strict {
			policy = RequireSequential
		}
	}
	return message.Decrypt(sessionKey, policy)
}

// EncryptIndexed seals plaintext and serializes every chunk with its explicit
// sequence number.
func EncryptIndexed(sessionKey, plaintext []byte, configs ...Config) ([]byte, error) {
	message, err := EncryptMessage(sessionKey, plaintext, configs...)
	if err != nil {
		return nil, err
	}
	return message.MarshalIndexed()
}

// DecryptIndexed parses an indexed message and decrypts it.
func DecryptIndexed(sessionKey, ciphertext []byte, configs ...Config) ([]byte, error) {
	message, err := ParseIndexed(ciphertext)
	if err != nil {
		return nil, err
	}
	return DecryptMessage(sessionKey, message, configs...)
}
