// Package netsec provides admission checks for device-mapper targets used by
// the mount service.
//
// The verity admission control must run before a dm-verity mapping table is
// loaded. It makes sure that every byte reachable through the data device is
// covered by the verified Merkle tree and that table options which weaken
// verification (ignore_zero_blocks, ignore_corruption) cannot be requested.
package netsec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// On-disk dm-verity superblock constants, see struct dm_verity_sb in
// drivers/md/dm-verity.c. The superblock is 512 bytes, little-endian, and
// starts with the ASCII magic "verity\0\0".
const (
	superblockMagic   = "verity\x00\x00"
	superblockSize    = 512
	supportedVersion  = 1
	hashTypeNormal    = 1
	offVersion        = 8
	offHashType       = 12
	offAlgorithm      = 32
	algorithmFieldLen = 32
	offDataBlockSize  = 64
	offHashBlockSize  = 68
	offDataBlocks     = 72
	offSaltSize       = 80
	offSalt           = 82
	saltFieldLen      = 256
)

// Sentinel errors so callers can classify rejection reasons with errors.Is.
var (
	ErrInvalidRequest       = errors.New("invalid verity admission request")
	ErrInvalidSuperblock    = errors.New("invalid dm-verity superblock")
	ErrUnsupportedAlgorithm = errors.New("unsupported verity hash algorithm")
	ErrTableMismatch        = errors.New("verity table does not match superblock")
	ErrInvalidTable         = errors.New("invalid verity mapping table")
	ErrRootHash             = errors.New("invalid verity root hash")
	ErrForbiddenOption      = errors.New("forbidden verity table option")
	ErrDeviceCapacity       = errors.New("verity device capacity check failed")
	ErrTrailingData         = fmt.Errorf("%w: unverified data past declared blocks", ErrDeviceCapacity)
	ErrDeviceTooSmall       = fmt.Errorf("%w: device smaller than declared blocks", ErrDeviceCapacity)
	ErrHashDeviceTooSmall   = fmt.Errorf("%w: hash device cannot hold hash tree", ErrDeviceCapacity)
	ErrHashTree             = errors.New("verity hash tree computation failed")
	ErrSignature            = errors.New("verity root hash signature verification failed")
)

// digestSize maps the algorithm names accepted by dm-verity to their digest
// length in bytes.
var digestSize = map[string]int{
	"sha1":   20,
	"sha224": 28,
	"sha256": 32,
	"sha384": 48,
	"sha512": 64,
}

// forbiddenOptions disable verification for reads and must never be accepted.
// restart_on_corruption is intentionally absent: it keeps verification
// enabled and only changes the kernel's reaction to a detected corruption.
var forbiddenOptions = map[string]struct{}{
	"ignore_zero_blocks": {},
	"ignore_corruption":  {},
}

// Superblock holds the security-relevant fields of a version 1 dm-verity
// on-disk superblock.
type Superblock struct {
	Version       uint32
	HashType      uint32
	Algorithm     string
	DataBlockSize uint32
	HashBlockSize uint32
	DataBlocks    uint64
	SaltSize      uint16
	Salt          []byte
}

// Device describes a block device referenced by a verity table together with
// its observed capacity in bytes.
type Device struct {
	Path string
	Size int64
}

// Table is a parsed dm-verity target table line:
//
//	<version> <data_dev> <hash_dev> <data_block_size> <hash_block_size>
//	<num_data_blocks> <hash_start_block> <algorithm> <root_digest> <salt>
//	[<#opt_args> <arg>...]
type Table struct {
	Version        uint32
	DataDevice     Device
	HashDevice     Device
	DataBlockSize  uint32
	HashBlockSize  uint32
	NumDataBlocks  uint64
	HashStartBlock uint64
	Algorithm      string
	RootHash       []byte
	Salt           []byte
	Options        []string
}

// AdmissionRequest bundles everything Admit inspects. Superblock is the raw
// superblock read from the hash device, Signature is the detached root hash
// signature supplied alongside the table.
type AdmissionRequest struct {
	Superblock []byte
	Table      *Table
	DataDevice Device
	HashDevice Device
	Signature  []byte
}

