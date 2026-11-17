package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	key1 = []byte("0123456789abcdef0123456789abcdef")
	key2 = []byte("fedcba9876543210fedcba9876543210")
	key3 = []byte("abcdef0123456789abcdef0123456789")
)

func mustSeal(t *testing.T, key []byte, connectionID []byte, epoch uint32, sequence uint64, plaintext []byte) Record {
	t.Helper()
	record, err := SealRecord(key, Record{
		ConnectionID: connectionID,
		Epoch:        epoch,
		Sequence:     sequence,
	}, plaintext)
	if err != nil {
		t.Fatalf("SealRecord(%d, %d): %v", epoch, sequence, err)
	}
	return record
}

func mustReceiver(t *testing.T, epoch uint32, key []byte) *Receiver {
	t.Helper()
	receiver, err := NewReceiver(epoch, key)
	if err != nil {
		t.Fatalf("NewReceiver(%d): %v", epoch, err)
	}
	return receiver
}

func deliverCollector(delivered *[]byte) func([]byte) error {
	return func(plaintext []byte) error {
		*delivered = append((*delivered)[:0], plaintext...)
		return nil
	}
}

func TestRecordAADAndNonceLayout(t *testing.T) {
	connectionID := []byte{0xaa, 0xbb, 0xcc}
	epoch := uint32(0x01020304)
	sequence := uint64(0x0102030405060708)

	aad, err := RecordAAD(connectionID, epoch, sequence)
	if err != nil {
		t.Fatal(err)
	}
	wantAAD := []byte{
		0x00, 0x00, 0x00, 0x03,
		0xaa, 0xbb, 0xcc,
		0x01, 0x02, 0x03, 0x04,
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	}
	if !bytes.Equal(aad, wantAAD) {
		t.Fatalf("AAD = %x, want %x", aad, wantAAD)
	}

	nonce := RecordNonce(epoch, sequence)
	wantNonce := []byte{0x01, 0x02, 0x03, 0x04, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(nonce[:], wantNonce) {
		t.Fatalf("nonce = %x, want %x", nonce, wantNonce)
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}
}

func TestSealRecordUsesSpecifiedAADAndNonce(t *testing.T) {
	connectionID := []byte("connection-17")
	epoch := uint32(7)
	sequence := uint64(99)
	plaintext := []byte("authenticated payload")

	record := mustSeal(t, key1, connectionID, epoch, sequence, plaintext)
	aad, err := RecordAAD(connectionID, epoch, sequence)
	if err != nil {
		t.Fatal(err)
	}
	nonce := RecordNonce(epoch, sequence)

	block, err := aes.NewCipher(key1)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	got, err := aead.Open(nil, nonce[:], record.Ciphertext, aad)
	if err != nil {
		t.Fatalf("independent AES-GCM Open failed: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext = %q, want %q", got, plaintext)
	}
	if len(record.Ciphertext) != len(plaintext)+aead.Overhead() {
		t.Fatalf("ciphertext length = %d, want %d", len(record.Ciphertext), len(plaintext)+aead.Overhead())
	}

	record.ConnectionID = []byte("connection-18")
	if _, err := OpenRecord(key1, record); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("OpenRecord after AAD tamper error = %v, want ErrAuthentication", err)
	}
}

