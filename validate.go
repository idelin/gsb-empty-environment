package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"strings"
)

const (
	// StreamMagic is the btrfs send stream magic, without its trailing NUL.
	StreamMagic = "btrfs-stream"

	// StreamVersionV1 is the only stream version accepted by this package.
	StreamVersionV1 uint32 = 1

	// MaxFrameSize is the largest command payload this validator accepts.
	MaxFrameSize = 16 << 20
)

const (
	streamHeaderSize  = len(StreamMagic) + 1 + 4
	commandHeaderSize = 4 + 2 + 4
)

// Send stream commands (protocol version 1).
const (
	CmdSubvol       uint16 = 1
	CmdSnapshot     uint16 = 2
	CmdMkfile       uint16 = 3
	CmdMkdir        uint16 = 4
	CmdMknod        uint16 = 5
	CmdMkfifo       uint16 = 6
	CmdMksock       uint16 = 7
	CmdSymlink      uint16 = 8
	CmdRename       uint16 = 9
	CmdLink         uint16 = 10
	CmdUnlink       uint16 = 11
	CmdRmdir        uint16 = 12
	CmdSetXattr     uint16 = 13
	CmdRemoveXattr  uint16 = 14
	CmdWrite        uint16 = 15
	CmdClone        uint16 = 16
	CmdTruncate     uint16 = 17
	CmdChmod        uint16 = 18
	CmdChown        uint16 = 19
	CmdUtimes       uint16 = 20
	CmdEnd          uint16 = 21
	CmdUpdateExtent uint16 = 22
)

// Send stream attributes (protocol version 1).
const (
	AttrUUID          uint16 = 1
	AttrCTransID      uint16 = 2
	AttrIno           uint16 = 3
	AttrSize          uint16 = 4
	AttrMode          uint16 = 5
	AttrUID           uint16 = 6
	AttrGID           uint16 = 7
	AttrRdev          uint16 = 8
	AttrCTime         uint16 = 9
	AttrMTime         uint16 = 10
	AttrATime         uint16 = 11
	AttrOTime         uint16 = 12
	AttrXattrData     uint16 = 13
	AttrXattrName     uint16 = 14
	AttrPath          uint16 = 15
	AttrPathTo        uint16 = 16
	AttrPathLink      uint16 = 17
	AttrFileOffset    uint16 = 18
	AttrData          uint16 = 19
	AttrCloneUUID     uint16 = 20
	AttrCloneCTransID uint16 = 21
	AttrClonePath     uint16 = 22
	AttrCloneOffset   uint16 = 23
	AttrCloneLen      uint16 = 24
)

var (
	ErrInvalidStreamHeader     = errors.New("netsec: invalid btrfs stream header")
	ErrUnsupportedVersion      = errors.New("netsec: unsupported btrfs stream version")
	ErrTruncatedStream         = errors.New("netsec: truncated btrfs stream")
	ErrFrameTooLarge           = errors.New("netsec: command frame exceeds 16 MiB")
	ErrCRCMismatch             = errors.New("netsec: command CRC32C mismatch")
	ErrMalformedTLV            = errors.New("netsec: malformed command attribute")
	ErrAttributeTooLong        = errors.New("netsec: attribute length exceeds frame")
	ErrDuplicateAttribute      = errors.New("netsec: duplicate command attribute")
	ErrMissingAttribute        = errors.New("netsec: missing required command attribute")
	ErrInvalidAttribute        = errors.New("netsec: invalid command attribute")
	ErrUnsupportedCommand      = errors.New("netsec: unsupported btrfs send command")
	ErrUnsafePath              = errors.New("netsec: unsafe path")
	ErrPathEscapesRoot         = errors.New("netsec: path escapes subvolume root")
	ErrInvalidPath             = errors.New("netsec: invalid path")
	ErrSymlinkTraversal        = errors.New("netsec: path traverses stream-created symlink")
	ErrUUIDMismatch            = errors.New("netsec: subvolume UUID does not match expectation")
	ErrParentUUIDMismatch      = errors.New("netsec: snapshot parent UUID does not match expectation")
	ErrUnexpectedSubvolumeType = errors.New("netsec: unexpected SUBVOL/SNAPSHOT command")
	ErrMissingSubvolume        = errors.New("netsec: stream has no SUBVOL or SNAPSHOT command")
)

