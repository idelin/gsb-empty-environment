package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
)

const (
	// LogicalSectorSize is the fixed plaintext sector size exposed by Volume.
	LogicalSectorSize = 4096

	// PhysicalSectorSize leaves room for an authenticated header and a 16-byte
	// AES-GCM tag around one logical sector.
	PhysicalSectorSize = 8192

	// StateSectorCount is the number of redundant metadata slots at the start
	// of the device. Physical data sectors begin immediately afterwards.
	StateSectorCount = 8
	DataStartSector  = StateSectorCount

	dataHeaderSize = 128
	dataTagSize    = 16
	authTagSize    = sha256.Size

	formatVersion = 1
)

var (
	dataMagic  = [8]byte{'N', 'S', 'D', 'A', 'T', '0', '0', '1'}
	stateMagic = [8]byte{'N', 'S', 'S', 'T', 'A', '0', '0', '1'}
	nodeMagic  = [8]byte{'N', 'S', 'M', 'A', 'P', '0', '0', '1'}
	aadDomain  = []byte("NETSEC-AAD-V1\x00\x00\x00\x00")
)

type physicalRef struct {
	Sector     uint64
	Generation uint64
}

type mappingEntry struct {
	Logical            uint64
	Physical           uint64
	PhysicalGeneration uint64
}

type dataHeader struct {
	KeyGeneration uint64
	Logical       uint64
	Physical      uint64
	PhysicalGen   uint64
	WriteEpoch    uint64
	PlainLen      uint32
}

func deriveMetaKey(master []byte, volumeID uint64) []byte {
	return deriveKey(master, "netsec-meta-v1", volumeID, 0)
}

func deriveDataKeys(master []byte, volumeID, keyGeneration uint64) (aeadKey, nonceKey []byte) {
	aeadKey = deriveKey(master, "netsec-aead-v1", volumeID, keyGeneration)
	nonceKey = deriveKey(master, "netsec-nonce-v1", volumeID, keyGeneration)
	return aeadKey, nonceKey
}

func deriveKey(master []byte, label string, volumeID, keyGeneration uint64) []byte {
	h := hmac.New(sha256.New, master)
	h.Write([]byte(label))
	h.Write([]byte{0})
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[:8], volumeID)
	binary.BigEndian.PutUint64(buf[8:], keyGeneration)
	h.Write(buf[:])
	return h.Sum(nil)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return aead, nil
}

func sectorNonce(nonceKey []byte, volumeID, keyGeneration, logicalSector, writeEpoch uint64) []byte {
	h := hmac.New(sha256.New, nonceKey)
	var buf [32]byte
	binary.BigEndian.PutUint64(buf[0:8], volumeID)
	binary.BigEndian.PutUint64(buf[8:16], keyGeneration)
	binary.BigEndian.PutUint64(buf[16:24], logicalSector)
	binary.BigEndian.PutUint64(buf[24:32], writeEpoch)
	h.Write(buf[:])
	sum := h.Sum(nil)
	return sum[:12]
}

func sectorAAD(volumeID, keyGeneration, logicalSector, physicalSector, physicalGeneration, writeEpoch uint64, plainLen int) []byte {
	buf := make([]byte, len(aadDomain)+7*8)
	copy(buf, aadDomain)
	off := len(aadDomain)
	for _, value := range []uint64{
		volumeID,
		keyGeneration,
		logicalSector,
		physicalSector,
		physicalGeneration,
		writeEpoch,
		uint64(plainLen),
	} {
		binary.BigEndian.PutUint64(buf[off:off+8], value)
		off += 8
	}
	return buf
}

