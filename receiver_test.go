package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func testKey(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}

func testReceiver(t *testing.T, connectionID []byte) *Receiver {
	t.Helper()
	receiver, err := NewReceiver(connectionID)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return receiver
}

func testInstall(t *testing.T, receiver *Receiver, epoch uint32, key []byte) {
	t.Helper()
	if err := receiver.Install(epoch, key); err != nil {
		t.Fatalf("Install(%d): %v", epoch, err)
	}
}

// independentSeal deliberately rebuilds the nonce and AAD from the wire
// specification instead of calling package helpers.
func independentSeal(
	t *testing.T,
	connectionID []byte,
	epoch uint32,
	sequence uint64,
	key, plaintext []byte,
) []byte {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}

	nonce := make([]byte, 12)
	binary.BigEndian.PutUint32(nonce[0:4], epoch)
	binary.BigEndian.PutUint64(nonce[4:12], sequence)

	aad := make([]byte, 4+len(connectionID)+4+8)
	binary.BigEndian.PutUint32(aad[0:4], uint32(len(connectionID)))
	copy(aad[4:], connectionID)
	offset := 4 + len(connectionID)
	binary.BigEndian.PutUint32(aad[offset:offset+4], epoch)
	binary.BigEndian.PutUint64(aad[offset+4:], sequence)

	return aead.Seal(nil, nonce, plaintext, aad)
}

func testRecord(
	t *testing.T,
	connectionID []byte,
	epoch uint32,
	sequence uint64,
	key, plaintext []byte,
) Record {
	t.Helper()
	return Record{
		ConnectionID: connectionID,
		Epoch:        epoch,
		Sequence:     sequence,
		Ciphertext:   independentSeal(t, connectionID, epoch, sequence, key, plaintext),
	}
}

func plaintextFor(sequence uint64) []byte {
	return []byte(fmt.Sprintf("payload-%d", sequence))
}

func TestSealWireFormatAndReceive(t *testing.T) {
	connectionID := []byte("connection-42")
	key := testKey(0x42)
	plaintext := []byte("authenticated payload")
	const epoch = uint32(9)
	const sequence = uint64(123456789)

	ciphertext, err := Seal(connectionID, epoch, sequence, key, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint32(nonce[0:4], epoch)
	binary.BigEndian.PutUint64(nonce[4:12], sequence)
	aad := make([]byte, 4+len(connectionID)+4+8)
	binary.BigEndian.PutUint32(aad[0:4], uint32(len(connectionID)))
	copy(aad[4:], connectionID)
	offset := 4 + len(connectionID)
	binary.BigEndian.PutUint32(aad[offset:offset+4], epoch)
	binary.BigEndian.PutUint64(aad[offset+4:], sequence)

	opened, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		t.Fatalf("independent Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened plaintext = %q, want %q", opened, plaintext)
	}

	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, epoch, key)
	record := Record{
		ConnectionID: connectionID,
		Epoch:        epoch,
		Sequence:     sequence,
		Ciphertext:   ciphertext,
	}
	var delivered []byte
	if err := receiver.Receive(record, func(value []byte) error {
		delivered = append([]byte(nil), value...)
		return nil
	}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !bytes.Equal(delivered, plaintext) {
		t.Fatalf("delivered plaintext = %q, want %q", delivered, plaintext)
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x80
	record.Ciphertext = tampered
	if err := receiver.Receive(record, func([]byte) error {
		t.Fatal("tampered record was delivered")
		return nil
	}); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered Receive error = %v, want ErrAuthentication", err)
	}

	record.Ciphertext = ciphertext
	record.ConnectionID = []byte("connection-43")
	if err := receiver.Receive(record, func([]byte) error {
		t.Fatal("record from another connection was delivered")
		return nil
	}); !errors.Is(err, ErrConnectionIDMismatch) {
		t.Fatalf("wrong connection error = %v, want ErrConnectionIDMismatch", err)
	}
}

func TestAuthenticationBindsConnectionEpochAndSequence(t *testing.T) {
	connectionID := []byte("bound-connection")
	otherConnectionID := []byte("other-connection")
	key := testKey(0x19)
	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key)
	testInstall(t, receiver, 2, key)

	deliver := func([]byte) error {
		t.Fatal("record with mismatched AAD or nonce was delivered")
		return nil
	}

	tests := []struct {
		name   string
		record Record
	}{
		{
			name: "connection ID is authenticated",
			record: Record{
				ConnectionID: connectionID,
				Epoch:        1,
				Sequence:     5,
				Ciphertext:   independentSeal(t, otherConnectionID, 1, 5, key, plaintextFor(5)),
			},
		},
		{
			name: "epoch is authenticated",
			record: Record{
				ConnectionID: connectionID,
				Epoch:        1,
				Sequence:     5,
				Ciphertext:   independentSeal(t, connectionID, 2, 5, key, plaintextFor(5)),
			},
		},
		{
			name: "sequence is authenticated",
			record: Record{
				ConnectionID: connectionID,
				Epoch:        1,
				Sequence:     5,
				Ciphertext:   independentSeal(t, connectionID, 1, 6, key, plaintextFor(6)),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := receiver.Receive(test.record, deliver); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("Receive error = %v, want ErrAuthentication", err)
			}
		})
	}

	// None of the failed records may have claimed sequence 5.
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 5, key, plaintextFor(5)),
		func([]byte) error { return nil },
	); err != nil {
		t.Fatalf("valid record after authentication failures: %v", err)
	}
}

