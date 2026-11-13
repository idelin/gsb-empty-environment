package netsec

import (
	"bytes"
	"fmt"
)

// Chunk is one encrypted SEIPD chunk.
//
// Sequence is the chunk index used in the nonce and associated data. The
// final chunk uses FinalSequence. Index is a compatibility alias: when
// Sequence is zero and Index is non-zero, Index is used.
//
// Nonce is informational. OpenChunk always derives the nonce from the
// sequence number and, if Nonce is non-empty, verifies that the stored value
// agrees with the derived value.
type Chunk struct {
	Sequence   uint64
	Index      uint64
	Final      bool
	Nonce      []byte
	Tag        []byte
	Ciphertext []byte
	Data       []byte
}

func (c Chunk) sequence() (uint64, error) {
	if c.Sequence != 0 && c.Index != 0 && c.Sequence != c.Index {
		return 0, fmt.Errorf("netsec: conflicting chunk sequence %d and index %d", c.Sequence, c.Index)
	}
	if c.Sequence != 0 {
		return c.Sequence, nil
	}
	return c.Index, nil
}

func (c Chunk) ciphertext() []byte {
	if c.Ciphertext != nil {
		return c.Ciphertext
	}
	return c.Data
}

// IsFinal reports whether the chunk is the final chunk.
func (c Chunk) IsFinal() bool {
	sequence, err := c.sequence()
	return c.Final || (err == nil && sequence == FinalSequence)
}

// Validate checks the chunk's sequence and tag shape.
func (c Chunk) Validate() error {
	sequence, err := c.sequence()
	if err != nil {
		return err
	}
	if c.Final && sequence != FinalSequence {
		return ErrInvalidFinal
	}
	if len(c.Tag) != TagSize {
		return fmt.Errorf("%w: tag length %d", ErrMalformedPacket, len(c.Tag))
	}
	return nil
}

// SealChunk encrypts plaintext as one chunk. A final chunk always uses the
// FinalSequence marker. Callers may pass the same sequence number more than
// once; that is the behavior under test.
func (c *Cipher) SealChunk(sequence uint64, final bool, plaintext []byte) (Chunk, error) {
	if len(plaintext) > c.ChunkSize {
		return Chunk{}, fmt.Errorf("%w: got %d bytes, limit %d", ErrChunkTooLarge, len(plaintext), c.ChunkSize)
	}
	effective := sequence
	if final {
		effective = FinalSequence
	} else if sequence == FinalSequence {
		return Chunk{}, ErrInvalidFinal
	}

	nonce := c.nonceFor(effective)
	sealed := c.aead.Seal(nil, nonce, plaintext, c.associatedData(effective))
	tag := append([]byte(nil), sealed[:TagSize]...)
	ciphertext := append([]byte(nil), sealed[TagSize:]...)

	return Chunk{
		Sequence:   effective,
		Index:      effective,
		Final:      final || effective == FinalSequence,
		Nonce:      nonce,
		Tag:        tag,
		Ciphertext: ciphertext,
	}, nil
}

// Seal encrypts one non-final chunk with an explicit sequence number.
func (c *Cipher) Seal(sequence uint64, plaintext []byte) (Chunk, error) {
	return c.SealChunk(sequence, sequence == FinalSequence, plaintext)
}

// SealFinal encrypts the final chunk.
func (c *Cipher) SealFinal(plaintext []byte) (Chunk, error) {
	return c.SealChunk(FinalSequence, true, plaintext)
}

// OpenChunk authenticates and decrypts one chunk using its sequence number.
func (c *Cipher) OpenChunk(chunk Chunk) ([]byte, error) {
	sequence, err := chunk.sequence()
	if err != nil {
		return nil, err
	}
	if chunk.Final && sequence != FinalSequence {
		return nil, ErrInvalidFinal
	}
	if len(chunk.Tag) != TagSize {
		return nil, fmt.Errorf("%w: tag length %d", ErrMalformedPacket, len(chunk.Tag))
	}
	data := chunk.ciphertext()
	if len(data) > c.ChunkSize {
		return nil, fmt.Errorf("%w: got %d bytes, limit %d", ErrChunkTooLarge, len(data), c.ChunkSize)
	}

	nonce := c.nonceFor(sequence)
	if len(chunk.Nonce) != 0 && !bytes.Equal(chunk.Nonce, nonce) {
		return nil, ErrNonceMismatch
	}

	sealed := make([]byte, 0, TagSize+len(data))
	sealed = append(sealed, chunk.Tag...)
	sealed = append(sealed, data...)
	plaintext, err := c.aead.Open(nil, nonce, sealed, c.associatedData(sequence))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthentication, err)
	}
	return plaintext, nil
}

