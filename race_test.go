package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRetireRaceNeverRedelivers(t *testing.T) {
	const iterations = 16
	const recordsPerIteration = 48

	for iteration := 0; iteration < iterations; iteration++ {
		connectionID := []byte(fmt.Sprintf("retire-race-%d", iteration))
		key1 := bytes.Repeat([]byte{byte(iteration + 1)}, 32)
		key2 := bytes.Repeat([]byte{byte(iteration + 101)}, 32)

		receiver := testReceiver(t, connectionID)
		testInstall(t, receiver, 1, key1)
		testInstall(t, receiver, 2, key2)

		records := make([]Record, recordsPerIteration)
		for i := range records {
			sequence := uint64(i + 1)
			records[i] = testRecord(t, connectionID, 1, sequence, key1, plaintextFor(sequence))
		}

		var callbacks atomic.Int64
		start := make(chan struct{})
		results := make(chan error, recordsPerIteration)
		var wait sync.WaitGroup

		for i := range records {
			record := records[i]
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				results <- receiver.Receive(record, func([]byte) error {
					callbacks.Add(1)
					return nil
				})
			}()
		}

		retireResult := make(chan error, 1)
		go func() {
			<-start
			retireResult <- receiver.Retire(1)
		}()

		close(start)
		wait.Wait()
		close(results)

		if err := <-retireResult; err != nil {
			t.Fatalf("iteration %d: Retire(1): %v", iteration, err)
		}
		for err := range results {
			if err != nil && !errors.Is(err, ErrEpochRetired) {
				t.Fatalf("iteration %d: unexpected receive error: %v", iteration, err)
			}
		}

		deliveredBeforeRetry := callbacks.Load()
		fresh := testRecord(t, connectionID, 1, recordsPerIteration+1, key1, []byte("fresh"))
		if err := receiver.Receive(fresh, func([]byte) error {
			callbacks.Add(1)
			return nil
		}); !errors.Is(err, ErrEpochRetired) {
			t.Fatalf("iteration %d: receive after Retire error = %v, want ErrEpochRetired", iteration, err)
		}

		for _, record := range records {
			if err := receiver.Receive(record, func([]byte) error {
				callbacks.Add(1)
				return nil
			}); !errors.Is(err, ErrEpochRetired) {
				t.Fatalf("iteration %d: retry after Retire error = %v, want ErrEpochRetired", iteration, err)
			}
		}
		if callbacks.Load() != deliveredBeforeRetry {
			t.Fatalf("iteration %d: callback ran after retirement", iteration)
		}
	}
}

func TestRotationRacePreservesEpochBoundaries(t *testing.T) {
	const iterations = 12
	const recordsPerEpoch = 40

	for iteration := 0; iteration < iterations; iteration++ {
		connectionID := []byte(fmt.Sprintf("rotation-race-%d", iteration))
		key1 := bytes.Repeat([]byte{byte(iteration + 1)}, 32)
		key2 := bytes.Repeat([]byte{byte(iteration + 51)}, 32)
		key3 := bytes.Repeat([]byte{byte(iteration + 101)}, 32)

		receiver := testReceiver(t, connectionID)
		testInstall(t, receiver, 1, key1)
		testInstall(t, receiver, 2, key2)

		type candidate struct {
			record Record
			epoch  uint32
			index  int
		}
		candidates := make([]candidate, 0, recordsPerEpoch*3)
		for epoch, key := range [][]byte{key1, key2, key3} {
			for i := 0; i < recordsPerEpoch; i++ {
				sequence := uint64(i + 1)
				candidates = append(candidates, candidate{
					record: testRecord(t, connectionID, uint32(epoch+1), sequence, key, plaintextFor(sequence)),
					epoch:  uint32(epoch + 1),
					index:  i,
				})
			}
		}

		var callbacks [3][recordsPerEpoch]atomic.Int64
		start := make(chan struct{})
		type receiveResult struct {
			epoch uint32
			err   error
		}
		results := make(chan receiveResult, len(candidates))
		var wait sync.WaitGroup

		for _, item := range candidates {
			item := item
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				err := receiver.Receive(item.record, func([]byte) error {
					callbacks[item.epoch-1][item.index].Add(1)
					return nil
				})
				results <- receiveResult{epoch: item.epoch, err: err}
			}()
		}

		rotateResult := make(chan error, 1)
		go func() {
			<-start
			rotateResult <- receiver.Install(3, key3)
		}()

		close(start)
		wait.Wait()
		close(results)

		if err := <-rotateResult; err != nil {
			t.Fatalf("iteration %d: rotate: %v", iteration, err)
		}
		for result := range results {
			switch result.epoch {
			case 2:
				if result.err != nil {
					t.Fatalf("iteration %d: epoch 2 must stay available: %v", iteration, result.err)
				}
			case 1, 3:
				if result.err == nil || errors.Is(result.err, ErrEpochUnavailable) {
					continue
				}
				t.Fatalf("iteration %d: unexpected epoch %d error: %v", iteration, result.epoch, result.err)
			default:
				t.Fatalf("iteration %d: unexpected epoch %d", iteration, result.epoch)
			}
		}
		for epoch := range callbacks {
			for index := range callbacks[epoch] {
				if repeated := callbacks[epoch][index].Load(); repeated > 1 {
					t.Fatalf(
						"iteration %d: epoch %d sequence %d delivered %d times",
						iteration, epoch+1, index+1, repeated,
					)
				}
				if epoch == 1 && callbacks[epoch][index].Load() != 1 {
					t.Fatalf(
						"iteration %d: epoch 2 sequence %d was not delivered",
						iteration, index+1,
					)
				}
			}
		}

		old := testRecord(t, connectionID, 1, recordsPerEpoch+1, key1, []byte("old"))
		if err := receiver.Receive(old, func([]byte) error {
			t.Fatal("old epoch was delivered after rotation completed")
			return nil
		}); !errors.Is(err, ErrEpochUnavailable) {
			t.Fatalf("iteration %d: old epoch error = %v, want ErrEpochUnavailable", iteration, err)
		}

		previous := testRecord(t, connectionID, 2, recordsPerEpoch+1, key2, []byte("previous"))
		if err := receiver.Receive(previous, func([]byte) error { return nil }); err != nil {
			t.Fatalf("iteration %d: previous epoch receive: %v", iteration, err)
		}
		current := testRecord(t, connectionID, 3, recordsPerEpoch+1, key3, []byte("current"))
		if err := receiver.Receive(current, func([]byte) error { return nil }); err != nil {
			t.Fatalf("iteration %d: current epoch receive: %v", iteration, err)
		}
	}
}