func TestReceiveOutOfOrderAndReplayWindow(t *testing.T) {
	connectionID := []byte("out-of-order")
	key := testKey(0x11)
	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key)

	var deliveries int
	deliver := func([]byte) error {
		deliveries++
		return nil
	}
	receive := func(sequence uint64) error {
		return receiver.Receive(testRecord(t, connectionID, 1, sequence, key, plaintextFor(sequence)), deliver)
	}

	for _, sequence := range []uint64{100, 2, 50, 99, 1025, 3, 1026, 4} {
		if err := receive(sequence); err != nil {
			t.Fatalf("Receive(%d): %v", sequence, err)
		}
	}
	if deliveries != 8 {
		t.Fatalf("deliveries = %d, want 8", deliveries)
	}

	if err := receive(50); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v, want ErrDuplicate", err)
	}
	if err := receive(2); !errors.Is(err, ErrTooOld) {
		t.Fatalf("too-old error = %v, want ErrTooOld", err)
	}
	if deliveries != 8 {
		t.Fatalf("rejected records reached callback; deliveries = %d", deliveries)
	}
}

func TestForgedHighSequenceDoesNotAdvanceWindow(t *testing.T) {
	connectionID := []byte("forged-window")
	key := testKey(0x22)
	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key)

	var deliveries int
	deliver := func([]byte) error {
		deliveries++
		return nil
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 10, key, plaintextFor(10)),
		deliver,
	); err != nil {
		t.Fatalf("Receive(10): %v", err)
	}

	forged := testRecord(t, connectionID, 1, 5000, key, plaintextFor(5000))
	forged.Ciphertext[len(forged.Ciphertext)-1] ^= 0x01
	if err := receiver.Receive(forged, deliver); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("forged high sequence error = %v, want ErrAuthentication", err)
	}
	if deliveries != 1 {
		t.Fatalf("forged record reached callback; deliveries = %d", deliveries)
	}

	// If the failed sequence had advanced the window to 5000, sequence 100
	// would now be outside the 1024-record window.
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 100, key, plaintextFor(100)),
		deliver,
	); err != nil {
		t.Fatalf("Receive(100) after forged high sequence: %v", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 5000, key, plaintextFor(5000)),
		deliver,
	); err != nil {
		t.Fatalf("Receive(5000) after forged copy: %v", err)
	}
	if deliveries != 3 {
		t.Fatalf("deliveries = %d, want 3", deliveries)
	}
}

func TestCallbackErrorStillConsumesSequence(t *testing.T) {
	connectionID := []byte("callback-error")
	key := testKey(0x33)
	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key)

	errDelivery := errors.New("application rejected payload")
	var calls int
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 7, key, plaintextFor(7)),
		func([]byte) error {
			calls++
			return errDelivery
		},
	); err != errDelivery {
		t.Fatalf("first Receive error = %v, want callback error", err)
	}
	if calls != 1 {
		t.Fatalf("callback calls = %d, want 1", calls)
	}

	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 7, key, plaintextFor(7)),
		func([]byte) error {
			calls++
			return nil
		},
	); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("retry error = %v, want ErrDuplicate", err)
	}
	if calls != 1 {
		t.Fatalf("callback was invoked again; calls = %d", calls)
	}

	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 8, key, plaintextFor(8)),
		nil,
	); !errors.Is(err, ErrNilDeliver) {
		t.Fatalf("nil callback error = %v, want ErrNilDeliver", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 8, key, plaintextFor(8)),
		func([]byte) error {
			calls++
			return nil
		},
	); err != nil {
		t.Fatalf("sequence consumed before nil callback check: %v", err)
	}
	if calls != 2 {
		t.Fatalf("callback calls = %d, want 2", calls)
	}
}