func TestOutOfOrderDuplicateTooOldAndAuthenticationFailure(t *testing.T) {
	connectionID := []byte("conn-a")
	receiver := mustReceiver(t, 4, key1)
	var delivered []byte
	var callbacks atomic.Int32
	deliver := func(plaintext []byte) error {
		callbacks.Add(1)
		return deliverCollector(&delivered)(plaintext)
	}

	// Seed the window at 5000, then prove out-of-order delivery within it.
	for _, sequence := range []uint64{5000, 4999, 4500, 4990} {
		record := mustSeal(t, key1, connectionID, 4, sequence, []byte("payload"))
		if err := receiver.Receive(record, deliver); err != nil {
			t.Fatalf("Receive(sequence=%d): %v", sequence, err)
		}
	}
	if got, want := callbacks.Load(), int32(4); got != want {
		t.Fatalf("callbacks = %d, want %d", got, want)
	}

	duplicate := mustSeal(t, key1, connectionID, 4, 4999, []byte("payload"))
	if err := receiver.Receive(duplicate, deliver); !errors.Is(err, ErrDuplicate) || !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate error = %v, want ErrDuplicate and ErrReplay", err)
	}

	// 5000-1024 is exactly one past the inclusive 1024-bit window.
	tooOld := mustSeal(t, key1, connectionID, 4, 3976, []byte("payload"))
	if err := receiver.Receive(tooOld, deliver); !errors.Is(err, ErrTooOld) || !errors.Is(err, ErrReplay) {
		t.Fatalf("too-old error = %v, want ErrTooOld and ErrReplay", err)
	}
	if got, want := callbacks.Load(), int32(4); got != want {
		t.Fatalf("callbacks after rejected records = %d, want %d", got, want)
	}

	// Authentication failure is rejected before replay state or callback use.
	tampered := mustSeal(t, key1, connectionID, 4, 5001, []byte("payload"))
	tampered.Ciphertext[len(tampered.Ciphertext)-1] ^= 0xff
	if err := receiver.Receive(tampered, deliver); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered error = %v, want ErrAuthentication", err)
	}
	if got, want := callbacks.Load(), int32(4); got != want {
		t.Fatalf("callbacks after authentication failure = %d, want %d", got, want)
	}

	recovered := mustSeal(t, key1, connectionID, 4, 5001, []byte("fresh"))
	if err := receiver.Receive(recovered, deliver); err != nil {
		t.Fatalf("Receive after authentication failure: %v", err)
	}
	if got := callbacks.Load(); got != 5 {
		t.Fatalf("callbacks = %d, want 5", got)
	}
}

func TestForgedHighSequenceDoesNotAdvanceWindow(t *testing.T) {
	connectionID := []byte("conn-forged")
	receiver := mustReceiver(t, 8, key1)
	var callbacks atomic.Int32
	deliver := func([]byte) error {
		callbacks.Add(1)
		return nil
	}

	valid := mustSeal(t, key1, connectionID, 8, 100, []byte("valid"))
	forged := valid
	forged.Sequence = 1 << 50
	if err := receiver.Receive(forged, deliver); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("forged high-sequence error = %v, want ErrAuthentication", err)
	}

	// If the forged record had moved the window, sequence 101 would be too old.
	next := mustSeal(t, key1, connectionID, 8, 101, []byte("next"))
	if err := receiver.Receive(next, deliver); err != nil {
		t.Fatalf("Receive after forged high sequence: %v", err)
	}
	if err := receiver.Receive(valid, deliver); err != nil {
		t.Fatalf("Receive original valid record: %v", err)
	}
	if got := callbacks.Load(); got != 2 {
		t.Fatalf("callbacks = %d, want 2", got)
	}
}

func TestConcurrentSameRecordDeliveredOnce(t *testing.T) {
	const goroutines = 128

	connectionID := []byte("conn-concurrent")
	receiver := mustReceiver(t, 2, key1)
	record := mustSeal(t, key1, connectionID, 2, 42, []byte("exactly once"))

	start := make(chan struct{})
	var callbacks atomic.Int32
	var successes atomic.Int32
	errs := make(chan error, goroutines)
	var wait sync.WaitGroup
	wait.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wait.Done()
			<-start
			err := receiver.Receive(record, func(plaintext []byte) error {
				if string(plaintext) != "exactly once" {
					return errors.New("unexpected plaintext")
				}
				callbacks.Add(1)
				return nil
			})
			if err == nil {
				successes.Add(1)
			}
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful receives = %d, want 1", got)
	}
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("callbacks = %d, want 1", got)
	}
	for err := range errs {
		if err != nil && !errors.Is(err, ErrDuplicate) {
			t.Fatalf("concurrent receive error = %v, want nil or ErrDuplicate", err)
		}
	}
}

