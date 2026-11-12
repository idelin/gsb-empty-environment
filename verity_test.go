package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"testing"
)

const (
	testAlgorithm     = "sha256"
	testDataBlockSize = uint32(4096)
	testHashBlockSize = uint32(4096)
	testDataBlocks    = uint64(8192)
	testRootDigestLen = 32
	testSalt          = "aabbccdd"
)

// buildSuperblock serializes a version 1 dm-verity superblock.
func buildSuperblock(t *testing.T, algorithm string, dataBlockSize, hashBlockSize uint32, dataBlocks uint64) []byte {
	t.Helper()
	block := make([]byte, superblockSize)
	copy(block[:len(superblockMagic)], superblockMagic)
	binary.LittleEndian.PutUint32(block[offVersion:], supportedVersion)
	binary.LittleEndian.PutUint32(block[offHashType:], hashTypeNormal)
	copy(block[offAlgorithm:offAlgorithm+algorithmFieldLen], algorithm)
	binary.LittleEndian.PutUint32(block[offDataBlockSize:], dataBlockSize)
	binary.LittleEndian.PutUint32(block[offHashBlockSize:], hashBlockSize)
	binary.LittleEndian.PutUint64(block[offDataBlocks:], dataBlocks)
	salt := mustHex(t, testSalt)
	binary.LittleEndian.PutUint16(block[offSaltSize:], uint16(len(salt)))
	copy(block[offSalt:offSalt+saltFieldLen], salt)
	return block
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	out, err := parseHex(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return out
}

// treeGeometry computes the reference expected values for the standard test
// fixture (8192 blocks, sha256, 4096-byte hash blocks).
func treeGeometry(t *testing.T) (treeBlocks uint64, hashStart uint64, dataBytes, hashBytes int64) {
	t.Helper()
	treeBlocks, err := HashTreeBlocks(testDataBlocks, testRootDigestLen, testHashBlockSize)
	if err != nil {
		t.Fatalf("HashTreeBlocks: %v", err)
	}
	hashStart = 1 // one superblock block before the tree
	dataBytes = int64(testDataBlocks) * int64(testDataBlockSize)
	hashBytes = int64(hashStart+treeBlocks) * int64(testHashBlockSize)
	return treeBlocks, hashStart, dataBytes, hashBytes
}

func validLine(algorithm string, dataBlockSize, hashBlockSize uint32, dataBlocks, hashStart uint64, optionArgs ...string) string {
	rootDigest := strings.Repeat("ab", testRootDigestLen)
	line := "2 /dev/data /dev/hash " +
		strconv.FormatUint(uint64(dataBlockSize), 10) + " " +
		strconv.FormatUint(uint64(hashBlockSize), 10) + " " +
		strconv.FormatUint(dataBlocks, 10) + " " +
		strconv.FormatUint(hashStart, 10) + " " +
		algorithm + " " + rootDigest + " " + testSalt + " " +
		strconv.Itoa(len(optionArgs))
	for _, arg := range optionArgs {
		line += " " + arg
	}
	return line
}

// validRequest builds the fully compliant fixture.
func validRequest(t *testing.T) *AdmissionRequest {
	t.Helper()
	_, hashStart, dataBytes, hashBytes := treeGeometry(t)
	target, err := ParseTable(validLine(testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks, hashStart))
	if err != nil {
		t.Fatalf("ParseTable valid line: %v", err)
	}
	return &AdmissionRequest{
		Superblock: buildSuperblock(t, testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks),
		Table:      target,
		DataDevice: Device{Path: "/dev/data", Size: dataBytes},
		HashDevice: Device{Path: "/dev/hash", Size: hashBytes},
		Signature:  []byte("detached-signature"),
	}
}

func acceptingVerifier(t *testing.T) SignatureVerifier {
	t.Helper()
	return func(algorithm string, rootHash, signature []byte) error {
		if algorithm != testAlgorithm || len(rootHash) != testRootDigestLen ||
			!bytes.Equal(signature, []byte("detached-signature")) {
			t.Errorf("verifier got algorithm=%q rootHashLen=%d signature=%q", algorithm, len(rootHash), signature)
		}
		return nil
	}
}

func TestAdmit_CompliantTable(t *testing.T) {
	req := validRequest(t)
	if err := Admit(req, acceptingVerifier(t)); err != nil {
		t.Fatalf("compliant request rejected: %v", err)
	}
}

func TestAdmit_RestartOnCorruptionAllowed(t *testing.T) {
	req := validRequest(t)
	req.Table.Options = []string{"restart_on_corruption"}
	if err := Admit(req, acceptingVerifier(t)); err != nil {
		t.Fatalf("restart_on_corruption must be allowed: %v", err)
	}
}

func TestAdmit_IgnoreZeroBlocksRejected(t *testing.T) {
	req := validRequest(t)
	req.Table.Options = []string{"ignore_zero_blocks"}
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrForbiddenOption) {
		t.Fatalf("want ErrForbiddenOption, got %v", err)
	}
}

