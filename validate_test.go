package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

var (
	childUUID  = MustParseUUID("11111111-2222-3333-4444-555555555555")
	parentUUID = MustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
)

type testAttr struct {
	typ  uint16
	data []byte
}

type testCommand struct {
	cmd   uint16
	attrs []testAttr
}

func stringAttr(typ uint16, value string) testAttr {
	return testAttr{typ: typ, data: []byte(value)}
}

func u64Attr(typ uint16, value uint64) testAttr {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, value)
	return testAttr{typ: typ, data: data}
}

func uuidAttr(typ uint16, value UUID) testAttr {
	return testAttr{typ: typ, data: value[:]}
}

func subvolCommand(uuid UUID) testCommand {
	return testCommand{cmd: CmdSubvol, attrs: []testAttr{
		stringAttr(AttrPath, "subvol"),
		uuidAttr(AttrUUID, uuid),
		u64Attr(AttrCTransID, 1),
	}}
}

func snapshotCommand(uuid, parent UUID) testCommand {
	return testCommand{cmd: CmdSnapshot, attrs: []testAttr{
		stringAttr(AttrPath, "snapshot"),
		uuidAttr(AttrUUID, uuid),
		u64Attr(AttrCTransID, 2),
		uuidAttr(AttrCloneUUID, parent),
		u64Attr(AttrCloneCTransID, 1),
	}}
}

func encodePayload(attrs []testAttr) []byte {
	var payload []byte
	for _, attr := range attrs {
		payload = append(payload,
			byte(attr.typ), byte(attr.typ>>8),
			byte(len(attr.data)), byte(len(attr.data)>>8))
		payload = append(payload, attr.data...)
	}
	return payload
}

func buildFrame(cmd uint16, payload []byte) []byte {
	return buildFrameConvention(crcRawFullFrame, cmd, payload)
}

func buildFrameConvention(convention crcConvention, cmd uint16, payload []byte) []byte {
	header := make([]byte, commandHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint16(header[4:6], cmd)
	crc := convention.calculate(header, payload)
	binary.LittleEndian.PutUint32(header[6:10], crc)
	return append(header, payload...)
}

func buildStream(commands ...testCommand) []byte {
	return buildStreamConvention(crcRawFullFrame, commands...)
}

func buildStreamConvention(convention crcConvention, commands ...testCommand) []byte {
	stream := []byte(StreamMagic)
	stream = append(stream, 0)
	version := make([]byte, 4)
	binary.LittleEndian.PutUint32(version, StreamVersionV1)
	stream = append(stream, version...)
	for _, command := range commands {
		stream = append(stream, buildFrameConvention(convention, command.cmd, encodePayload(command.attrs))...)
	}
	return stream
}

func validFullStream() []byte {
	return buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdMkdir, attrs: []testAttr{
			stringAttr(AttrPath, "dir"),
			u64Attr(AttrIno, 1),
		}},
		testCommand{cmd: CmdMkfile, attrs: []testAttr{
			stringAttr(AttrPath, "dir/file"),
			u64Attr(AttrIno, 2),
		}},
		testCommand{cmd: CmdEnd},
	)
}

func TestValidateFullStream(t *testing.T) {
	report, err := ValidateStream(bytes.NewReader(validFullStream()), FullStream(childUUID))
	if err != nil {
		t.Fatalf("valid full stream rejected: %v", err)
	}
	if report.Commands != 4 || report.Ends != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.SubvolumeUUID != childUUID {
		t.Fatalf("subvolume UUID = %s, want %s", report.SubvolumeUUID, childUUID)
	}
}

func TestRejectsParentTraversal(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdMkfile, attrs: []testAttr{
			stringAttr(AttrPath, "../outside"),
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestRejectsAbsolutePath(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdMkdir, attrs: []testAttr{
			stringAttr(AttrPath, "/etc/evil"),
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("expected absolute path rejection, got %v", err)
	}
}

func TestRejectsWriteThroughStreamSymlink(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "escape"),
			stringAttr(AttrPathLink, "/etc"),
		}},
		testCommand{cmd: CmdWrite, attrs: []testAttr{
			stringAttr(AttrPath, "escape/passwd"),
			u64Attr(AttrFileOffset, 0),
			{typ: AttrData, data: []byte("owned")},
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrSymlinkTraversal) {
		t.Fatalf("expected symlink traversal rejection, got %v", err)
	}
}