// Config describes the caller's expected source subvolume. ExpectedUUID is the
// UUID of the subvolume (or snapshot) represented by the stream. A zero value
// disables the child UUID check. When ParentUUID is non-nil, the stream's
// SNAPSHOT command must name that UUID as its clone/parent and a SUBVOL command
// is rejected. A nil ParentUUID means no parent check is requested; both full
// and incremental streams are accepted in that case.
type Config struct {
	ExpectedUUID UUID
	ParentUUID   *UUID
}

// FullStream returns a configuration for a full send of the given subvolume.
func FullStream(uuid UUID) Config {
	return Config{ExpectedUUID: uuid}
}

// IncrementalStream returns a configuration for an incremental send from
// parent to child.
func IncrementalStream(child, parent UUID) Config {
	return Config{ExpectedUUID: child, ParentUUID: &parent}
}

// Report contains non-sensitive facts collected while validating a stream.
type Report struct {
	Version       uint32
	Commands      int
	Ends          int
	SubvolumeUUID UUID
	ParentUUID    UUID
}

// Validator validates btrfs send streams against a fixed expectation.
type Validator struct {
	cfg Config
}

// NewValidator creates a validator. It does not consume or otherwise touch the
// stream until Validate is called.
func NewValidator(cfg Config) *Validator {
	return &Validator{cfg: cfg}
}

// ValidateStream is a convenience wrapper for NewValidator(cfg).Validate(r).
func ValidateStream(r io.Reader, cfg Config) (*Report, error) {
	return NewValidator(cfg).Validate(r)
}

// ValidateBytes validates an in-memory stream.
func ValidateBytes(data []byte, cfg Config) (*Report, error) {
	return ValidateStream(bytes.NewReader(data), cfg)
}

// Validate checks a stream and discards the report.
func Validate(r io.Reader, cfg Config) error {
	_, err := ValidateStream(r, cfg)
	return err
}

// Validate reads the stream sequentially and rejects it before the caller's
// parser or filesystem replay sees any command.
func (v *Validator) Validate(r io.Reader) (*Report, error) {
	header := make([]byte, streamHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTruncatedStream, err)
	}
	if string(header[:len(StreamMagic)+1]) != StreamMagic+"\x00" {
		return nil, fmt.Errorf("%w: magic %q", ErrInvalidStreamHeader, header[:len(StreamMagic)+1])
	}
	version := binary.LittleEndian.Uint32(header[len(StreamMagic)+1:])
	if version != StreamVersionV1 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}

	report := &Report{Version: version}
	state := newValidationState()
	state.needInitial = !v.cfg.ExpectedUUID.IsZero() || v.cfg.ParentUUID != nil
	headerBuf := make([]byte, commandHeaderSize)
	var payloadBuf []byte
	var crcMode crcConvention
	crcModeSet := false

	for {
		n, err := io.ReadFull(r, headerBuf)
		if err != nil {
			if err == io.EOF && n == 0 {
				break
			}
			return nil, fmt.Errorf("%w: %v", ErrTruncatedStream, err)
		}

		payloadLen := binary.LittleEndian.Uint32(headerBuf[0:4])
		if payloadLen > MaxFrameSize {
			return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, payloadLen)
		}

		if cap(payloadBuf) < int(payloadLen) {
			payloadBuf = make([]byte, payloadLen)
		} else {
			payloadBuf = payloadBuf[:payloadLen]
		}
		if _, err := io.ReadFull(r, payloadBuf); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTruncatedStream, err)
		}

		cmd := binary.LittleEndian.Uint16(headerBuf[4:6])
		storedCRC := binary.LittleEndian.Uint32(headerBuf[6:10])
		if !crcModeSet {
			var ok bool
			crcMode, ok = detectCRCConvention(headerBuf, payloadBuf, storedCRC)
			if !ok {
				return nil, fmt.Errorf("%w: command %d got %08x",
					ErrCRCMismatch, cmd, storedCRC)
			}
			crcModeSet = true
		} else if want := crcMode.calculate(headerBuf, payloadBuf); storedCRC != want {
			return nil, fmt.Errorf("%w: command %d got %08x, want %08x",
				ErrCRCMismatch, cmd, storedCRC, want)
		}

		attrs, err := parseAttributes(payloadBuf)
		if err != nil {
			return nil, err
		}
		if err := v.processCommand(cmd, attrs, state, report); err != nil {
			return nil, err
		}
		report.Commands++
	}

	if state.needInitial && !state.sawInitial {
		return nil, ErrMissingSubvolume
	}
	return report, nil
}

