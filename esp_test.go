package esp

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"testing"
)

var (
	testAESKey = []byte("0123456789abcdef0123456789abcdef")
	testMACKey = []byte("fedcba9876543210fedcba9876543210")
)

type deterministicReader struct {
	counter uint64
}

func (r *deterministicReader) Read(p []byte) (int, error) {
	for i := range p {
		r.counter++
		p[i] = byte(r.counter*29 + 17)
	}
	return len(p), nil
}

func TestReconstructESN(t *testing.T) {
	tests := []struct {
		name    string
		highest uint64
		low     uint32
		want    uint64
	}{
		{name: "same epoch ahead", highest: 100, low: 110, want: 110},
		{name: "same epoch reordered", highest: 100, low: 90, want: 90},
		{name: "next epoch after low wrap", highest: 0x00000000ffffffff, low: 0, want: 0x0000000100000000},
		{name: "previous epoch low word", highest: 0x0000000100000005, low: 0xfffffff6, want: 0x00000000fffffff6},
		{name: "current epoch just ahead", highest: 0x0000000100000005, low: 10, want: 0x000000010000000a},
		{name: "high and low wrap", highest: 0x00000001ffffffff, low: 0, want: 0x0000000200000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReconstructESN(tt.highest, tt.low)
			if err != nil {
				t.Fatalf("ReconstructESN() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ReconstructESN() = %#x, want %#x", got, tt.want)
			}
		})
	}
}

func TestReplayWindowWrap(t *testing.T) {
	window, err := NewReplayWindow(1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := window.Initialize(0x00000000ffffffff); err != nil {
		t.Fatal(err)
	}

	seq, err := window.Check(0)
	if err != nil {
		t.Fatalf("first post-wrap Check() error = %v", err)
	}
	if seq != 0x0000000100000000 {
		t.Fatalf("post-wrap candidate = %#x", seq)
	}
	if err := window.Commit(seq); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	if _, err := window.Check(0); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed post-wrap sequence error = %v", err)
	}
	if _, err := window.Check(0xffffffff); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed pre-wrap sequence error = %v", err)
	}

	old, err := window.Check(0xfffffffe)
	if err != nil {
		t.Fatalf("reordered pre-wrap Check() error = %v", err)
	}
	if old != 0x00000000fffffffe {
		t.Fatalf("reordered candidate = %#x", old)
	}
	if err := window.Commit(old); err != nil {
		t.Fatalf("reordered Commit() error = %v", err)
	}

	next, err := window.Check(1)
	if err != nil {
		t.Fatalf("next sequence Check() error = %v", err)
	}
	if next != 0x0000000100000001 {
		t.Fatalf("next candidate = %#x", next)
	}
	if err := window.Commit(next); err != nil {
		t.Fatalf("next Commit() error = %v", err)
	}
}

func TestReplayWindowSizesAndEdges(t *testing.T) {
	for _, size := range []int{64, 256, 1024} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			window, err := NewReplayWindow(size)
			if err != nil {
				t.Fatal(err)
			}
			const highest = uint64(2000)
			if err := window.Initialize(highest); err != nil {
				t.Fatal(err)
			}

			for delta := uint64(1); delta < uint64(size); delta++ {
				seq := highest - delta
				got, err := window.Check(uint32(seq))
				if err != nil {
					t.Fatalf("Check(%d) error = %v", seq, err)
				}
				if got != seq {
					t.Fatalf("Check(%d) = %d", seq, got)
				}
				if err := window.Commit(seq); err != nil {
					t.Fatalf("Commit(%d) error = %v", seq, err)
				}
			}

			if _, err := window.Check(uint32(highest - uint64(size))); !errors.Is(err, ErrReplay) {
				t.Fatalf("left-outside candidate error = %v", err)
			}
			if _, err := window.Check(uint32(highest - uint64(size-1))); !errors.Is(err, ErrReplay) {
				t.Fatalf("left-edge replay error = %v", err)
			}

			advance := highest + uint64(size)
			if _, err := window.Check(uint32(advance)); err != nil {
				t.Fatalf("future Check() error = %v", err)
			}
			if err := window.Commit(advance); err != nil {
				t.Fatalf("future Commit() error = %v", err)
			}
			if _, err := window.Check(uint32(highest)); !errors.Is(err, ErrReplay) {
				t.Fatalf("sequence dropped by advance error = %v", err)
			}

			if _, ok := window.Highest(); !ok {
				t.Fatal("window lost initialized state")
			}
		})
	}
}

