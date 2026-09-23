package rangecoder

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

func trainedModel() *Model {
	m := NewModel()
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 3000; i++ {
		m.update(rng.Intn(NumSymbols))
	}
	return m
}

func TestSnapshotRoundTrip(t *testing.T) {
	m := trainedModel()
	cp := m.Snapshot()
	m2, err := ModelFromCheckpoint(cp)
	if err != nil {
		t.Fatal(err)
	}
	if m2.total != m.total || m2.freq != m.freq {
		t.Fatal("restored model differs from snapshot")
	}
}

func TestCheckpointVersionRejected(t *testing.T) {
	cp := trainedModel().Snapshot()
	binary.BigEndian.PutUint16(cp[4:6], checkpointVersion+1)
	// Fix the checksum so only the version is wrong.
	fixCRC(cp)
	if _, err := ModelFromCheckpoint(cp); err != ErrBadVersion {
		t.Fatalf("want ErrBadVersion, got %v", err)
	}
}

func TestCheckpointMagicRejected(t *testing.T) {
	cp := trainedModel().Snapshot()
	cp[0] = 'X'
	if _, err := ModelFromCheckpoint(cp); err != ErrBadMagic {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestCheckpointChecksumRejected(t *testing.T) {
	cp := trainedModel().Snapshot()
	cp[len(cp)-1] ^= 0x01 // flip a payload bit, keep header
	if _, err := ModelFromCheckpoint(cp); err != ErrBadChecksum {
		t.Fatalf("want ErrBadChecksum, got %v", err)
	}
}

func TestCheckpointLengthRejected(t *testing.T) {
	cp := trainedModel().Snapshot()
	if _, err := ModelFromCheckpoint(cp[:len(cp)-3]); err != ErrBadLength {
		t.Fatalf("truncated: want ErrBadLength, got %v", err)
	}
	if _, err := ModelFromCheckpoint(append(cp, 0x00)); err != ErrBadLength {
		t.Fatalf("extended: want ErrBadLength, got %v", err)
	}
	if _, err := ModelFromCheckpoint(cp[:5]); err != ErrBadLength {
		t.Fatalf("short header: want ErrBadLength, got %v", err)
	}
}

// A checkpoint with a valid checksum but an inconsistent model (freq sum
// != total) must still be rejected.
func TestCheckpointInconsistentModelRejected(t *testing.T) {
	cp := trainedModel().Snapshot()
	payload := cp[checkpointHdrLen:]
	// Find the first freq varint after numSymbols and total, bump it.
	_, n1 := binary.Uvarint(payload)
	_, n2 := binary.Uvarint(payload[n1:])
	i := n1 + n2
	if payload[i] == 0xFF {
		payload[i]--
	} else {
		payload[i]++
	}
	fixCRC(cp)
	if _, err := ModelFromCheckpoint(cp); err != ErrBadPayload {
		t.Fatalf("want ErrBadPayload, got %v", err)
	}
}

func fixCRC(cp []byte) {
	crc := crc32.NewIEEE()
	crc.Write(cp[4 : checkpointHdrLen-4])
	crc.Write(cp[checkpointHdrLen:])
	binary.BigEndian.PutUint32(cp[10:14], crc.Sum32())
}