func TestAdmit_IgnoreCorruptionRejected(t *testing.T) {
	req := validRequest(t)
	req.Table.Options = []string{"restart_on_corruption", "ignore_corruption"}
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrForbiddenOption) || !strings.Contains(err.Error(), "ignore_corruption") {
		t.Fatalf("want ErrForbiddenOption naming ignore_corruption, got %v", err)
	}
}

// The superblock declares one data block fewer than the table: its trailing
// block would be read without verification.
func TestAdmit_SuperblockDeclaresOneBlockFewer(t *testing.T) {
	req := validRequest(t)
	req.Superblock = buildSuperblock(t, testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks-1)
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrTableMismatch) {
		t.Fatalf("want ErrTableMismatch for block count mismatch, got %v", err)
	}
}

// A table claiming one data block fewer than the superblock cannot be loaded.
func TestAdmit_TableDeclaresOneBlockFewer(t *testing.T) {
	_, hashStart, dataBytes, _ := treeGeometry(t)
	line := validLine(testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks-1, hashStart)
	target, err := ParseTable(line)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	req := validRequest(t)
	req.Table = target
	req.DataDevice.Size = dataBytes
	err = Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrTableMismatch) {
		t.Fatalf("want ErrTableMismatch, got %v", err)
	}
}

// Extra data at the end of the data device is refused rather than silently
// exposed unverified.
func TestAdmit_DataDeviceTrailingData(t *testing.T) {
	req := validRequest(t)
	req.DataDevice.Size += 1
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("want ErrTrailingData, got %v", err)
	}
	req.DataDevice.Size += int64(testDataBlockSize)
	err = Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("want ErrTrailingData for extra block, got %v", err)
	}
}

func TestAdmit_DataDeviceTooSmall(t *testing.T) {
	req := validRequest(t)
	req.DataDevice.Size -= 1
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrDeviceTooSmall) {
		t.Fatalf("want ErrDeviceTooSmall, got %v", err)
	}
}

// A hash device one byte too small cannot hold the full layer-by-layer tree.
func TestAdmit_HashDeviceTooSmall(t *testing.T) {
	req := validRequest(t)
	req.HashDevice.Size -= 1
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrHashDeviceTooSmall) {
		t.Fatalf("want ErrHashDeviceTooSmall, got %v", err)
	}
}

func TestAdmit_HashDeviceOneBlockShort(t *testing.T) {
	req := validRequest(t)
	req.HashDevice.Size -= int64(testHashBlockSize)
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrHashDeviceTooSmall) {
		t.Fatalf("want ErrHashDeviceTooSmall, got %v", err)
	}
}

// A larger hash device is fine: extra capacity does not expose unverified
// data through the data device.
func TestAdmit_HashDeviceLargerIsAllowed(t *testing.T) {
	req := validRequest(t)
	req.HashDevice.Size += int64(testHashBlockSize)
	if err := Admit(req, acceptingVerifier(t)); err != nil {
		t.Fatalf("oversized hash device rejected: %v", err)
	}
}

func TestAdmit_BlockSizeMismatch(t *testing.T) {
	req := validRequest(t)
	req.Superblock = buildSuperblock(t, testAlgorithm, 512, testHashBlockSize, testDataBlocks)
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrTableMismatch) {
		t.Fatalf("want ErrTableMismatch for block size, got %v", err)
	}
}

func TestAdmit_RootHashLengthMismatch(t *testing.T) {
	_, hashStart, _, _ := treeGeometry(t)
	line := "2 /dev/data /dev/hash 4096 4096 " +
		strconv.FormatUint(testDataBlocks, 10) + " " + strconv.FormatUint(hashStart, 10) +
		" sha256 " + strings.Repeat("cd", 16) + " " + testSalt + " 0"
	target, err := ParseTable(line)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	req := validRequest(t)
	req.Table = target
	err = Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrRootHash) {
		t.Fatalf("want ErrRootHash, got %v", err)
	}
}

func TestAdmit_SignatureVerifierRejects(t *testing.T) {
	req := validRequest(t)
	wantErr := errors.New("bad signature")
	err := Admit(req, func(algorithm string, rootHash, signature []byte) error { return wantErr })
	if !errors.Is(err, ErrSignature) || !errors.Is(err, wantErr) {
		t.Fatalf("want wrapped ErrSignature, got %v", err)
	}
}