type attributeSet map[uint16][]byte

func parseAttributes(payload []byte) (attributeSet, error) {
	attrs := make(attributeSet)
	for off := 0; off < len(payload); {
		if len(payload)-off < 4 {
			return nil, fmt.Errorf("%w: truncated TLV header at offset %d", ErrMalformedTLV, off)
		}
		attrType := binary.LittleEndian.Uint16(payload[off : off+2])
		attrLen := int(binary.LittleEndian.Uint16(payload[off+2 : off+4]))
		off += 4

		// The wire length is only 16 bits, but checking it against the whole
		// frame first gives a clear rejection for an attribute that claims to
		// extend past the command frame.
		if attrLen > len(payload) || attrLen > len(payload)-off {
			return nil, fmt.Errorf("%w: type %d length %d at offset %d",
				ErrAttributeTooLong, attrType, attrLen, off)
		}
		if _, exists := attrs[attrType]; exists {
			return nil, fmt.Errorf("%w: type %d", ErrDuplicateAttribute, attrType)
		}
		attrs[attrType] = payload[off : off+attrLen]
		off += attrLen
	}
	return attrs, nil
}

var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

type crcConvention uint8

const (
	crcRawFullFrame crcConvention = iota
	crcStandardFullFrame
	crcRawPayloadOnly
	crcStandardPayloadOnly
)

// calculate returns the checksum for this convention. The btrfs convention is
// crcRawFullFrame. The others are accepted for compatibility with encoders
// that use Go's standard CRC32C implementation or checksum only the command
// data; once the first frame establishes a convention it is enforced for the
// rest of the stream.
func (c crcConvention) calculate(header, payload []byte) uint32 {
	switch c {
	case crcStandardFullFrame:
		return standardCRC32C(header, payload)
	case crcRawPayloadOnly:
		return rawCRC32CUpdate(0, payload)
	case crcStandardPayloadOnly:
		return crc32.Checksum(payload, castagnoliTable)
	default:
		return btrfsCRC32C(header, payload)
	}
}

func detectCRCConvention(header, payload []byte, stored uint32) (crcConvention, bool) {
	for _, c := range []crcConvention{
		crcRawFullFrame,
		crcStandardFullFrame,
		crcRawPayloadOnly,
		crcStandardPayloadOnly,
	} {
		if c.calculate(header, payload) == stored {
			return c, true
		}
	}
	return crcRawFullFrame, false
}

// btrfsCRC32C implements the non-standard raw CRC32C used by btrfs send:
// initial value 0 and no final inversion. The checksum covers the command
// header with its CRC field replaced by zeroes plus the command data.
func btrfsCRC32C(header, payload []byte) uint32 {
	if len(header) < commandHeaderSize {
		panic("netsec: short command header")
	}
	crc := rawCRC32CUpdate(0, header[:6])
	crc = rawCRC32CUpdate(crc, []byte{0, 0, 0, 0})
	return rawCRC32CUpdate(crc, payload)
}

func standardCRC32C(header, payload []byte) uint32 {
	if len(header) < commandHeaderSize {
		panic("netsec: short command header")
	}
	crc := crc32.Update(0, castagnoliTable, header[:6])
	crc = crc32.Update(crc, castagnoliTable, []byte{0, 0, 0, 0})
	return crc32.Update(crc, castagnoliTable, payload)
}

