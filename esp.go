// Package esp implements RFC 4303 ESP encapsulation and decapsulation with
// 64-bit extended sequence numbers (ESN), AES-GCM, Encrypt-then-MAC
// HMAC-SHA256, and an authenticated anti-replay window.
//
// Authentication is always completed before the replay window is advanced or
// before the encrypted ESP trailer is inspected. Authentication failures and
// malformed padding both produce ErrAuthentication, so callers cannot build a
// padding oracle.
package esp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math"
	"sync"
)

const (
	// AESGCMNonceSize is the nonce length used by the AES-GCM packet format.
	AESGCMNonceSize = 12
	// AESGCMTagSize is the GCM tag length used by the AES-GCM packet format.
	AESGCMTagSize = 16
	// CBCIVSize is the cipher-block size and IV size for AES-CBC.
	CBCIVSize = aes.BlockSize
	// HMACSHA256Size is the full HMAC-SHA256 ICV length.
	HMACSHA256Size = sha256.Size

	espHeaderSize = 8
)

// Mode identifies the ESP confidentiality and authentication construction.
type Mode uint8

const (
	// AESGCM uses AES-GCM as an AEAD.
	AESGCM Mode = iota
	// EncryptThenMAC uses AES-CBC followed by HMAC-SHA256.
	EncryptThenMAC
)

const (
	ModeAESGCM         = AESGCM
	ModeEncryptThenMAC = EncryptThenMAC
)

const defaultReplayWindow = window64

// Config configures an ESP session. A Session represents one security
// association and one SPI. For production traffic, use a separate session for
// each SA/direction.
type Config struct {
	Mode       Mode
	AESKey     []byte
	MACKey     []byte // required for EncryptThenMAC
	WindowSize int

	// SPI is placed in the four-octet ESP header and authenticated.
	SPI uint32

	// InitialReplayHighest optionally seeds the receive window. Zero means
	// uninitialized. It is useful when an SA starts at a known ESN.
	InitialReplayHighest uint64

	// InitialSendSequence is the first value used by SealNext. Zero defaults
	// to one.
	InitialSendSequence uint64

	// Rand supplies IV/nonce randomness. It defaults to crypto/rand.Reader.
	Rand io.Reader
}

// Session performs ESP sealing and opening for one SA. Sealing with different
// explicit sequence numbers is stateless and may be concurrent. Open performs
// a two-phase replay check and updates the window only after both
// authentication and trailer validation succeed.
type Session struct {
	mode   Mode
	spi    uint32
	rand   io.Reader
	window *ReplayWindow

	aead   cipher.AEAD
	block  cipher.Block
	macKey []byte

	replayMu sync.Mutex
	sendMu   sync.Mutex
	nextSeq  uint64
}

// OpenResult is the authenticated output of Decapsulate.
type OpenResult struct {
	Payload        []byte
	NextHeader     uint8
	SequenceNumber uint64
}

// New creates an ESP session.
func New(cfg Config) (*Session, error) {
	windowSize := cfg.WindowSize
	if windowSize == 0 {
		windowSize = defaultReplayWindow
	}
	window, err := NewReplayWindow(windowSize)
	if err != nil {
		return nil, err
	}

	s := &Session{
		mode:    cfg.Mode,
		spi:     cfg.SPI,
		rand:    cfg.Rand,
		window:  window,
		nextSeq: cfg.InitialSendSequence,
	}
	if s.rand == nil {
		s.rand = rand.Reader
	}
	if s.nextSeq == 0 {
		s.nextSeq = 1
	}
	if err := validateSequence(s.nextSeq); err != nil {
		return nil, err
	}
	if cfg.InitialReplayHighest != 0 {
		if err := window.Initialize(cfg.InitialReplayHighest); err != nil {
			return nil, err
		}
	}

	switch cfg.Mode {
	case AESGCM:
		block, err := aes.NewCipher(cfg.AESKey)
		if err != nil {
			return nil, ErrInvalidKey
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrInvalidKey
		}
		s.aead = aead
		s.block = block
	case EncryptThenMAC:
		block, err := aes.NewCipher(cfg.AESKey)
		if err != nil {
			return nil, ErrInvalidKey
		}
		if len(cfg.MACKey) == 0 {
			return nil, ErrInvalidKey
		}
		s.block = block
		s.macKey = append([]byte(nil), cfg.MACKey...)
	default:
		return nil, ErrInvalidConfig
	}

	return s, nil
}