func TestCallbackErrorStillConsumesRecord(t *testing.T) {
	connectionID := []byte("conn-callback")
	receiver := mustReceiver(t, 1, key1)
	record := mustSeal(t, key1, connectionID, 1, 7, []byte("payload"))
	callbackErr := errors.New("application rejected payload")
	var callbacks atomic.Int32
	deliver := func([]byte) error {
		callbacks.Add(1)
		return callbackErr
	}

	err := receiver.Receive(record, deliver)
	if !errors.Is(err, ErrDelivery) || !errors.Is(err, callbackErr) {
		t.Fatalf("first Receive error = %v, want ErrDelivery wrapping callback error", err)
	}
	err = receiver.Receive(record, deliver)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second Receive error = %v, want ErrDuplicate", err)
	}
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("callbacks = %d, want 1", got)
	}
}

func TestReplayWindowBoundary(t *testing.T) {
	var window replayWindow
	if err := window.accept(5000); err != nil {
		t.Fatal(err)
	}
	if err := window.accept(3977); err != nil {
		t.Fatalf("sequence at distance 1023: %v", err)
	}
	if err := window.accept(3976); !errors.Is(err, ErrTooOld) {
		t.Fatalf("sequence at distance 1024: %v, want ErrTooOld", err)
	}
	if err := window.accept(3977); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate within window: %v, want ErrDuplicate", err)
	}

	if err := window.accept(7000); err != nil {
		t.Fatal(err)
	}
	if err := window.accept(5977); err != nil {
		t.Fatalf("sequence at new-window distance 1023: %v", err)
	}
	if err := window.accept(5976); !errors.Is(err, ErrTooOld) {
		t.Fatalf("sequence just outside shifted window: %v, want ErrTooOld", err)
	}
}

func TestReplayWindowRandomizedAgainstReference(t *testing.T) {
	var window replayWindow
	var reference replayReference
	random := rand.New(rand.NewSource(9)) //nolint:gosec -- deterministic test input

	for i := 0; i < 20000; i++ {
		var sequence uint64
		switch i % 5 {
		case 0:
			sequence = uint64(i + 1)
		case 1:
			sequence = uint64(i) / 3
		default:
			sequence = random.Uint64() % 50000
		}

		got := window.accept(sequence)
		want := reference.accept(sequence)
		if !sameReplayResult(got, want) {
			t.Fatalf("iteration %d, sequence %d: got %v, want %v", i, sequence, got, want)
		}
	}
}

type replayReference struct {
	max    uint64
	seen   map[uint64]bool
	seeded bool
}

func (r *replayReference) accept(sequence uint64) error {
	if !r.seeded {
		r.seeded = true
		r.max = sequence
		r.seen = map[uint64]bool{sequence: true}
		return nil
	}
	if sequence > r.max {
		r.max = sequence
	}
	if r.max-sequence >= replayWindowBits {
		return ErrTooOld
	}
	if r.seen[sequence] {
		return ErrDuplicate
	}
	r.seen[sequence] = true
	return nil
}

func sameReplayResult(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want) || errors.Is(want, got)
}