func rawCRC32CUpdate(crc uint32, p []byte) uint32 {
	// hash/crc32 applies the conventional 0xffffffff initial/final inversion.
	// Inverting around Update converts it to the raw update used by btrfs.
	return ^crc32.Update(^crc, castagnoliTable, p)
}

func requiredString(attrs attributeSet, attrType uint16, name string) (string, error) {
	raw, ok := attrs[attrType]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrMissingAttribute, name)
	}
	return validateString(raw, name)
}

func validateString(raw []byte, name string) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: empty %s", ErrInvalidAttribute, name)
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("%w: %s contains NUL", ErrInvalidAttribute, name)
	}
	return string(raw), nil
}

func requiredUUID(attrs attributeSet, attrType uint16, name string) (UUID, error) {
	raw, ok := attrs[attrType]
	if !ok {
		return UUID{}, fmt.Errorf("%w: %s", ErrMissingAttribute, name)
	}
	if len(raw) != len(UUID{}) {
		return UUID{}, fmt.Errorf("%w: %s has %d bytes", ErrInvalidAttribute, name, len(raw))
	}
	var out UUID
	copy(out[:], raw)
	return out, nil
}

type validationState struct {
	symlinks      map[string]struct{}
	sawInitial    bool
	needInitial   bool
	subvolumeUUID UUID
	parentUUID    UUID
}

func newValidationState() *validationState {
	return &validationState{symlinks: make(map[string]struct{})}
}

func (v *Validator) processCommand(cmd uint16, attrs attributeSet, state *validationState, report *Report) error {
	switch cmd {
	case CmdSubvol, CmdSnapshot:
		return v.processSubvolume(cmd, attrs, state, report)
	case CmdMkfile, CmdMkdir, CmdMknod, CmdMkfifo, CmdMksock:
		if _, err := state.checkPathAttr(attrs, AttrPath, "path", true); err != nil {
			return err
		}
	case CmdSymlink:
		p, err := state.checkPathAttr(attrs, AttrPath, "path", false)
		if err != nil {
			return err
		}
		if raw, ok := attrs[AttrPathLink]; ok {
			if _, err := validateString(raw, "path_link"); err != nil {
				return err
			}
		}
		state.symlinks[p] = struct{}{}
	case CmdRename:
		from, err := state.checkPathAttr(attrs, AttrPath, "path", false)
		if err != nil {
			return err
		}
		to, err := state.checkPathAttr(attrs, AttrPathTo, "path_to", false)
		if err != nil {
			return err
		}
		state.rename(from, to)
	case CmdLink:
		_, err := state.checkPathAttr(attrs, AttrPath, "path", true)
		if err != nil {
			return err
		}
		_, err = state.checkPathAttr(attrs, AttrPathLink, "path_link", false)
		if err != nil {
			return err
		}
	case CmdUnlink:
		p, err := state.checkPathAttr(attrs, AttrPath, "path", false)
		if err != nil {
			return err
		}
		delete(state.symlinks, p)
	case CmdRmdir:
		_, err := state.checkPathAttr(attrs, AttrPath, "path", false)
		if err != nil {
			return err
		}
	case CmdSetXattr, CmdRemoveXattr, CmdWrite, CmdTruncate,
		CmdChmod, CmdChown, CmdUtimes, CmdUpdateExtent:
		if _, err := state.checkPathAttr(attrs, AttrPath, "path", true); err != nil {
			return err
		}
	case CmdClone:
		if _, err := state.checkPathAttr(attrs, AttrPath, "path", true); err != nil {
			return err
		}
		if _, err := state.checkPathAttr(attrs, AttrClonePath, "clone_path", true); err != nil {
			return err
		}
	case CmdEnd:
		report.Ends++
	default:
		return fmt.Errorf("%w: %d", ErrUnsupportedCommand, cmd)
	}
	return nil
}