// NewAESGCM creates an AES-GCM ESP session. An optional window size of 64,
// 256, or 1024 may be supplied; 64 is the default.
func NewAESGCM(key []byte, windowSize ...int) (*Session, error) {
	if len(windowSize) > 1 {
		return nil, ErrInvalidConfig
	}
	cfg := Config{Mode: AESGCM, AESKey: key}
	if len(windowSize) == 1 {
		cfg.WindowSize = windowSize[0]
	}
	return New(cfg)
}

// NewEncryptThenMAC creates an AES-CBC plus HMAC-SHA256 ESP session. An
// optional window size of 64, 256, or 1024 may be supplied; 64 is the default.
func NewEncryptThenMAC(aesKey, macKey []byte, windowSize ...int) (*Session, error) {
	if len(windowSize) > 1 {
		return nil, ErrInvalidConfig
	}
	cfg := Config{Mode: EncryptThenMAC, AESKey: aesKey, MACKey: macKey}
	if len(windowSize) == 1 {
		cfg.WindowSize = windowSize[0]
	}
	return New(cfg)
}

// Seal constructs an ESP packet for sequence number seq. Only the low 32 bits
// of seq are transmitted, while the full ESN is included in the authenticated
// data. Sequence number zero and 2^64-1 are rejected; the latter requires a
// rekey.
func (s *Session) Seal(seq uint64, payload []byte, nextHeader uint8) ([]byte, error) {
	if err := validateSequence(seq); err != nil {
		return nil, err
	}

	aad := authData(s.spi, seq)
	switch s.mode {
	case AESGCM:
		nonce, err := readRandom(s.rand, AESGCMNonceSize)
		if err != nil {
			return nil, err
		}
		plaintext := buildESPPlaintext(payload, nextHeader, 4)
		packet := make([]byte, espHeaderSize+AESGCMNonceSize)
		binary.BigEndian.PutUint32(packet[0:4], s.spi)
		binary.BigEndian.PutUint32(packet[4:8], uint32(seq))
		copy(packet[espHeaderSize:], nonce)
		packet = s.aead.Seal(packet, nonce, plaintext, aad)
		wipe(plaintext)
		return packet, nil

	case EncryptThenMAC:
		iv, err := readRandom(s.rand, CBCIVSize)
		if err != nil {
			return nil, err
		}
		plaintext := buildESPPlaintext(payload, nextHeader, aes.BlockSize)
		packet := make([]byte, espHeaderSize+CBCIVSize+len(plaintext)+HMACSHA256Size)
		binary.BigEndian.PutUint32(packet[0:4], s.spi)
		binary.BigEndian.PutUint32(packet[4:8], uint32(seq))
		copy(packet[espHeaderSize:espHeaderSize+CBCIVSize], iv)

		ciphertext := packet[espHeaderSize+CBCIVSize : len(packet)-HMACSHA256Size]
		cipher.NewCBCEncrypter(s.block, iv).CryptBlocks(ciphertext, plaintext)
		wipe(plaintext)

		icv := calculateICV(s.macKey, aad, iv, ciphertext)
		copy(packet[len(packet)-HMACSHA256Size:], icv)
		return packet, nil
	default:
		return nil, ErrInvalidConfig
	}
}

// SealNext seals the next sequence number maintained by the session.
func (s *Session) SealNext(payload []byte, nextHeader uint8) ([]byte, uint64, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	seq := s.nextSeq
	if err := validateSequence(seq); err != nil {
		return nil, 0, err
	}
	packet, err := s.Seal(seq, payload, nextHeader)
	if err != nil {
		return nil, 0, err
	}
	if seq == math.MaxUint64-1 {
		s.nextSeq = math.MaxUint64
	} else {
		s.nextSeq = seq + 1
	}
	return packet, seq, nil
}