func TestAdmit_NilVerifierRejected(t *testing.T) {
	req := validRequest(t)
	if err := Admit(req, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("want ErrSignature without verifier, got %v", err)
	}
}

func TestAdmit_NilRequest(t *testing.T) {
	if err := Admit(nil, acceptingVerifier(t)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("want ErrInvalidRequest, got %v", err)
	}
}

func TestAdmit_UnsupportedAlgorithm(t *testing.T) {
	req := validRequest(t)
	req.Superblock = buildSuperblock(t, "md5", testDataBlockSize, testHashBlockSize, testDataBlocks)
	req.Table.Algorithm = "md5"
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("want ErrUnsupportedAlgorithm, got %v", err)
	}
}

func TestHashTreeBlocks_LayerByLayer(t *testing.T) {
	// sha256 over 4096-byte blocks: 128 digests per hash block.
	got, err := HashTreeBlocks(128, 32, 4096)
	if err != nil {
		t.Fatalf("HashTreeBlocks: %v", err)
	}
	if got != 1 {
		t.Fatalf("128 data blocks: want 1 hash block, got %d", got)
	}

	// 8192 data blocks -> 64 blocks, then 1 block. Total 65.
	got, err = HashTreeBlocks(8192, 32, 4096)
	if err != nil {
		t.Fatalf("HashTreeBlocks: %v", err)
	}
	if got != 65 {
		t.Fatalf("8192 data blocks: want 65 hash blocks, got %d", got)
	}

	// 129 blocks: first layer needs ceil(129/128)=2 blocks, then 1.
	got, err = HashTreeBlocks(129, 32, 4096)
	if err != nil {
		t.Fatalf("HashTreeBlocks: %v", err)
	}
	if got != 3 {
		t.Fatalf("129 data blocks: want 3 hash blocks, got %d", got)
	}

	if _, err := HashTreeBlocks(0, 32, 4096); !errors.Is(err, ErrHashTree) {
		t.Fatalf("zero blocks: want ErrHashTree, got %v", err)
	}
}

func TestParseTable_VersionOneAndTwo(t *testing.T) {
	v1 := "1 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " -"
	target, err := ParseTable(v1)
	if err != nil {
		t.Fatalf("ParseTable v1: %v", err)
	}
	if target.Version != 1 || len(target.Options) != 0 || target.HashStartBlock != 1 {
		t.Fatalf("unexpected v1 table: %+v", target)
	}
	if len(target.RootHash) != 32 || len(target.Salt) != 0 {
		t.Fatalf("root hash/salt decode wrong: %+v", target)
	}

	v2 := "2 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) +
		" " + testSalt + " 2 restart_on_corruption check_at_most_once"
	target, err = ParseTable(v2)
	if err != nil {
		t.Fatalf("ParseTable v2: %v", err)
	}
	if len(target.Options) != 2 || target.Options[0] != "restart_on_corruption" {
		t.Fatalf("options not parsed: %+v", target.Options)
	}
}

func TestParseTable_Malformed(t *testing.T) {
	bad := []string{
		"",
		"0 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " -",
		"3 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " -",
		"1 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " - extra",
		"2 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " - 2 only-one",
		"2 /dev/data /dev/hash x 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " - 0",
		"2 /dev/data /dev/hash 4096 4096 100 1 sha256 abc - 0",
	}
	for _, line := range bad {
		if _, err := ParseTable(line); !errors.Is(err, ErrInvalidTable) {
			t.Fatalf("line %q: want ErrInvalidTable, got %v", line, err)
		}
	}
}

func TestParseSuperblock_Malformed(t *testing.T) {
	if _, err := ParseSuperblock(make([]byte, 128)); !errors.Is(err, ErrInvalidSuperblock) {
		t.Fatalf("short block: want ErrInvalidSuperblock, got %v", err)
	}
	block := buildSuperblock(t, testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks)
	block[0] = 'x'
	if _, err := ParseSuperblock(block); !errors.Is(err, ErrInvalidSuperblock) {
		t.Fatalf("bad magic: want ErrInvalidSuperblock, got %v", err)
	}

	block = buildSuperblock(t, testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks)
	binary.LittleEndian.PutUint32(block[offVersion:], 2)
	if _, err := ParseSuperblock(block); !errors.Is(err, ErrInvalidSuperblock) {
		t.Fatalf("bad version: want ErrInvalidSuperblock, got %v", err)
	}

	parsed, err := ParseSuperblock(buildSuperblock(t, testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks))
	if err != nil {
		t.Fatalf("ParseSuperblock: %v", err)
	}
	if parsed.Algorithm != testAlgorithm || parsed.DataBlocks != testDataBlocks ||
		parsed.DataBlockSize != testDataBlockSize || !bytes.Equal(parsed.Salt, mustHex(t, testSalt)) {
		t.Fatalf("unexpected parsed superblock: %+v", parsed)
	}
}