func TestSequenceDoesNotWrap(t *testing.T) {
	connectionID := []byte("conn-wrap")
	receiver := mustReceiver(t, 1, key1)
	var callbacks atomic.Int32
	deliver := func([]byte) error {
		callbacks.Add(1)
		return nil
	}

	maxRecord := mustSeal(t, key1, connectionID, 1, ^uint64(0), []byte("max"))
	if err := receiver.Receive(maxRecord, deliver); err != nil {
		t.Fatalf("Receive(max uint64): %v", err)
	}
	previous := mustSeal(t, key1, connectionID, 1, ^uint64(0)-1, []byte("previous"))
	if err := receiver.Receive(previous, deliver); err != nil {
		t.Fatalf("Receive(max uint64-1): %v", err)
	}
	wrapped := mustSeal(t, key1, connectionID, 1, 0, []byte("wrapped"))
	if err := receiver.Receive(wrapped, deliver); !errors.Is(err, ErrTooOld) {
		t.Fatalf("Receive(0) after max: %v, want ErrTooOld", err)
	}
	if err := receiver.Receive(maxRecord, deliver); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Receive(max uint64) again: %v, want ErrDuplicate", err)
	}
	if got := callbacks.Load(); got != 2 {
		t.Fatalf("callbacks = %d, want 2", got)
	}
}

func TestRotationCurrentPreviousAndRetire(t *testing.T) {
	connectionID := []byte("conn-rotate")
	receiver := mustReceiver(t, 10, key1)
	var callbacks atomic.Int32
	deliver := func([]byte) error {
		callbacks.Add(1)
		return nil
	}

	epoch10 := mustSeal(t, key1, connectionID, 10, 42, []byte("epoch-10"))
	if err := receiver.Receive(epoch10, deliver); err != nil {
		t.Fatalf("initial epoch: %v", err)
	}

	if err := receiver.Rotate(11, key2); err != nil {
		t.Fatalf("Rotate(11): %v", err)
	}
	if err := receiver.Receive(mustSeal(t, key1, connectionID, 10, 43, []byte("previous")), deliver); err != nil {
		t.Fatalf("previous epoch while active: %v", err)
	}
	if err := receiver.Receive(mustSeal(t, key2, connectionID, 11, 1, []byte("current")), deliver); err != nil {
		t.Fatalf("current epoch: %v", err)
	}

	// Same-epoch key replacement must fail and must not reset replay state.
	if err := receiver.Rotate(11, key3); !errors.Is(err, ErrEpochExists) {
		t.Fatalf("Rotate to same epoch: %v, want ErrEpochExists", err)
	}
	if err := receiver.Receive(mustSeal(t, key2, connectionID, 11, 1, []byte("current")), deliver); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("current duplicate after rejected replacement: %v, want ErrDuplicate", err)
	}
	if err := receiver.Receive(mustSeal(t, key1, connectionID, 10, 44, []byte("previous")), deliver); err != nil {
		t.Fatalf("previous epoch after rejected replacement: %v", err)
	}
	if err := receiver.Rotate(9, key3); !errors.Is(err, ErrEpochRollback) {
		t.Fatalf("Rotate to older epoch: %v, want ErrEpochRollback", err)
	}

	// Rotating forward drops the second-most-recent epoch.
	if err := receiver.Rotate(12, key3); err != nil {
		t.Fatalf("Rotate(12): %v", err)
	}
	if err := receiver.Receive(mustSeal(t, key1, connectionID, 10, 45, []byte("dropped")), deliver); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("epoch 10 after second rotation: %v, want ErrEpochUnavailable", err)
	}
	if err := receiver.Receive(mustSeal(t, key2, connectionID, 11, 2, []byte("previous")), deliver); err != nil {
		t.Fatalf("epoch 11 after second rotation: %v", err)
	}
	if err := receiver.Receive(mustSeal(t, key3, connectionID, 12, 1, []byte("current")), deliver); err != nil {
		t.Fatalf("epoch 12 after second rotation: %v", err)
	}
	if err := receiver.Retire(11); err != nil {
		t.Fatalf("Retire(11): %v", err)
	}
	if err := receiver.Receive(mustSeal(t, key2, connectionID, 11, 3, []byte("retired")), deliver); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("retired epoch: %v, want ErrEpochUnavailable", err)
	}
	if err := receiver.Retire(11); !errors.Is(err, ErrNoPreviousEpoch) {
		t.Fatalf("Retire twice: %v, want ErrNoPreviousEpoch", err)
	}
	if err := receiver.Retire(12); !errors.Is(err, ErrCannotRetireCurrent) {
		t.Fatalf("Retire current: %v, want ErrCannotRetireCurrent", err)
	}
	if err := receiver.Receive(mustSeal(t, key3, connectionID, 12, 2, []byte("current")), deliver); err != nil {
		t.Fatalf("current after rejected retire: %v", err)
	}
	if got := callbacks.Load(); got != 7 {
		t.Fatalf("callbacks = %d, want 7", got)
	}
}