// SignatureVerifier is injected by the caller. Admit never performs key
// management itself; it delegates root hash signature validation to the
// mount service's trusted verifier.
type SignatureVerifier func(algorithm string, rootHash, signature []byte) error

// ParseSuperblock decodes a dm-verity superblock from its on-disk bytes.
func ParseSuperblock(block []byte) (*Superblock, error) {
	if len(block) < superblockSize {
		return nil, fmt.Errorf("%w: need %d bytes, got %d", ErrInvalidSuperblock, superblockSize, len(block))
	}
	if string(block[:len(superblockMagic)]) != superblockMagic {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalidSuperblock)
	}

	version := binary.LittleEndian.Uint32(block[offVersion:])
	if version != supportedVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidSuperblock, version)
	}
	hashType := binary.LittleEndian.Uint32(block[offHashType:])
	if hashType != hashTypeNormal {
		return nil, fmt.Errorf("%w: unsupported hash type %d", ErrInvalidSuperblock, hashType)
	}

	saltSize := binary.LittleEndian.Uint16(block[offSaltSize:])
	if offSalt+int(saltSize) > len(block) {
		return nil, fmt.Errorf("%w: salt exceeds superblock", ErrInvalidSuperblock)
	}

	sb := &Superblock{
		Version:       version,
		HashType:      hashType,
		Algorithm:     cString(block[offAlgorithm : offAlgorithm+algorithmFieldLen]),
		DataBlockSize: binary.LittleEndian.Uint32(block[offDataBlockSize:]),
		HashBlockSize: binary.LittleEndian.Uint32(block[offHashBlockSize:]),
		DataBlocks:    binary.LittleEndian.Uint64(block[offDataBlocks:]),
		SaltSize:      saltSize,
		Salt:          append([]byte(nil), block[offSalt:offSalt+int(saltSize)]...),
	}
	if sb.Algorithm == "" || sb.DataBlockSize == 0 || sb.HashBlockSize == 0 || sb.DataBlocks == 0 {
		return nil, fmt.Errorf("%w: zero-valued required field", ErrInvalidSuperblock)
	}
	return sb, nil
}

// ParseTable parses a dm-verity target table line. Table format version 1 has
// no options; version 2 carries an option count followed by the options.
func ParseTable(line string) (*Table, error) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil, fmt.Errorf("%w: want at least 10 fields, got %d", ErrInvalidTable, len(fields))
	}

	version, err := parseU32(fields[0])
	if err != nil || (version != 1 && version != 2) {
		return nil, fmt.Errorf("%w: unsupported table version %q", ErrInvalidTable, fields[0])
	}
	dataBlockSize, err := parseU32(fields[3])
	if err != nil {
		return nil, fmt.Errorf("%w: data block size: %v", ErrInvalidTable, err)
	}
	hashBlockSize, err := parseU32(fields[4])
	if err != nil {
		return nil, fmt.Errorf("%w: hash block size: %v", ErrInvalidTable, err)
	}
	numDataBlocks, err := parseU64(fields[5])
	if err != nil {
		return nil, fmt.Errorf("%w: data block count: %v", ErrInvalidTable, err)
	}
	hashStartBlock, err := parseU64(fields[6])
	if err != nil {
		return nil, fmt.Errorf("%w: hash start block: %v", ErrInvalidTable, err)
	}
	rootHash, err := parseHex(fields[8])
	if err != nil {
		return nil, fmt.Errorf("%w: root digest: %v", ErrInvalidTable, err)
	}
	salt, err := parseHex(fields[9])
	if err != nil {
		return nil, fmt.Errorf("%w: salt: %v", ErrInvalidTable, err)
	}

	table := &Table{
		Version:        version,
		DataDevice:     Device{Path: fields[1]},
		HashDevice:     Device{Path: fields[2]},
		DataBlockSize:  dataBlockSize,
		HashBlockSize:  hashBlockSize,
		NumDataBlocks:  numDataBlocks,
		HashStartBlock: hashStartBlock,
		Algorithm:      fields[7],
		RootHash:       rootHash,
		Salt:           salt,
	}

	if version == 1 {
		if len(fields) != 10 {
			return nil, fmt.Errorf("%w: version 1 table must have exactly 10 fields", ErrInvalidTable)
		}
		return table, nil
	}

	if len(fields) < 11 {
		return nil, fmt.Errorf("%w: version 2 table missing option count", ErrInvalidTable)
	}
	optionCount, err := strconv.Atoi(fields[10])
	if err != nil || optionCount < 0 {
		return nil, fmt.Errorf("%w: invalid option count %q", ErrInvalidTable, fields[10])
	}
	if len(fields) != 11+optionCount {
		return nil, fmt.Errorf("%w: declared %d options but found %d", ErrInvalidTable, optionCount, len(fields)-11)
	}
	table.Options = append(table.Options, fields[11:]...)
	return table, nil
}