func TestRejectsDirectWriteThroughStreamSymlink(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "escape"),
			stringAttr(AttrPathLink, "/etc/passwd"),
		}},
		testCommand{cmd: CmdWrite, attrs: []testAttr{
			stringAttr(AttrPath, "escape"),
			u64Attr(AttrFileOffset, 0),
			{typ: AttrData, data: []byte("owned")},
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrSymlinkTraversal) {
		t.Fatalf("expected direct symlink write rejection, got %v", err)
	}
}

func TestAllowsAbsoluteSymlinkTargetWithoutFollowingIt(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "escape"),
			stringAttr(AttrPathLink, "/etc"),
		}},
	)
	if _, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID)); err != nil {
		t.Fatalf("symlink data itself rejected: %v", err)
	}
}

func TestTracksRenamedSymlink(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "escape"),
			stringAttr(AttrPathLink, "/etc"),
		}},
		testCommand{cmd: CmdRename, attrs: []testAttr{
			stringAttr(AttrPath, "escape"),
			stringAttr(AttrPathTo, "moved"),
		}},
		testCommand{cmd: CmdWrite, attrs: []testAttr{
			stringAttr(AttrPath, "moved/passwd"),
			{typ: AttrData, data: []byte("owned")},
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrSymlinkTraversal) {
		t.Fatalf("expected renamed symlink traversal rejection, got %v", err)
	}
}

func TestTracksSymlinkThroughDirectoryRename(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdMkdir, attrs: []testAttr{
			stringAttr(AttrPath, "dir"),
		}},
		testCommand{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "dir/escape"),
			stringAttr(AttrPathLink, "/etc"),
		}},
		testCommand{cmd: CmdRename, attrs: []testAttr{
			stringAttr(AttrPath, "dir"),
			stringAttr(AttrPathTo, "moved"),
		}},
		testCommand{cmd: CmdWrite, attrs: []testAttr{
			stringAttr(AttrPath, "moved/escape/passwd"),
			{typ: AttrData, data: []byte("owned")},
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrSymlinkTraversal) {
		t.Fatalf("expected renamed directory traversal rejection, got %v", err)
	}
}

func TestAllowsUnlinkedSymlinkPath(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "old"),
			stringAttr(AttrPathLink, "/etc"),
		}},
		testCommand{cmd: CmdUnlink, attrs: []testAttr{
			stringAttr(AttrPath, "old"),
		}},
		testCommand{cmd: CmdMkfile, attrs: []testAttr{
			stringAttr(AttrPath, "old"),
		}},
	)
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if err != nil {
		t.Fatalf("stream after unlinking symlink rejected: %v", err)
	}
}

func TestRejectsCRCMismatch(t *testing.T) {
	stream := validFullStream()
	stream[len(stream)-1] ^= 0x01
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("expected CRC rejection, got %v", err)
	}
}

func TestAcceptsCompatibleCRCEncodings(t *testing.T) {
	commands := []testCommand{
		subvolCommand(childUUID),
		{cmd: CmdMkfile, attrs: []testAttr{stringAttr(AttrPath, "file")}},
		{cmd: CmdEnd},
	}
	for _, convention := range []crcConvention{
		crcRawFullFrame,
		crcStandardFullFrame,
		crcRawPayloadOnly,
		crcStandardPayloadOnly,
	} {
		stream := buildStreamConvention(convention, commands...)
		if _, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID)); err != nil {
			t.Fatalf("CRC convention %d rejected: %v", convention, err)
		}
	}
}

func TestAllowsRootMetadataPath(t *testing.T) {
	stream := buildStream(
		subvolCommand(childUUID),
		testCommand{cmd: CmdUtimes, attrs: []testAttr{
			stringAttr(AttrPath, "."),
		}},
	)
	if _, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID)); err != nil {
		t.Fatalf("root metadata path rejected: %v", err)
	}
}

