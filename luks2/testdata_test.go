package luks2

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

const testHeaderSize = uint64(16384)

// validJSON returns a well-formed metadata document. Both keyslot areas fit in
// the keyslots region [2*16384, 8388608) and do not overlap.
func validJSON() string {
	return `{
  "keyslots": {
    "0": {
      "type": "luks2",
      "key_size": 64,
      "priority": {"type": "normal"},
      "af": {"type": "luks1", "stripes": 4000, "hash": "sha256"},
      "area": {"type": "raw", "offset": "32768", "size": "131072",
               "encryption": "aes-xts-plain64", "key_size": 128},
      "kdf": {"type": "argon2id", "time": 4, "memory": 1048576,
              "cpus": 4, "salt": "c2FsdHNhbHRzYWx0c2FsdA=="}
    },
    "1": {
      "type": "luks2",
      "key_size": 64,
      "priority": {"type": "normal"},
      "af": {"type": "luks1", "stripes": 4000, "hash": "sha256"},
      "area": {"type": "raw", "offset": "163840", "size": "131072",
               "encryption": "aes-xts-plain64", "key_size": 128},
      "kdf": {"type": "argon2id", "time": 5, "memory": 2097152,
              "cpus": 8, "salt": "bW9yZXNhbHRtb3Jlc2FsdA=="}
    }
  },
  "digests": {
    "0": {
      "type": "pbkdf2",
      "keyslots": ["0", "1"],
      "segments": ["0"],
      "salt": "ZGlnZXN0c2FsdGRpZ2VzdA==",
      "digest": "ZGlnaWVzdGRpZ2VzdGRpZ2VzdA==",
      "hash": "sha256",
      "iterations": 1000
    }
  },
  "segments": {
    "0": {
      "type": "crypt",
      "offset": "8388608",
      "size": "dynamic",
      "encryption": "aes-xts-plain64",
      "sector_size": 4096
    }
  },
  "tokens": {},
  "config": {"json_size": "12288", "keyslots_size": "8355840"}
}`
}

// buildRawHeader places binary header and JSON into a header region and
// computes the checksum with the checksum field zeroed.
func buildRawHeader(t *testing.T, jsonText string, headerSize uint64) []byte {
	t.Helper()
	raw := make([]byte, headerSize)
	copy(raw[offMagic:], Magic)
	binary.LittleEndian.PutUint16(raw[offVersion:], Version2)
	binary.LittleEndian.PutUint64(raw[offHeaderSize:], headerSize)
	binary.LittleEndian.PutUint64(raw[offSeqID:], 1)
	putField(raw, offLabel, lenLabel, "test-label")
	putField(raw, offChecksumAlgorithm, lenChecksumAlgorithm, ChecksumSHA256)
	for i := range raw[offSalt : offSalt+lenSalt] {
		raw[offSalt+i] = byte(i + 1)
	}
	putField(raw, offUUID, lenUUID, "12345678-1234-1234-1234-123456789abc")
	putField(raw, offSubsystem, lenSubsystem, "test")
	binary.LittleEndian.PutUint64(raw[offHeaderOffset:], 0)

	jsonBytes := []byte(jsonText)
	if len(jsonBytes) > int(headerSize-jsonAreaOffset) {
		t.Fatalf("test JSON longer than JSON area")
	}
	copy(raw[jsonAreaOffset:], jsonBytes)

	sum := sha256.Sum256(raw)
	copy(raw[offChecksum:offChecksum+ChecksumSize], sum[:])
	return raw
}

func putField(raw []byte, offset, length int, value string) {
	copy(raw[offset:offset+length], []byte(value))
}

func parseValid(t *testing.T, jsonText string) *Header {
	t.Helper()
	header, err := ParseMetadata([]byte(jsonText))
	if err != nil {
		t.Fatalf("ParseMetadata returned unexpected error: %v", err)
	}
	return header
}