// Admit decides whether a dm-verity mapping table may be loaded. It returns
// nil only when all of the following hold:
//
//   - the superblock is well-formed and its algorithm, block sizes and data
//     block count match the table field by field;
//   - the data device has exactly data_blocks * data_block_size bytes, so no
//     unverified tail can be read;
//   - the hash device is large enough for the superblock region plus the
//     Merkle tree sized layer by layer from the data blocks;
//   - the root hash length equals the algorithm digest size;
//   - no verification-weakening option is present;
//   - the caller-injected signature verifier accepts the root hash signature.
func Admit(req *AdmissionRequest, verify SignatureVerifier) error {
	if req == nil || req.Table == nil {
		return fmt.Errorf("%w: nil request or table", ErrInvalidRequest)
	}
	table := req.Table

	sb, err := ParseSuperblock(req.Superblock)
	if err != nil {
		return err
	}

	if table.Algorithm != sb.Algorithm {
		return fmt.Errorf("%w: algorithm table=%q superblock=%q", ErrTableMismatch, table.Algorithm, sb.Algorithm)
	}
	if table.DataBlockSize != sb.DataBlockSize {
		return fmt.Errorf("%w: data block size table=%d superblock=%d", ErrTableMismatch, table.DataBlockSize, sb.DataBlockSize)
	}
	if table.HashBlockSize != sb.HashBlockSize {
		return fmt.Errorf("%w: hash block size table=%d superblock=%d", ErrTableMismatch, table.HashBlockSize, sb.HashBlockSize)
	}
	if table.NumDataBlocks != sb.DataBlocks {
		return fmt.Errorf("%w: data block count table=%d superblock=%d", ErrTableMismatch, table.NumDataBlocks, sb.DataBlocks)
	}
	if !isPowerOfTwo(table.DataBlockSize) || !isPowerOfTwo(table.HashBlockSize) {
		return fmt.Errorf("%w: block sizes must be powers of two", ErrInvalidTable)
	}

	digest, ok := digestSize[sb.Algorithm]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, sb.Algorithm)
	}
	if len(table.RootHash) != digest {
		return fmt.Errorf("%w: length %d, %s digest is %d bytes", ErrRootHash, len(table.RootHash), sb.Algorithm, digest)
	}

	for _, option := range table.Options {
		if _, forbidden := forbiddenOptions[option]; forbidden {
			return fmt.Errorf("%w: %s", ErrForbiddenOption, option)
		}
	}

	if err := checkDataDevice(req, table); err != nil {
		return err
	}
	if err := checkHashDevice(req, table, digest); err != nil {
		return err
	}

	if verify == nil {
		return fmt.Errorf("%w: no signature verifier injected", ErrSignature)
	}
	if err := verify(sb.Algorithm, table.RootHash, req.Signature); err != nil {
		return errors.Join(ErrSignature, err)
	}
	return nil
}

// HashTreeBlocks returns the number of hash blocks occupied by the Merkle
// tree above dataBlocks. Each layer stores one digest per block of the layer
// below, so layer N+1 has ceil(layerN * digestSize / hashBlockSize) blocks;
// the layers stop once a single root block remains.
func HashTreeBlocks(dataBlocks uint64, digestSize int, hashBlockSize uint32) (uint64, error) {
	if dataBlocks == 0 || digestSize <= 0 || hashBlockSize == 0 {
		return 0, fmt.Errorf("%w: zero input", ErrHashTree)
	}
	return hashTreeBlocks(dataBlocks, uint64(digestSize), uint64(hashBlockSize))
}