// Open authenticates and decrypts an ESP packet.
//
// The returned order is (payload, next header, reconstructed 64-bit ESN).
// The anti-replay window is advanced only after authentication and trailer
// validation both succeed. Authentication and padding failures both return
// ErrAuthentication.
func (s *Session) Open(packet []byte) ([]byte, uint8, uint64, error) {
	if len(packet) < espHeaderSize {
		return nil, 0, 0, ErrMalformedPacket
	}
	spi := binary.BigEndian.Uint32(packet[0:4])
	if spi != s.spi {
		return nil, 0, 0, ErrSPIMismatch
	}
	seqLow := binary.BigEndian.Uint32(packet[4:8])

	s.replayMu.Lock()
	candidate, err := s.window.Check(seqLow)
	s.replayMu.Unlock()
	if err != nil {
		return nil, 0, 0, err
	}

	aad := authData(s.spi, candidate)
	var (
		plaintext  []byte
		nextHeader uint8
	)

	switch s.mode {
	case AESGCM:
		if len(packet) < espHeaderSize+AESGCMNonceSize+AESGCMTagSize {
			return nil, 0, 0, ErrMalformedPacket
		}
		nonce := packet[espHeaderSize : espHeaderSize+AESGCMNonceSize]
		plaintext, err = s.aead.Open(nil, nonce, packet[espHeaderSize+AESGCMNonceSize:], aad)
		if err != nil {
			return nil, 0, 0, ErrAuthentication
		}

	case EncryptThenMAC:
		if len(packet) < espHeaderSize+CBCIVSize+HMACSHA256Size {
			return nil, 0, 0, ErrMalformedPacket
		}
		iv := packet[espHeaderSize : espHeaderSize+CBCIVSize]
		ciphertext := packet[espHeaderSize+CBCIVSize : len(packet)-HMACSHA256Size]
		icv := packet[len(packet)-HMACSHA256Size:]
		expected := calculateICV(s.macKey, aad, iv, ciphertext)
		if !hmac.Equal(expected, icv) {
			return nil, 0, 0, ErrAuthentication
		}
		if len(ciphertext)%aes.BlockSize != 0 {
			return nil, 0, 0, ErrAuthentication
		}

		plaintext = make([]byte, len(ciphertext))
		cipher.NewCBCDecrypter(s.block, iv).CryptBlocks(plaintext, ciphertext)

	default:
		return nil, 0, 0, ErrInvalidConfig
	}

	body, nh, ok := parseESPPlaintext(plaintext)
	if !ok {
		wipe(plaintext)
		return nil, 0, 0, ErrAuthentication
	}
	nextHeader = nh

	s.replayMu.Lock()
	err = s.window.Commit(candidate)
	s.replayMu.Unlock()
	if err != nil {
		wipe(plaintext)
		return nil, 0, 0, err
	}

	return body, nextHeader, candidate, nil
}

// Encapsulate is an alias for Seal.
func (s *Session) Encapsulate(seq uint64, payload []byte, nextHeader uint8) ([]byte, error) {
	return s.Seal(seq, payload, nextHeader)
}

// Decapsulate authenticates and decrypts a packet and returns a structured
// result.
func (s *Session) Decapsulate(packet []byte) (OpenResult, error) {
	payload, nextHeader, seq, err := s.Open(packet)
	if err != nil {
		return OpenResult{}, err
	}
	return OpenResult{
		Payload:        payload,
		NextHeader:     nextHeader,
		SequenceNumber: seq,
	}, nil
}

// ReplayWindowSize returns the receive window size in bits.
func (s *Session) ReplayWindowSize() int {
	return s.window.Size()
}

// HighestReceived returns the highest authenticated ESN recorded by the
// receive window.
func (s *Session) HighestReceived() (uint64, bool) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	return s.window.Highest()
}

// SetReplayHighest initializes the receive window at a known ESN. This should
// normally be used only at SA setup.
func (s *Session) SetReplayHighest(seq uint64) error {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	return s.window.Initialize(seq)
}

// SetNextSequence sets the sequence number used by the next SealNext call.
func (s *Session) SetNextSequence(seq uint64) error {
	if err := validateSequence(seq); err != nil {
		return err
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.nextSeq = seq
	return nil
}

// NextSequence returns the sequence number that SealNext will use.
func (s *Session) NextSequence() uint64 {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.nextSeq
}

func authData(spi uint32, seq uint64) []byte {
	// The high-order 32 bits of the ESN are not transmitted, but are
	// authenticated by prepending them to the wire ESP header (SPI and the
	// low-order 32 sequence bits).
	out := make([]byte, 12)
	binary.BigEndian.PutUint32(out[0:4], uint32(seq>>32))
	binary.BigEndian.PutUint32(out[4:8], spi)
	binary.BigEndian.PutUint32(out[8:12], uint32(seq))
	return out
}

func calculateICV(macKey, aad, iv, ciphertext []byte) []byte {
	mac := hmac.New(sha256.New, macKey)
	_, _ = mac.Write(aad)
	_, _ = mac.Write(iv)
	_, _ = mac.Write(ciphertext)
	return mac.Sum(nil)
}

func readRandom(r io.Reader, size int) ([]byte, error) {
	out := make([]byte, size)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}