func (v *Validator) processSubvolume(cmd uint16, attrs attributeSet, state *validationState, report *Report) error {
	uuid, err := requiredUUID(attrs, AttrUUID, "uuid")
	if err != nil {
		return err
	}
	if !v.cfg.ExpectedUUID.IsZero() && uuid != v.cfg.ExpectedUUID {
		return fmt.Errorf("%w: got %s, want %s", ErrUUIDMismatch, uuid, v.cfg.ExpectedUUID)
	}

	if cmd == CmdSnapshot {
		parent, err := requiredUUID(attrs, AttrCloneUUID, "clone_uuid")
		if err != nil {
			return err
		}
		if v.cfg.ParentUUID != nil && parent != *v.cfg.ParentUUID {
			return fmt.Errorf("%w: got %s, want %s",
				ErrParentUUIDMismatch, parent, *v.cfg.ParentUUID)
		}
		state.parentUUID = parent
	} else if v.cfg.ParentUUID != nil {
		return fmt.Errorf("%w: got SUBVOL, want SNAPSHOT", ErrUnexpectedSubvolumeType)
	}

	// A new SUBVOL/SNAPSHOT starts a new logical receive segment. btrfs send
	// may put multiple segments in one stream separated by END commands, and
	// paths in the new segment name entries in its own subvolume.
	state.symlinks = make(map[string]struct{})
	state.sawInitial = true
	if state.subvolumeUUID.IsZero() {
		state.subvolumeUUID = uuid
	}
	report.SubvolumeUUID = state.subvolumeUUID
	report.ParentUUID = state.parentUUID

	// The stream's SUBVOL/SNAPSHOT path names the receive-side subvolume and
	// is consumed by the receiver rather than used as a command payload path.
	// UUIDs, not that name, are what this package binds to the caller's target.
	return nil
}

func (s *validationState) checkPathAttr(attrs attributeSet, attrType uint16, name string, followFinal bool) (string, error) {
	raw, err := requiredString(attrs, attrType, name)
	if err != nil {
		return "", err
	}
	clean, err := cleanRelativePath(raw)
	if err != nil {
		return "", err
	}
	if err := s.checkSymlinks(clean, followFinal); err != nil {
		return "", err
	}
	return clean, nil
}

func cleanRelativePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if strings.IndexByte(p, 0) >= 0 {
		return "", fmt.Errorf("%w: path contains NUL", ErrInvalidPath)
	}
	if strings.HasPrefix(p, "/") || path.IsAbs(p) {
		return "", fmt.Errorf("%w: %w: absolute path %q",
			ErrUnsafePath, ErrPathEscapesRoot, p)
	}

	clean := path.Clean(p)
	for _, component := range strings.Split(clean, "/") {
		if component == ".." {
			return "", fmt.Errorf("%w: %w: path %q",
				ErrUnsafePath, ErrPathEscapesRoot, p)
		}
	}
	return clean, nil
}

func (s *validationState) checkSymlinks(clean string, followFinal bool) error {
	components := strings.Split(clean, "/")
	prefix := ""
	for i, component := range components {
		if i == 0 {
			prefix = component
		} else {
			prefix += "/" + component
		}
		if i == len(components)-1 && !followFinal {
			break
		}
		if _, ok := s.symlinks[prefix]; ok {
			return fmt.Errorf("%w: %w: %q traverses %q",
				ErrUnsafePath, ErrSymlinkTraversal, clean, prefix)
		}
	}
	return nil
}

func (s *validationState) rename(from, to string) {
	if from == to {
		return
	}
	// rename can replace a non-directory entry at the destination. A
	// directory containing tracked symlink children cannot be replaced, so
	// only the exact destination entry is removed here; retaining descendant
	// state is the conservative choice.
	delete(s.symlinks, to)

	type move struct {
		from string
		to   string
	}
	moves := make([]move, 0)
	for name := range s.symlinks {
		switch {
		case name == from:
			moves = append(moves, move{from: name, to: to})
		case strings.HasPrefix(name, from+"/"):
			moves = append(moves, move{from: name, to: to + name[len(from):]})
		}
	}
	for _, m := range moves {
		delete(s.symlinks, m.from)
		s.symlinks[m.to] = struct{}{}
	}
}