// Encrypt splits plaintext into sequentially numbered chunks and seals the
// final chunk with the end marker.
func (c *Cipher) Encrypt(plaintext []byte) (*Message, error) {
	plaintexts := make([][]byte, 0, (len(plaintext)+c.ChunkSize-1)/c.ChunkSize)
	for len(plaintext) > c.ChunkSize {
		plaintexts = append(plaintexts, append([]byte(nil), plaintext[:c.ChunkSize]...))
		plaintext = plaintext[c.ChunkSize:]
	}
	plaintexts = append(plaintexts, append([]byte(nil), plaintext...))

	chunks := make([]Chunk, 0, len(plaintexts))
	for i, part := range plaintexts {
		final := i == len(plaintexts)-1
		chunk, err := c.SealChunk(uint64(i), final, part)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}

	return &Message{
		Version:   Version2,
		Cipher:    c.Cipher,
		Mode:      c.Mode,
		ChunkSize: c.ChunkSize,
		Salt:      append([]byte(nil), c.Salt...),
		Chunks:    chunks,
	}, nil
}

// EncryptMessage is an alias for Encrypt.
func (c *Cipher) EncryptMessage(plaintext []byte) (*Message, error) {
	return c.Encrypt(plaintext)
}

// EncryptChunks seals each plaintext as a chunk. All chunks except the last
// use consecutive sequence numbers; the last uses the final marker.
func (c *Cipher) EncryptChunks(plaintexts [][]byte) ([]Chunk, error) {
	if len(plaintexts) == 0 {
		return nil, ErrEmptyMessage
	}
	chunks := make([]Chunk, 0, len(plaintexts))
	for i, plaintext := range plaintexts {
		chunk, err := c.SealChunk(uint64(i), i == len(plaintexts)-1, plaintext)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// EncryptWithSequences seals chunks with caller-selected sequence numbers.
// The final plaintext always uses the final marker. Supplying the same
// sequence number for two chunks reuses the AEAD nonce.
func (c *Cipher) EncryptWithSequences(sequences []uint64, plaintexts [][]byte) ([]Chunk, error) {
	if len(sequences) != len(plaintexts) {
		return nil, fmt.Errorf("netsec: %d sequences for %d chunks", len(sequences), len(plaintexts))
	}
	if len(plaintexts) == 0 {
		return nil, ErrEmptyMessage
	}
	chunks := make([]Chunk, 0, len(plaintexts))
	for i, plaintext := range plaintexts {
		final := i == len(plaintexts)-1
		sequence := sequences[i]
		if final {
			sequence = FinalSequence
		}
		chunk, err := c.SealChunk(sequence, final, plaintext)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// Decrypt validates the chunk sequence and decrypts all chunks.
func (c *Cipher) Decrypt(chunks []Chunk) ([]byte, error) {
	if err := ValidateSequences(chunks, c.Policy); err != nil {
		return nil, err
	}
	return c.DecryptUnchecked(chunks)
}

// DecryptWithPolicy decrypts chunks using an explicit sequence policy.
func (c *Cipher) DecryptWithPolicy(chunks []Chunk, policy SequencePolicy) ([]byte, error) {
	if err := ValidateSequences(chunks, policy); err != nil {
		return nil, err
	}
	return c.DecryptUnchecked(chunks)
}

// DecryptMessage is an alias for Decrypt.
func (c *Cipher) DecryptMessage(chunks []Chunk) ([]byte, error) {
	return c.Decrypt(chunks)
}

// Validate checks the chunks using the cipher's configured policy.
func (c *Cipher) Validate(chunks []Chunk) error {
	return ValidateSequences(chunks, c.Policy)
}

// DecryptUnchecked decrypts each chunk independently without validating the
// overall sequence. It is intended for attack analysis and must not be used
// for ordinary message decryption.
func (c *Cipher) DecryptUnchecked(chunks []Chunk) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, ErrEmptyMessage
	}
	var out []byte
	for _, chunk := range chunks {
		plaintext, err := c.OpenChunk(chunk)
		if err != nil {
			return nil, err
		}
		out = append(out, plaintext...)
	}
	return out, nil
}

// ValidateSequences checks chunk ordering using the supplied policy.
func ValidateSequences(chunks []Chunk, policy SequencePolicy) error {
	if len(chunks) == 0 {
		return ErrEmptyMessage
	}

	finalCount := 0
	for _, chunk := range chunks {
		sequence, err := chunk.sequence()
		if err != nil {
			return err
		}
		if chunk.Final && sequence != FinalSequence {
			return ErrInvalidFinal
		}
		if chunk.Final || sequence == FinalSequence {
			finalCount++
		}
	}
	if finalCount > 1 {
		return ErrDuplicateFinal
	}

	var previous uint64
	haveNormal := false
	haveFinal := false

	for i, chunk := range chunks {
		sequence, err := chunk.sequence()
		if err != nil {
			return err
		}
		final := chunk.Final || sequence == FinalSequence
		if final {
			if haveFinal {
				return ErrDuplicateFinal
			}
			if i != len(chunks)-1 {
				return ErrFinalNotLast
			}
			haveFinal = true
			continue
		}

		if !haveNormal {
			if sequence != 0 {
				return ErrSequenceJump
			}
			previous = sequence
			haveNormal = true
			continue
		}

		switch {
		case sequence == previous+1:
			previous = sequence
		case sequence == previous && policy == RequireSequential:
			return fmt.Errorf("%w: %d after %d", ErrSequenceReuse, sequence, previous)
		case policy == AllowSequenceReuse && sequence == previous:
			// Reported vulnerability: reusing a non-final sequence number
			// reuses the nonce and associated data.
		case sequence > previous+1:
			return fmt.Errorf("%w: %d after %d", ErrSequenceJump, sequence, previous)
		default:
			return fmt.Errorf("%w: %d after %d", ErrSequenceOrder, sequence, previous)
		}
	}

	if policy == RequireSequential && !haveFinal {
		return ErrMissingFinal
	}
	return nil
}

// SwapSequenceNumbers exchanges the sequence fields of two chunks. The
// stored nonce is cleared so it is re-derived from the new sequence.
func SwapSequenceNumbers(chunks []Chunk, i, j int) error {
	if i < 0 || j < 0 || i >= len(chunks) || j >= len(chunks) {
		return fmt.Errorf("netsec: chunk index out of range")
	}
	if i == j {
		return nil
	}
	chunks[i].Sequence, chunks[j].Sequence = chunks[j].Sequence, chunks[i].Sequence
	chunks[i].Index, chunks[j].Index = chunks[j].Index, chunks[i].Index
	chunks[i].Final, chunks[j].Final = chunks[j].Final, chunks[i].Final
	chunks[i].Nonce = nil
	chunks[j].Nonce = nil
	return nil
}

// SwapChunkSequences is an alias for SwapSequenceNumbers.
func SwapChunkSequences(chunks []Chunk, i, j int) error {
	return SwapSequenceNumbers(chunks, i, j)
}

// SwapChunkSequenceNumbers is an alias for SwapSequenceNumbers.
func SwapChunkSequenceNumbers(chunks []Chunk, i, j int) error {
	return SwapSequenceNumbers(chunks, i, j)
}

// SwapSequences is an alias for SwapSequenceNumbers.
func SwapSequences(chunks []Chunk, i, j int) error {
	return SwapSequenceNumbers(chunks, i, j)
}

// SwapChunks exchanges two complete chunks, including their sequence
// numbers, nonces, tags, and ciphertexts.
func SwapChunks(chunks []Chunk, i, j int) error {
	if i < 0 || j < 0 || i >= len(chunks) || j >= len(chunks) {
		return fmt.Errorf("netsec: chunk index out of range")
	}
	chunks[i], chunks[j] = chunks[j], chunks[i]
	return nil
}

// SwapPayloads exchanges the tag and ciphertext of two chunks while leaving
// their sequence numbers and nonces in place.
func SwapPayloads(chunks []Chunk, i, j int) error {
	if i < 0 || j < 0 || i >= len(chunks) || j >= len(chunks) {
		return fmt.Errorf("netsec: chunk index out of range")
	}
	chunks[i].Tag, chunks[j].Tag = chunks[j].Tag, chunks[i].Tag
	chunks[i].Ciphertext, chunks[j].Ciphertext = chunks[j].Ciphertext, chunks[i].Ciphertext
	chunks[i].Data, chunks[j].Data = chunks[j].Data, chunks[i].Data
	return nil
}

// SetSequence changes a chunk's sequence number and clears its stored nonce.
func SetSequence(chunk *Chunk, sequence uint64) {
	chunk.Sequence = sequence
	chunk.Index = sequence
	chunk.Final = sequence == FinalSequence
	chunk.Nonce = nil
}

// ReuseSequence copies the source chunk into the target position, including
// its sequence number, nonce, tag, and ciphertext. With the default policy,
// the copied later chunk authenticates and decrypts to the source plaintext.
func ReuseSequence(chunks []Chunk, source, target int) error {
	if source < 0 || target < 0 || source >= len(chunks) || target >= len(chunks) {
		return fmt.Errorf("netsec: chunk index out of range")
	}
	chunks[target] = cloneChunk(chunks[source])
	return nil
}

// ReuseNonce is an alias for ReuseSequence.
func ReuseNonce(chunks []Chunk, source, target int) error {
	return ReuseSequence(chunks, source, target)
}

func cloneChunk(chunk Chunk) Chunk {
	chunk.Nonce = append([]byte(nil), chunk.Nonce...)
	chunk.Tag = append([]byte(nil), chunk.Tag...)
	chunk.Ciphertext = append([]byte(nil), chunk.Ciphertext...)
	chunk.Data = append([]byte(nil), chunk.Data...)
	return chunk
}