func TestRotationAndRetireRace(t *testing.T) {
	const recordsPerEpoch = 256

	connectionID := []byte("conn-race")
	receiver := mustReceiver(t, 1, key1)
	if err := receiver.Rotate(2, key2); err != nil {
		t.Fatal(err)
	}

	epoch1 := make([]Record, recordsPerEpoch)
	epoch2 := make([]Record, recordsPerEpoch)
	for i := 0; i < recordsPerEpoch; i++ {
		epoch1[i] = mustSeal(t, key1, connectionID, 1, uint64(i+1), []byte("epoch-1"))
		epoch2[i] = mustSeal(t, key2, connectionID, 2, uint64(i+1), []byte("epoch-2"))
	}

	var delivered [2][recordsPerEpoch]atomic.Int32
	var successes atomic.Int32
	start := make(chan struct{})
	rotationDone := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(recordsPerEpoch * 2)

	for i := 0; i < recordsPerEpoch; i++ {
		for epochIndex, record := range []Record{epoch1[i], epoch2[i]} {
			epochIndex := epochIndex
			index := i
			go func() {
				defer wait.Done()
				<-start
				err := receiver.Receive(record, func([]byte) error {
					delivered[epochIndex][index].Add(1)
					return nil
				})
				if err == nil {
					successes.Add(1)
				} else if !errors.Is(err, ErrEpochUnavailable) {
					t.Errorf("race Receive(epoch=%d, sequence=%d): %v", epochIndex+1, index+1, err)
				}
			}()
		}
	}

	go func() {
		defer close(rotationDone)
		<-start
		if err := receiver.Rotate(3, key3); err != nil {
			t.Errorf("concurrent Rotate(3): %v", err)
		}
	}()
	close(start)
	wait.Wait()
	<-rotationDone

	for epochIndex := range delivered {
		for i := range delivered[epochIndex] {
			if got := delivered[epochIndex][i].Load(); got > 1 {
				t.Fatalf("epoch %d sequence %d delivered %d times", epochIndex+1, i+1, got)
			}
		}
	}

	var deliveredCount int32
	for epochIndex := range delivered {
		for i := range delivered[epochIndex] {
			deliveredCount += delivered[epochIndex][i].Load()
		}
	}
	if deliveredCount != successes.Load() {
		t.Fatalf("callbacks = %d, successful receives = %d", deliveredCount, successes.Load())
	}

	// Epoch 1 is now two generations old; epoch 2 remains the previous epoch.
	if err := receiver.Receive(mustSeal(t, key1, connectionID, 1, 9999, []byte("old")), func([]byte) error { return nil }); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("late epoch 1 record: %v, want ErrEpochUnavailable", err)
	}
	if err := receiver.Receive(mustSeal(t, key2, connectionID, 2, 9999, []byte("previous")), func([]byte) error { return nil }); err != nil {
		t.Fatalf("late epoch 2 record: %v", err)
	}
	if err := receiver.Retire(2); err != nil {
		t.Fatalf("Retire(2): %v", err)
	}
	if err := receiver.Receive(mustSeal(t, key2, connectionID, 2, 10000, []byte("retired")), func([]byte) error { return nil }); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("epoch 2 after retire: %v, want ErrEpochUnavailable", err)
	}
}