func TestAdmit_DevicePathMismatch(t *testing.T) {
	req := validRequest(t)
	req.DataDevice.Path = "/dev/something-else"
	if err := Admit(req, acceptingVerifier(t)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("want ErrInvalidRequest for wrong data device, got %v", err)
	}
}

func TestHashTreeBlocks_Overflow(t *testing.T) {
	// layer * digestSize wraps past uint64 on the first layer.
	if _, err := HashTreeBlocks(^uint64(0), 32, 4096); !errors.Is(err, ErrHashTree) {
		t.Fatalf("want ErrHashTree on overflow, got %v", err)
	}
	if _, err := HashTreeBlocks(1, 0, 4096); !errors.Is(err, ErrHashTree) {
		t.Fatalf("zero digest: want ErrHashTree, got %v", err)
	}
}

func TestAdmit_NonPowerOfTwoBlockSize(t *testing.T) {
	req := validRequest(t)
	req.Table.DataBlockSize = 1000
	req.Superblock = buildSuperblock(t, testAlgorithm, 1000, testHashBlockSize, testDataBlocks)
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("want ErrInvalidTable for non-power-of-two block size, got %v", err)
	}
}

func TestAdmit_HashStartOverflow(t *testing.T) {
	req := validRequest(t)
	req.Table.HashStartBlock = ^uint64(0)
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrHashDeviceTooSmall) {
		t.Fatalf("want ErrHashDeviceTooSmall on start-block overflow, got %v", err)
	}
}

func TestAdmit_BadSuperblock(t *testing.T) {
	req := validRequest(t)
	req.Superblock = req.Superblock[:128]
	if err := Admit(req, acceptingVerifier(t)); !errors.Is(err, ErrInvalidSuperblock) {
		t.Fatalf("want ErrInvalidSuperblock, got %v", err)
	}
}

func TestParseSuperblock_RejectedFields(t *testing.T) {
	cases := map[string]func([]byte){
		"hash type": func(b []byte) {
			binary.LittleEndian.PutUint32(b[offHashType:], 99)
		},
		"salt overflow": func(b []byte) {
			binary.LittleEndian.PutUint16(b[offSaltSize:], uint16(superblockSize))
		},
		"zero algorithm": func(b []byte) {
			b[offAlgorithm] = 0
		},
		"zero data blocks": func(b []byte) {
			binary.LittleEndian.PutUint64(b[offDataBlocks:], 0)
		},
	}
	for name, mutate := range cases {
		block := buildSuperblock(t, testAlgorithm, testDataBlockSize, testHashBlockSize, testDataBlocks)
		mutate(block)
		if _, err := ParseSuperblock(block); !errors.Is(err, ErrInvalidSuperblock) {
			t.Fatalf("%s: want ErrInvalidSuperblock, got %v", name, err)
		}
	}
}

func TestParseTable_HexErrors(t *testing.T) {
	oddDigest := "2 /dev/data /dev/hash 4096 4096 100 1 sha256 abc - 0"
	if _, err := ParseTable(oddDigest); !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("odd digest: want ErrInvalidTable, got %v", err)
	}
	badSalt := "2 /dev/data /dev/hash 4096 4096 100 1 sha256 " + strings.Repeat("ab", 32) + " zz 0"
	if _, err := ParseTable(badSalt); !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("bad salt: want ErrInvalidTable, got %v", err)
	}
	empty, err := parseHex("-")
	if err != nil || empty != nil {
		t.Fatalf("parseHex(\"-\") = %v, %v", empty, err)
	}
}

func TestAdmit_HashDevicePathMismatch(t *testing.T) {
	req := validRequest(t)
	req.HashDevice.Path = "/dev/wrong-hash"
	if err := Admit(req, acceptingVerifier(t)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("want ErrInvalidRequest for wrong hash device, got %v", err)
	}
}

func TestAdmit_HashRegionSizeOverflow(t *testing.T) {
	// A huge start block makes (start + tree) * block_size exceed MaxInt64.
	req := validRequest(t)
	req.Table.HashStartBlock = 1 << 40
	err := Admit(req, acceptingVerifier(t))
	if !errors.Is(err, ErrHashDeviceTooSmall) {
		t.Fatalf("want ErrHashDeviceTooSmall on size overflow, got %v", err)
	}
}