func TestReplayWindowCommitIsTwoPhase(t *testing.T) {
	window, err := NewReplayWindow(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := window.Initialize(10); err != nil {
		t.Fatal(err)
	}

	future, err := window.Check(1000)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if future != 1000 {
		t.Fatalf("candidate = %d", future)
	}
	highest, _ := window.Highest()
	if highest != 10 {
		t.Fatalf("Check advanced highest to %d", highest)
	}

	if err := window.Commit(1000); err != nil {
		t.Fatal(err)
	}
	highest, _ = window.Highest()
	if highest != 1000 {
		t.Fatalf("Commit highest = %d", highest)
	}
}

func TestESNRoundTripAcross32BitWrap(t *testing.T) {
	for _, mode := range []Mode{AESGCM, EncryptThenMAC} {
		t.Run(modeName(mode), func(t *testing.T) {
			sender, receiver := newTestPair(t, mode, 1024)
			seqs := []uint64{
				0x00000000fffffffd,
				0x00000000fffffffe,
				0x00000000ffffffff,
				0x0000000100000000,
				0x0000000100000002,
				0x0000000100000001,
			}

			for _, seq := range seqs {
				payload := []byte{byte(seq), byte(seq >> 8), byte(seq >> 16)}
				packet, err := sender.Seal(seq, payload, 59)
				if err != nil {
					t.Fatalf("Seal(%#x) error = %v", seq, err)
				}
				got, nextHeader, gotSeq, err := receiver.Open(packet)
				if err != nil {
					t.Fatalf("Open(%#x) error = %v", seq, err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("Open(%#x) payload = %x, want %x", seq, got, payload)
				}
				if nextHeader != 59 {
					t.Fatalf("Open(%#x) next header = %d", seq, nextHeader)
				}
				if gotSeq != seq {
					t.Fatalf("Open(%#x) sequence = %#x", seq, gotSeq)
				}
			}

			highest, ok := receiver.HighestReceived()
			if !ok || highest != 0x0000000100000002 {
				t.Fatalf("highest = %#x, initialized = %v", highest, ok)
			}

			// The first packet is still inside the 1024-bit window and must be
			// rejected as a replay after the window crossed 2^32.
			replayed, err := sender.Seal(seqs[0], []byte("replay"), 59)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := receiver.Open(replayed); !errors.Is(err, ErrReplay) {
				t.Fatalf("post-wrap replay error = %v", err)
			}
		})
	}
}

func TestPayloadLengthRoundTrip(t *testing.T) {
	for _, mode := range []Mode{AESGCM, EncryptThenMAC} {
		t.Run(modeName(mode), func(t *testing.T) {
			sender, receiver := newTestPair(t, mode, 256)
			for length := 0; length <= 48; length++ {
				payload := make([]byte, length)
				for i := range payload {
					payload[i] = byte(i + length)
				}
				seq := uint64(length + 1)
				packet, err := sender.Seal(seq, payload, byte(length))
				if err != nil {
					t.Fatalf("length %d Seal() error = %v", length, err)
				}
				got, nextHeader, gotSeq, err := receiver.Open(packet)
				if err != nil {
					t.Fatalf("length %d Open() error = %v", length, err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("length %d payload mismatch", length)
				}
				if nextHeader != byte(length) || gotSeq != seq {
					t.Fatalf("length %d trailer mismatch: header=%d seq=%d", length, nextHeader, gotSeq)
				}
			}
		})
	}
}

func TestUnauthenticatedHighSequenceCannotAdvanceWindow(t *testing.T) {
	for _, mode := range []Mode{AESGCM, EncryptThenMAC} {
		t.Run(modeName(mode), func(t *testing.T) {
			sender, receiver := newTestPair(t, mode, 64)
			first, err := sender.Seal(1, []byte("first"), 59)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := receiver.Open(first); err != nil {
				t.Fatalf("Open(first) error = %v", err)
			}

			second, err := sender.Seal(2, []byte("second"), 59)
			if err != nil {
				t.Fatal(err)
			}
			forged := append([]byte(nil), second...)
			binary.BigEndian.PutUint32(forged[4:8], 0xffffffff)

			if _, _, _, err := receiver.Open(forged); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("forged high sequence error = %v", err)
			}
			highest, _ := receiver.HighestReceived()
			if highest != 1 {
				t.Fatalf("forged packet advanced highest to %d", highest)
			}

			if _, _, gotSeq, err := receiver.Open(second); err != nil {
				t.Fatalf("valid second packet error = %v", err)
			} else if gotSeq != 2 {
				t.Fatalf("valid second sequence = %d", gotSeq)
			}
		})
	}
}

func TestReplayAndTamperAreRejected(t *testing.T) {
	for _, mode := range []Mode{AESGCM, EncryptThenMAC} {
		t.Run(modeName(mode), func(t *testing.T) {
			sender, receiver := newTestPair(t, mode, 64)
			packet, err := sender.Seal(1, []byte("replay-me"), 59)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := receiver.Open(packet); err != nil {
				t.Fatalf("first Open() error = %v", err)
			}
			if _, _, _, err := receiver.Open(packet); !errors.Is(err, ErrReplay) {
				t.Fatalf("replayed packet error = %v", err)
			}

			second, err := sender.Seal(2, []byte("tamper-me"), 59)
			if err != nil {
				t.Fatal(err)
			}
			tampered := append([]byte(nil), second...)
			tampered[len(tampered)-1] ^= 0x80
			highestBefore, _ := receiver.HighestReceived()
			if _, _, _, err := receiver.Open(tampered); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("tampered packet error = %v", err)
			}
			highestAfter, _ := receiver.HighestReceived()
			if highestAfter != highestBefore {
				t.Fatalf("tampered packet advanced highest from %d to %d", highestBefore, highestAfter)
			}
		})
	}
}

func TestAuthenticationAndPaddingFailuresAreIndistinguishable(t *testing.T) {
	for _, mode := range []Mode{AESGCM, EncryptThenMAC} {
		t.Run(modeName(mode), func(t *testing.T) {
			sender, receiver := newTestPair(t, mode, 64)
			first, err := sender.Seal(1, []byte("first"), 59)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := receiver.Open(first); err != nil {
				t.Fatalf("Open(first) error = %v", err)
			}

			valid, err := sender.Seal(2, []byte("valid"), 59)
			if err != nil {
				t.Fatal(err)
			}
			authTampered := append([]byte(nil), valid...)
			if mode == AESGCM {
				authTampered[espHeaderSize+AESGCMNonceSize] ^= 0x01
			} else {
				authTampered[espHeaderSize+CBCIVSize] ^= 0x01
			}
			if _, _, _, err := receiver.Open(authTampered); err != ErrAuthentication {
				t.Fatalf("authentication failure error = %v", err)
			}

			badPadding := customTrailerPacket(t, mode, 0x12345678, 2, badPaddingBody(mode))
			if _, _, _, err := receiver.Open(badPadding); err != ErrAuthentication {
				t.Fatalf("bad padding error = %v", err)
			}

			tooLong := customTrailerPacket(t, mode, 0x12345678, 3, tooLongPadLengthBody(mode))
			if _, _, _, err := receiver.Open(tooLong); err != ErrAuthentication {
				t.Fatalf("overlong pad length error = %v", err)
			}

			highest, _ := receiver.HighestReceived()
			if highest != 1 {
				t.Fatalf("failed authenticated packets advanced highest to %d", highest)
			}
			if _, _, _, err := receiver.Open(valid); err != nil {
				t.Fatalf("valid packet after padding failures error = %v", err)
			}
		})
	}
}

func TestSequenceZeroAndExhaustion(t *testing.T) {
	sender, err := NewAESGCM(testAESKey, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Seal(0, []byte("zero"), 59); !errors.Is(err, ErrSequenceZero) {
		t.Fatalf("Seal(0) error = %v", err)
	}
	if _, err := sender.Seal(math.MaxUint64, []byte("end"), 59); !errors.Is(err, ErrRekeyRequired) {
		t.Fatalf("Seal(max) error = %v", err)
	}
	if _, err := sender.Seal(math.MaxUint64-1, []byte("last usable"), 59); err != nil {
		t.Fatalf("Seal(max-1) error = %v", err)
	}

	window, err := NewReplayWindow(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := window.Initialize(0); !errors.Is(err, ErrSequenceZero) {
		t.Fatalf("Initialize(0) error = %v", err)
	}
	if err := window.Initialize(math.MaxUint64); !errors.Is(err, ErrRekeyRequired) {
		t.Fatalf("Initialize(max) error = %v", err)
	}
	if err := window.Initialize(math.MaxUint64 - 1); err != nil {
		t.Fatal(err)
	}
	if _, err := window.Check(0xffffffff); !errors.Is(err, ErrRekeyRequired) {
		t.Fatalf("Check(max) error = %v", err)
	}
}

func TestSealNextStopsAtExhaustion(t *testing.T) {
	sender, err := New(Config{
		Mode:                AESGCM,
		AESKey:              testAESKey,
		WindowSize:          64,
		InitialSendSequence: math.MaxUint64 - 1,
		Rand:                &deterministicReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, seq, err := sender.SealNext([]byte("last"), 59); err != nil {
		t.Fatalf("SealNext() error = %v", err)
	} else if seq != math.MaxUint64-1 {
		t.Fatalf("SealNext() sequence = %d", seq)
	}
	if _, _, err := sender.SealNext([]byte("overflow"), 59); !errors.Is(err, ErrRekeyRequired) {
		t.Fatalf("SealNext() after exhaustion error = %v", err)
	}
}

func TestInvalidConfigurationAndWindowSize(t *testing.T) {
	if _, err := NewReplayWindow(128); !errors.Is(err, ErrInvalidWindowSize) {
		t.Fatalf("NewReplayWindow(128) error = %v", err)
	}
	if _, err := NewAESGCM([]byte{1, 2, 3}, 64); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewAESGCM(short key) error = %v", err)
	}
	if _, err := New(Config{Mode: Mode(99)}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(invalid mode) error = %v", err)
	}
	if _, err := New(Config{Mode: EncryptThenMAC, AESKey: testAESKey}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("New(no MAC key) error = %v", err)
	}
}

func newTestPair(t *testing.T, mode Mode, windowSize int) (*Session, *Session) {
	t.Helper()
	randSource := &deterministicReader{}
	senderConfig := Config{
		Mode:       mode,
		AESKey:     testAESKey,
		MACKey:     testMACKey,
		WindowSize: windowSize,
		SPI:        0x12345678,
		Rand:       randSource,
	}
	receiverConfig := senderConfig
	receiverConfig.Rand = &deterministicReader{}

	sender, err := New(senderConfig)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := New(receiverConfig)
	if err != nil {
		t.Fatal(err)
	}
	return sender, receiver
}

func modeName(mode Mode) string {
	switch mode {
	case AESGCM:
		return "AES-GCM"
	case EncryptThenMAC:
		return "Encrypt-then-MAC"
	default:
		return "unknown"
	}
}

func customTrailerPacket(t *testing.T, mode Mode, spi uint32, seq uint64, body []byte) []byte {
	t.Helper()
	aad := authData(spi, seq)

	switch mode {
	case AESGCM:
		block, err := aes.NewCipher(testAESKey)
		if err != nil {
			t.Fatal(err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce := []byte("0123456789ab")
		ciphertext := aead.Seal(nil, nonce, body, aad)
		packet := make([]byte, espHeaderSize+AESGCMNonceSize+len(ciphertext))
		binary.BigEndian.PutUint32(packet[0:4], spi)
		binary.BigEndian.PutUint32(packet[4:8], uint32(seq))
		copy(packet[espHeaderSize:espHeaderSize+AESGCMNonceSize], nonce)
		copy(packet[espHeaderSize+AESGCMNonceSize:], ciphertext)
		return packet

	case EncryptThenMAC:
		if len(body)%aes.BlockSize != 0 {
			t.Fatalf("test body length %d is not a CBC block multiple", len(body))
		}
		block, err := aes.NewCipher(testAESKey)
		if err != nil {
			t.Fatal(err)
		}
		iv := []byte("0123456789abcdef")
		ciphertext := make([]byte, len(body))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, body)
		icv := calculateICV(testMACKey, aad, iv, ciphertext)
		packet := make([]byte, espHeaderSize+CBCIVSize+len(ciphertext)+HMACSHA256Size)
		binary.BigEndian.PutUint32(packet[0:4], spi)
		binary.BigEndian.PutUint32(packet[4:8], uint32(seq))
		copy(packet[espHeaderSize:espHeaderSize+CBCIVSize], iv)
		copy(packet[espHeaderSize+CBCIVSize:], ciphertext)
		copy(packet[len(packet)-HMACSHA256Size:], icv)
		return packet

	default:
		t.Fatalf("unknown mode %d", mode)
		return nil
	}
}

func badPaddingBody(mode Mode) []byte {
	if mode == AESGCM {
		// Three payload bytes, three padding bytes, Pad Length, Next Header.
		// The second padding byte is deliberately 9 instead of 2.
		return []byte{0xaa, 0xbb, 0xcc, 1, 9, 3, 3, 59}
	}
	// Eleven payload bytes plus a three-byte malformed padding area and the
	// two-byte trailer make one complete AES block.
	body := make([]byte, aes.BlockSize)
	body[0] = 0xaa
	body[11], body[12], body[13] = 1, 9, 3
	body[14], body[15] = 3, 59
	return body
}

func tooLongPadLengthBody(mode Mode) []byte {
	if mode == AESGCM {
		// Pad Length claims 200 bytes, but fewer than 200 bytes precede it.
		return []byte{0xaa, 0xbb, 200, 59}
	}
	// One AES block with no padding area but a Pad Length of 250.
	body := make([]byte, 15*aes.BlockSize)
	body[len(body)-2] = 250
	body[len(body)-1] = 59
	return body
}