func TestRejectsAttributeLongerThanFrame(t *testing.T) {
	payload := []byte{
		byte(AttrPath), 0,
		0xff, 0xff, // attribute claims 65535 bytes
		'a', 'b',
	}
	stream := []byte(StreamMagic)
	stream = append(stream, 0)
	version := make([]byte, 4)
	binary.LittleEndian.PutUint32(version, StreamVersionV1)
	stream = append(stream, version...)
	stream = append(stream, buildFrame(CmdMkfile, payload)...)

	_, err := ValidateStream(bytes.NewReader(stream), Config{})
	if !errors.Is(err, ErrAttributeTooLong) {
		t.Fatalf("expected oversized attribute rejection, got %v", err)
	}
}

func TestRejectsOversizedFrame(t *testing.T) {
	stream := []byte(StreamMagic)
	stream = append(stream, 0)
	version := make([]byte, 4)
	binary.LittleEndian.PutUint32(version, StreamVersionV1)
	stream = append(stream, version...)

	header := make([]byte, commandHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], MaxFrameSize+1)
	binary.LittleEndian.PutUint16(header[4:6], CmdMkfile)
	stream = append(stream, header...)

	_, err := ValidateStream(bytes.NewReader(stream), Config{})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected frame size rejection, got %v", err)
	}
}

func validIncrementalCommands() []testCommand {
	return []testCommand{
		snapshotCommand(childUUID, parentUUID),
		{cmd: CmdMkdir, attrs: []testAttr{
			stringAttr(AttrPath, "dir"),
			u64Attr(AttrIno, 1),
		}},
		{cmd: CmdMkfile, attrs: []testAttr{
			stringAttr(AttrPath, "dir/file"),
			u64Attr(AttrIno, 2),
		}},
		{cmd: CmdWrite, attrs: []testAttr{
			stringAttr(AttrPath, "dir/file"),
			u64Attr(AttrFileOffset, 0),
			{typ: AttrData, data: []byte("hello")},
		}},
		{cmd: CmdClone, attrs: []testAttr{
			stringAttr(AttrPath, "dir/copy"),
			uuidAttr(AttrCloneUUID, parentUUID),
			u64Attr(AttrCloneCTransID, 1),
			stringAttr(AttrClonePath, "dir/file"),
			u64Attr(AttrCloneOffset, 0),
			u64Attr(AttrCloneLen, 5),
		}},
		{cmd: CmdSymlink, attrs: []testAttr{
			stringAttr(AttrPath, "dir/link"),
			stringAttr(AttrPathLink, "file"),
		}},
		{cmd: CmdRename, attrs: []testAttr{
			stringAttr(AttrPath, "dir/file"),
			stringAttr(AttrPathTo, "dir/renamed"),
		}},
		{cmd: CmdLink, attrs: []testAttr{
			stringAttr(AttrPath, "dir/renamed"),
			stringAttr(AttrPathLink, "dir/hardlink"),
		}},
		{cmd: CmdUnlink, attrs: []testAttr{
			stringAttr(AttrPath, "dir/hardlink"),
		}},
		{cmd: CmdEnd},
	}
}

func TestValidIncrementalStream(t *testing.T) {
	stream := buildStream(validIncrementalCommands()...)
	report, err := ValidateStream(bytes.NewReader(stream), IncrementalStream(childUUID, parentUUID))
	if err != nil {
		t.Fatalf("valid incremental stream rejected: %v", err)
	}
	if report.SubvolumeUUID != childUUID {
		t.Fatalf("subvolume UUID = %s, want %s", report.SubvolumeUUID, childUUID)
	}
	if report.ParentUUID != parentUUID {
		t.Fatalf("parent UUID = %s, want %s", report.ParentUUID, parentUUID)
	}
}