func TestConcurrentSameRecordDeliveredOnce(t *testing.T) {
	const goroutines = 64

	tests := []struct {
		name       string
		callback   func(*atomic.Int64) DeliverFunc
		wantResult func(error) bool
	}{
		{
			name: "successful callback",
			callback: func(calls *atomic.Int64) DeliverFunc {
				return func([]byte) error {
					calls.Add(1)
					return nil
				}
			},
			wantResult: func(err error) bool { return err == nil },
		},
		{
			name: "callback error",
			callback: func(calls *atomic.Int64) DeliverFunc {
				return func([]byte) error {
					calls.Add(1)
					return errConcurrentDelivery
				}
			},
			wantResult: func(err error) bool { return err == errConcurrentDelivery },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connectionID := []byte("concurrent-same")
			key := testKey(0x44)
			receiver := testReceiver(t, connectionID)
			testInstall(t, receiver, 1, key)
			record := testRecord(t, connectionID, 1, 77, key, plaintextFor(77))

			var calls atomic.Int64
			start := make(chan struct{})
			results := make(chan error, goroutines)
			var wait sync.WaitGroup
			for i := 0; i < goroutines; i++ {
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-start
					results <- receiver.Receive(record, test.callback(&calls))
				}()
			}
			close(start)
			wait.Wait()
			close(results)

			var winners, duplicates int
			for err := range results {
				switch {
				case test.wantResult(err):
					winners++
				case errors.Is(err, ErrDuplicate):
					duplicates++
				default:
					t.Errorf("unexpected Receive error: %v", err)
				}
			}
			if winners != 1 {
				t.Fatalf("successful receives = %d, want 1", winners)
			}
			if duplicates != goroutines-1 {
				t.Fatalf("duplicate receives = %d, want %d", duplicates, goroutines-1)
			}
			if calls.Load() != 1 {
				t.Fatalf("callback calls = %d, want 1", calls.Load())
			}
		})
	}
}

var errConcurrentDelivery = errors.New("concurrent callback failure")

func TestKeyRotationAndEpochLifetime(t *testing.T) {
	connectionID := []byte("rotation")
	key1 := testKey(0x51)
	key2 := testKey(0x52)
	key3 := testKey(0x53)
	attackerKey := testKey(0xEE)

	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key1)

	var deliveries int
	deliver := func([]byte) error {
		deliveries++
		return nil
	}
	receive := func(epoch uint32, sequence uint64, key []byte) error {
		return receiver.Receive(
			testRecord(t, connectionID, epoch, sequence, key, plaintextFor(sequence)),
			deliver,
		)
	}

	if err := receive(1, 1, key1); err != nil {
		t.Fatalf("epoch 1 initial receive: %v", err)
	}
	if err := receiver.Install(2, key2); err != nil {
		t.Fatalf("rotate to epoch 2: %v", err)
	}

	if err := receiver.Install(2, attackerKey); !errors.Is(err, ErrEpochExists) {
		t.Fatalf("same-epoch replacement error = %v, want ErrEpochExists", err)
	}
	if err := receiver.Install(1, attackerKey); !errors.Is(err, ErrEpochExists) {
		t.Fatalf("previous-epoch replacement error = %v, want ErrEpochExists", err)
	}
	if err := receiver.Install(0, attackerKey); !errors.Is(err, ErrEpochOrder) {
		t.Fatalf("lower-epoch install error = %v, want ErrEpochOrder", err)
	}

	if err := receive(1, 2, key1); err != nil {
		t.Fatalf("previous epoch should remain readable: %v", err)
	}
	if err := receive(2, 1, key2); err != nil {
		t.Fatalf("current epoch receive: %v", err)
	}
	if err := receive(2, 2, attackerKey); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("replacement key was accepted: %v", err)
	}

	if err := receiver.RotateKey(3, key3); err != nil {
		t.Fatalf("rotate to epoch 3: %v", err)
	}
	if err := receive(1, 3, key1); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("two-epochs-old error = %v, want ErrEpochUnavailable", err)
	}
	if err := receive(2, 3, key2); err != nil {
		t.Fatalf("previous epoch 2 receive: %v", err)
	}
	if err := receive(3, 1, key3); err != nil {
		t.Fatalf("current epoch 3 receive: %v", err)
	}

	if err := receiver.Install(2, attackerKey); !errors.Is(err, ErrEpochExists) {
		t.Fatalf("reinstall previous epoch error = %v, want ErrEpochExists", err)
	}
	if err := receive(2, 4, key2); err != nil {
		t.Fatalf("epoch 2 key changed after rejected reinstall: %v", err)
	}
	if deliveries != 6 {
		t.Fatalf("deliveries = %d, want 6", deliveries)
	}
}