func checkDataDevice(req *AdmissionRequest, table *Table) error {
	if req.DataDevice.Path != "" && table.DataDevice.Path != "" && req.DataDevice.Path != table.DataDevice.Path {
		return fmt.Errorf("%w: measured device %q is not table data device %q", ErrInvalidRequest, req.DataDevice.Path, table.DataDevice.Path)
	}
	if req.DataDevice.Size < 0 {
		return fmt.Errorf("%w: negative data device size", ErrDeviceCapacity)
	}
	dataBytes, ok := mul64(table.NumDataBlocks, uint64(table.DataBlockSize))
	if !ok || dataBytes > math.MaxInt64 {
		return fmt.Errorf("%w: data region size overflows", ErrDeviceCapacity)
	}
	deviceSize := uint64(req.DataDevice.Size)
	switch {
	case deviceSize < dataBytes:
		return fmt.Errorf("%w: device %d bytes, declared region needs %d", ErrDeviceTooSmall, deviceSize, dataBytes)
	case deviceSize > dataBytes:
		return fmt.Errorf("%w: device %d bytes, verified region ends at %d", ErrTrailingData, deviceSize, dataBytes)
	}
	return nil
}

func checkHashDevice(req *AdmissionRequest, table *Table, digest int) error {
	if req.HashDevice.Path != "" && table.HashDevice.Path != "" && req.HashDevice.Path != table.HashDevice.Path {
		return fmt.Errorf("%w: measured device %q is not table hash device %q", ErrInvalidRequest, req.HashDevice.Path, table.HashDevice.Path)
	}
	if req.HashDevice.Size < 0 {
		return fmt.Errorf("%w: negative hash device size", ErrDeviceCapacity)
	}

	treeBlocks, err := hashTreeBlocks(table.NumDataBlocks, uint64(digest), uint64(table.HashBlockSize))
	if err != nil {
		return err
	}
	totalHashBlocks, ok := add64(table.HashStartBlock, treeBlocks)
	if !ok {
		return fmt.Errorf("%w: hash region block range overflows", ErrHashDeviceTooSmall)
	}
	requiredBytes, ok := mul64(totalHashBlocks, uint64(table.HashBlockSize))
	if !ok || requiredBytes > math.MaxInt64 {
		return fmt.Errorf("%w: hash region size overflows", ErrHashDeviceTooSmall)
	}
	if uint64(req.HashDevice.Size) < requiredBytes {
		return fmt.Errorf("%w: device %d bytes, needs %d (%d tree blocks after start block %d)",
			ErrHashDeviceTooSmall, uint64(req.HashDevice.Size), requiredBytes, treeBlocks, table.HashStartBlock)
	}
	return nil
}

// hashTreeBlocks performs the layer-by-layer Merkle tree sizing with checked
// arithmetic so adversarial superblock values cannot wrap the counters.
func hashTreeBlocks(dataBlocks, digestBytes, hashBlockSize uint64) (uint64, error) {
	var total uint64
	layer := dataBlocks
	for layer > 1 {
		hashes, ok := mul64(layer, digestBytes)
		if !ok {
			return 0, fmt.Errorf("%w: layer size overflows", ErrHashTree)
		}
		blocks := (hashes + hashBlockSize - 1) / hashBlockSize
		total, ok = add64(total, blocks)
		if !ok {
			return 0, fmt.Errorf("%w: tree size overflows", ErrHashTree)
		}
		layer = blocks
	}
	return total, nil
}

func mul64(a, b uint64) (uint64, bool) {
	product := a * b
	if a != 0 && product/a != b {
		return 0, false
	}
	return product, true
}

func add64(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}

func isPowerOfTwo(v uint32) bool {
	return v != 0 && v&(v-1) == 0
}

func parseU32(s string) (uint32, error) {
	value, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(value), nil
}

func parseU64(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}

// parseHex accepts lowercase, uppercase and empty ("-"/"") hex byte strings.
func parseHex(s string) ([]byte, error) {
	if s == "-" || s == "" {
		return nil, nil
	}
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex string %q", s)
	}
	out := make([]byte, hex.DecodedLen(len(s)))
	if _, err := hex.Decode(out, []byte(s)); err != nil {
		return nil, err
	}
	return out, nil
}

func cString(b []byte) string {
	if end := bytes.IndexByte(b, 0); end >= 0 {
		b = b[:end]
	}
	return string(b)
}