func sealDataRecord(
	aead cipher.AEAD,
	nonceKey []byte,
	volumeID, keyGeneration, logicalSector, physicalSector, physicalGeneration, writeEpoch uint64,
	plaintext []byte,
) ([]byte, error) {
	if len(plaintext) != LogicalSectorSize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidSize, len(plaintext))
	}
	record := make([]byte, PhysicalSectorSize)
	copy(record[0:8], dataMagic[:])
	binary.BigEndian.PutUint16(record[8:10], formatVersion)
	binary.BigEndian.PutUint64(record[12:20], keyGeneration)
	binary.BigEndian.PutUint64(record[20:28], logicalSector)
	binary.BigEndian.PutUint64(record[28:36], physicalSector)
	binary.BigEndian.PutUint64(record[36:44], physicalGeneration)
	binary.BigEndian.PutUint64(record[44:52], writeEpoch)
	binary.BigEndian.PutUint32(record[52:56], uint32(len(plaintext)))

	nonce := sectorNonce(nonceKey, volumeID, keyGeneration, logicalSector, writeEpoch)
	aad := sectorAAD(
		volumeID,
		keyGeneration,
		logicalSector,
		physicalSector,
		physicalGeneration,
		writeEpoch,
		len(plaintext),
	)
	aead.Seal(record[dataHeaderSize:dataHeaderSize], nonce, plaintext, aad)
	return record, nil
}

func openDataRecord(
	aead cipher.AEAD,
	nonceKey []byte,
	record []byte,
	volumeID, keyGeneration, logicalSector, physicalSector, physicalGeneration uint64,
) ([]byte, error) {
	if len(record) != PhysicalSectorSize {
		return nil, fmt.Errorf("%w: record has %d bytes", ErrInvalidSize, len(record))
	}
	if subtle.ConstantTimeCompare(record[0:8], dataMagic[:]) != 1 {
		return nil, ErrAuthentication
	}
	if binary.BigEndian.Uint16(record[8:10]) != formatVersion {
		return nil, fmt.Errorf("%w: unsupported data version", ErrCorrupt)
	}
	header := dataHeader{
		KeyGeneration: binary.BigEndian.Uint64(record[12:20]),
		Logical:       binary.BigEndian.Uint64(record[20:28]),
		Physical:      binary.BigEndian.Uint64(record[28:36]),
		PhysicalGen:   binary.BigEndian.Uint64(record[36:44]),
		WriteEpoch:    binary.BigEndian.Uint64(record[44:52]),
		PlainLen:      binary.BigEndian.Uint32(record[52:56]),
	}
	if header.KeyGeneration != keyGeneration || header.Logical != logicalSector {
		return nil, ErrAuthentication
	}
	if header.Physical != physicalSector || header.PhysicalGen != physicalGeneration {
		return nil, ErrPhysicalGeneration
	}
	if header.PlainLen != LogicalSectorSize {
		return nil, fmt.Errorf("%w: authenticated plaintext length %d", ErrCorrupt, header.PlainLen)
	}

	nonce := sectorNonce(nonceKey, volumeID, keyGeneration, logicalSector, header.WriteEpoch)
	aad := sectorAAD(
		volumeID,
		keyGeneration,
		logicalSector,
		physicalSector,
		physicalGeneration,
		header.WriteEpoch,
		LogicalSectorSize,
	)
	plaintext, err := aead.Open(
		nil,
		nonce,
		record[dataHeaderSize:dataHeaderSize+LogicalSectorSize+dataTagSize],
		aad,
	)
	if err != nil {
		return nil, ErrAuthentication
	}
	if len(plaintext) != LogicalSectorSize {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

func authenticateMetadata(metaKey, record []byte) {
	body := len(record) - authTagSize
	for i := body; i < len(record); i++ {
		record[i] = 0
	}
	mac := hmac.New(sha256.New, metaKey)
	mac.Write(record[:body])
	copy(record[body:], mac.Sum(nil))
}

func verifyMetadata(metaKey, record []byte) bool {
	if len(record) < authTagSize {
		return false
	}
	body := len(record) - authTagSize
	mac := hmac.New(sha256.New, metaKey)
	mac.Write(record[:body])
	sum := mac.Sum(nil)
	return subtle.ConstantTimeCompare(sum, record[body:]) == 1
}