func TestRejectsUnexpectedSnapshotParent(t *testing.T) {
	otherParent := MustParseUUID("00000000-0000-0000-0000-000000000001")
	stream := buildStream(snapshotCommand(childUUID, otherParent))
	_, err := ValidateStream(bytes.NewReader(stream), IncrementalStream(childUUID, parentUUID))
	if !errors.Is(err, ErrParentUUIDMismatch) {
		t.Fatalf("expected parent mismatch, got %v", err)
	}
}

func TestRejectsUnexpectedChildUUID(t *testing.T) {
	otherChild := MustParseUUID("00000000-0000-0000-0000-000000000002")
	stream := buildStream(subvolCommand(otherChild))
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrUUIDMismatch) {
		t.Fatalf("expected UUID mismatch, got %v", err)
	}
}

func TestRejectsSubvolForIncrementalExpectation(t *testing.T) {
	stream := buildStream(subvolCommand(childUUID))
	_, err := ValidateStream(bytes.NewReader(stream), IncrementalStream(childUUID, parentUUID))
	if !errors.Is(err, ErrUnexpectedSubvolumeType) {
		t.Fatalf("expected SUBVOL rejection, got %v", err)
	}
}

func TestRejectsMissingInitialSubvolume(t *testing.T) {
	stream := buildStream(testCommand{cmd: CmdMkfile, attrs: []testAttr{
		stringAttr(AttrPath, "file"),
	}})
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrMissingSubvolume) {
		t.Fatalf("expected missing subvolume rejection, got %v", err)
	}
}

func TestRejectsBadStreamHeaderAndVersion(t *testing.T) {
	badMagic := append([]byte("btrfs-strean"), 0, 1, 0, 0, 0)
	if _, err := ValidateStream(bytes.NewReader(badMagic), Config{}); !errors.Is(err, ErrInvalidStreamHeader) {
		t.Fatalf("expected magic rejection, got %v", err)
	}

	badVersion := append([]byte(StreamMagic), 0, 2, 0, 0, 0)
	if _, err := ValidateStream(bytes.NewReader(badVersion), Config{}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected version rejection, got %v", err)
	}
}

func TestRejectsTruncatedFrame(t *testing.T) {
	stream := buildStream(subvolCommand(childUUID))
	stream = stream[:len(stream)-1]
	_, err := ValidateStream(bytes.NewReader(stream), FullStream(childUUID))
	if !errors.Is(err, ErrTruncatedStream) {
		t.Fatalf("expected truncation rejection, got %v", err)
	}
}

func TestUUIDRoundTrip(t *testing.T) {
	if got := childUUID.String(); got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("UUID string = %q", got)
	}
	parsed, err := ParseUUID("11111111-2222-3333-4444-555555555555")
	if err != nil || parsed != childUUID {
		t.Fatalf("UUID round trip failed: %s, %v", parsed, err)
	}
}

func TestRawCRC32CCompatibility(t *testing.T) {
	data := []byte("123456789")
	want := ^crc32.Checksum(data, castagnoliTable)
	if got := rawCRC32CUpdate(0xffffffff, data); got != want {
		t.Fatalf("raw CRC32C = %08x, want %08x", got, want)
	}
}

func TestValidateReaderConvenience(t *testing.T) {
	if err := Validate(bytes.NewReader(validFullStream()), FullStream(childUUID)); err != nil {
		t.Fatalf("Validate rejected valid stream: %v", err)
	}
	if err := Validate(bytes.NewReader([]byte{}), Config{}); !errors.Is(err, ErrTruncatedStream) {
		t.Fatalf("expected empty stream rejection, got %v", err)
	}
}

func TestValidatorDoesNotMutateConfig(t *testing.T) {
	cfg := IncrementalStream(childUUID, parentUUID)
	validator := NewValidator(cfg)
	stream := buildStream(validIncrementalCommands()...)
	if _, err := validator.Validate(bytes.NewReader(stream)); err != nil {
		t.Fatalf("validator rejected valid stream: %v", err)
	}
	if cfg.ParentUUID == nil || *cfg.ParentUUID != parentUUID {
		t.Fatalf("configuration mutated: %+v", cfg)
	}
}