func TestConcurrentSameEpochRotationDoesNotReplaceWinner(t *testing.T) {
	const goroutines = 32

	connectionID := []byte("conn-rotation-race")
	receiver := mustReceiver(t, 10, key1)

	start := make(chan struct{})
	results := make(chan error, goroutines)
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		key := key2
		if i%2 == 0 {
			key = key3
		}
		go func() {
			defer wait.Done()
			<-start
			results <- receiver.Rotate(11, key)
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var won int
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrEpochExists):
		default:
			t.Fatalf("Rotate(11) error = %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("successful rotations = %d, want 1", won)
	}

	var callbacks atomic.Int32
	deliver := func([]byte) error {
		callbacks.Add(1)
		return nil
	}
	accepted := 0
	for _, key := range [][]byte{key2, key3} {
		err := receiver.Receive(mustSeal(t, key, connectionID, 11, 1, []byte("current")), deliver)
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrAuthentication):
		default:
			t.Fatalf("Receive with candidate key: %v", err)
		}
	}
	if accepted != 1 || callbacks.Load() != 1 {
		t.Fatalf("accepted keys = %d, callbacks = %d, want 1 and 1", accepted, callbacks.Load())
	}
}

func TestRetireWaitsForAcceptedCallbackAndRejectsNewRecords(t *testing.T) {
	connectionID := []byte("conn-retire-race")
	receiver := mustReceiver(t, 1, key1)
	if err := receiver.Rotate(2, key2); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	first := mustSeal(t, key1, connectionID, 1, 10, []byte("in flight"))
	go func() {
		firstDone <- receiver.Receive(first, func([]byte) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	if err := receiver.Retire(1); err != nil {
		t.Fatalf("Retire(1): %v", err)
	}
	late := mustSeal(t, key1, connectionID, 1, 11, []byte("late"))
	if err := receiver.Receive(late, func([]byte) error { return nil }); !errors.Is(err, ErrEpochUnavailable) {
		t.Fatalf("Receive after Retire: %v, want ErrEpochUnavailable", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("in-flight Receive: %v", err)
	}
}

func TestIndependentConnectionReplayWindows(t *testing.T) {
	receiver := mustReceiver(t, 1, key1)
	var callbacks atomic.Int32
	deliver := func([]byte) error {
		callbacks.Add(1)
		return nil
	}

	for _, connectionID := range [][]byte{[]byte("conn-1"), []byte("conn-2")} {
		record := mustSeal(t, key1, connectionID, 1, 1, []byte("payload"))
		if err := receiver.Receive(record, deliver); err != nil {
			t.Fatalf("Receive(%q): %v", connectionID, err)
		}
	}
	if got := callbacks.Load(); got != 2 {
		t.Fatalf("callbacks = %d, want 2", got)
	}
}

func TestInvalidKeyAndNilCallback(t *testing.T) {
	if _, err := NewReceiver(1, []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewReceiver with short key: %v, want ErrInvalidKey", err)
	}
	if _, err := SealRecord([]byte("short"), Record{}, nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("SealRecord with short key: %v, want ErrInvalidKey", err)
	}

	receiver := mustReceiver(t, 1, key1)
	record := mustSeal(t, key1, []byte("conn"), 1, 1, nil)
	if err := receiver.Receive(record, nil); !errors.Is(err, ErrNilCallback) {
		t.Fatalf("Receive with nil callback: %v, want ErrNilCallback", err)
	}
	// A nil callback must not consume the record.
	if err := receiver.Receive(record, func([]byte) error { return nil }); err != nil {
		t.Fatalf("Receive after nil callback: %v", err)
	}
}

func TestAADLengthIsBigEndian(t *testing.T) {
	connectionID := make([]byte, 258)
	aad, err := RecordAAD(connectionID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := binary.BigEndian.Uint32(aad[:4]), uint32(258); got != want {
		t.Fatalf("AAD connection ID length = %d, want %d", got, want)
	}
}