func TestRetirePreviousEpoch(t *testing.T) {
	connectionID := []byte("retire")
	key1 := testKey(0x61)
	key2 := testKey(0x62)
	key3 := testKey(0x63)

	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key1)
	testInstall(t, receiver, 2, key2)

	if err := receiver.Retire(1); err != nil {
		t.Fatalf("Retire(1): %v", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 1, key1, plaintextFor(1)),
		func([]byte) error {
			t.Fatal("retired epoch record was delivered")
			return nil
		},
	); !errors.Is(err, ErrEpochRetired) {
		t.Fatalf("retired epoch error = %v, want ErrEpochRetired", err)
	}
	if err := receiver.Retire(1); err != nil {
		t.Fatalf("idempotent Retire(1): %v", err)
	}
	if err := receiver.Retire(2); !errors.Is(err, ErrCannotRetireCurrent) {
		t.Fatalf("Retire(current) error = %v, want ErrCannotRetireCurrent", err)
	}
	if err := receiver.Retire(99); !errors.Is(err, ErrEpochNotFound) {
		t.Fatalf("Retire(unknown) error = %v, want ErrEpochNotFound", err)
	}

	if err := receiver.Receive(
		testRecord(t, connectionID, 2, 1, key2, plaintextFor(1)),
		func([]byte) error { return nil },
	); err != nil {
		t.Fatalf("current epoch receive: %v", err)
	}

	if err := receiver.Install(3, key3); err != nil {
		t.Fatalf("rotate to epoch 3: %v", err)
	}
	if err := receiver.Retire(2); err != nil {
		t.Fatalf("Retire(2): %v", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 2, 2, key2, plaintextFor(2)),
		func([]byte) error {
			t.Fatal("retired epoch 2 record was delivered")
			return nil
		},
	); !errors.Is(err, ErrEpochRetired) {
		t.Fatalf("retired epoch 2 error = %v, want ErrEpochRetired", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 2, key1, plaintextFor(2)),
		func([]byte) error {
			t.Fatal("discarded epoch 1 record was delivered")
			return nil
		},
	); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("discarded epoch 1 error = %v, want ErrEpochUnavailable", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 3, 1, key3, plaintextFor(1)),
		func([]byte) error { return nil },
	); err != nil {
		t.Fatalf("epoch 3 receive: %v", err)
	}
}

func TestSequenceDoesNotWrap(t *testing.T) {
	connectionID := []byte("sequence-wrap")
	key := testKey(0x71)
	receiver := testReceiver(t, connectionID)
	testInstall(t, receiver, 1, key)

	deliver := func([]byte) error { return nil }
	for _, sequence := range []uint64{math.MaxUint64, math.MaxUint64 - 1, math.MaxUint64 - 1023} {
		if err := receiver.Receive(
			testRecord(t, connectionID, 1, sequence, key, plaintextFor(sequence)),
			deliver,
		); err != nil {
			t.Fatalf("Receive(%d): %v", sequence, err)
		}
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 0, key, plaintextFor(0)),
		deliver,
	); !errors.Is(err, ErrTooOld) {
		t.Fatalf("wrapped sequence error = %v, want ErrTooOld", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, math.MaxUint64-1024, key, plaintextFor(0)),
		deliver,
	); !errors.Is(err, ErrTooOld) {
		t.Fatalf("sequence at window edge error = %v, want ErrTooOld", err)
	}
}

func TestReceiverValidationAndKeyHandling(t *testing.T) {
	connectionID := []byte("validation")
	if _, err := NewReceiver(connectionID); err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}

	receiver := testReceiver(t, connectionID)
	if err := receiver.Install(1, []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid key error = %v, want ErrInvalidKey", err)
	}
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 1, testKey(1), plaintextFor(1)),
		func([]byte) error { return nil },
	); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("receive before successful install error = %v", err)
	}

	key := testKey(0x81)
	testInstall(t, receiver, 1, key)
	key[0] ^= 0xFF
	if err := receiver.Receive(
		testRecord(t, connectionID, 1, 1, testKey(0x81), plaintextFor(1)),
		func([]byte) error { return nil },
	); err != nil {
		t.Fatalf("receiver retained caller key slice: %v", err)
	}

	short := testRecord(t, connectionID, 1, 2, testKey(0x81), nil)
	short.Ciphertext = short.Ciphertext[:aeadTagSize-1]
	if err := receiver.Receive(short, func([]byte) error { return nil }); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("short ciphertext error = %v, want ErrAuthentication", err)
	}

	originalID := receiver.ConnectionID()
	originalID[0] ^= 0xFF
	if !bytes.Equal(receiver.ConnectionID(), connectionID) {
		t.Fatal("ConnectionID returned internal storage")
	}

	if _, err := Seal(connectionID, 1, 1, []byte("bad"), []byte("plaintext")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Seal invalid key error = %v, want ErrInvalidKey", err)
	}
}
