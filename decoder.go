package netsec

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	localHeaderSignature   = 0x04034b50
	centralHeaderSignature = 0x02014b50
	endSignature           = 0x06054b50
	zip64EndSignature      = 0x06064b50
	zip64LocatorSignature  = 0x07064b50
	zip64ExtraID           = 0x0001
)

// Entry describes one ZIP entry relevant to path adjudication.
type Entry struct {
	Name           string
	LocalName      string
	NamePath       string
	Path           string
	FullPath       string
	UnicodePath    string
	LinkTarget     string
	TargetPath     string
	HasLinkTarget  bool
	IsSymlink      bool
	CreatorVersion uint16
	ExternalAttrs  uint32
	Extra          []byte
	LocalExtra     []byte
}

// EffectiveName is the Unicode path when one is present, otherwise the name
// from the central directory.
func (e Entry) EffectiveName() string {
	if e.UnicodePath != "" {
		return e.UnicodePath
	}
	return e.Name
}

// Archive is the validated entry list returned by Decode.
type Archive []Entry

// Entries returns the archive's entries.
func (a Archive) Entries() []Entry {
	return []Entry(a)
}

// Paths returns the normalized root-relative entry paths.
func (a Archive) Paths() []string {
	paths := make([]string, 0, len(a))
	for _, entry := range a {
		paths = append(paths, entry.Path)
	}
	return paths
}

// Decoder reads ZIP data and rejects paths that are not root-relative. Root is
// used to turn validated relative paths into absolute paths.
type Decoder struct {
	Root string
}

// NewDecoder returns a decoder rooted at root.
func NewDecoder(root string) *Decoder {
	return &Decoder{Root: root}
}

// Decode accepts a path, []byte, io.Reader, io.ReaderAt, *os.File,
// *zip.Reader, or *zip.ReadCloser source.
func Decode(src any) (Archive, error) {
	return NewDecoder("").Decode(src)
}

// DecodeArchive is an alias for Decode.
func DecodeArchive(src any) (Archive, error) {
	return Decode(src)
}

// Decode is the method form of the package-level Decode.
func (d *Decoder) Decode(src any) (Archive, error) {
	switch value := src.(type) {
	case []byte:
		return d.decodeBytes(value)
	case string:
		return d.DecodeFile(value)
	case *zip.Reader:
		return d.decodeZipReader(value)
	case *zip.ReadCloser:
		return d.decodeZipReader(&value.Reader)
	case io.Reader:
		if readerAt, ok := value.(io.ReaderAt); ok {
			if statter, ok := readerAt.(interface {
				Stat() (os.FileInfo, error)
			}); ok {
				info, err := statter.Stat()
				if err != nil {
					return nil, err
				}
				return d.decodeReaderAt(readerAt, info.Size())
			}
		}
		return d.decodeReader(value)
	case io.ReaderAt:
		if statter, ok := value.(interface {
			Stat() (os.FileInfo, error)
		}); ok {
			info, err := statter.Stat()
			if err != nil {
				return nil, err
			}
			return d.decodeReaderAt(value, info.Size())
		}
		return nil, fmt.Errorf("%w: io.ReaderAt source needs a size; use DecodeReaderAt", ErrInvalidArchive)
	case nil:
		return nil, fmt.Errorf("%w: nil source", ErrInvalidArchive)
	default:
		return nil, fmt.Errorf("%w: unsupported source %T", ErrInvalidArchive, src)
	}
}

// DecodeBytes decodes an in-memory ZIP archive.
func DecodeBytes(data []byte) (Archive, error) {
	return NewDecoder("").decodeBytes(data)
}

// DecodeReader decodes a ZIP archive from any reader.
func DecodeReader(r io.Reader) (Archive, error) {
	return NewDecoder("").decodeReader(r)
}

// DecodeReaderAt decodes a ZIP archive from a sized reader.
func DecodeReaderAt(r io.ReaderAt, size int64) (Archive, error) {
	return NewDecoder("").decodeReaderAt(r, size)
}

// DecodeFile decodes a ZIP archive from path.
func DecodeFile(path string) (Archive, error) {
	return NewDecoder("").DecodeFile(path)
}

// Open is an alias for DecodeFile.
func Open(path string) (Archive, error) {
	return DecodeFile(path)
}

// DecodeFile decodes a ZIP archive from path using the receiver's root.
func (d *Decoder) DecodeFile(path string) (Archive, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return d.decodeReaderAt(file, info.Size())
}

// Validate accepts the same source types as Decode and returns only an error.
func Validate(src any) error {
	_, err := Decode(src)
	return err
}

// ValidateArchive is an alias for Validate.
func ValidateArchive(src any) error {
	return Validate(src)
}

// ValidateBytes validates an in-memory archive.
func ValidateBytes(data []byte) error {
	return Validate(data)
}

// ValidateFile validates a ZIP archive on disk.
func ValidateFile(path string) error {
	return NewDecoder("").ValidateFile(path)
}

// Check is an alias for Validate.
func Check(src any) error {
	return Validate(src)
}

// Validate accepts the same source types as Decode using the receiver's root.
func (d *Decoder) Validate(src any) error {
	_, err := d.Decode(src)
	return err
}

// ValidateFile validates a ZIP archive on disk using the receiver's root.
func (d *Decoder) ValidateFile(path string) error {
	_, err := d.DecodeFile(path)
	return err
}

// ValidateReaderAt validates a sized ZIP source.
func ValidateReaderAt(r io.ReaderAt, size int64) error {
	_, err := DecodeReaderAt(r, size)
	return err
}

func (d *Decoder) decodeReader(r io.Reader) (Archive, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return d.decodeBytes(data)
}

func (d *Decoder) decodeReaderAt(r io.ReaderAt, size int64) (Archive, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: negative size", ErrInvalidArchive)
	}
	data, err := io.ReadAll(io.NewSectionReader(r, 0, size))
	if err != nil {
		return nil, err
	}
	return d.decodeBytes(data)
}

func (d *Decoder) decodeBytes(data []byte) (Archive, error) {
	entries, err := parseDirectory(data)
	if err != nil {
		return nil, err
	}
	return d.adjudicate(entries)
}

func (d *Decoder) decodeZipReader(reader *zip.Reader) (Archive, error) {
	raw := make([]rawEntry, 0, len(reader.File))
	for _, file := range reader.File {
		raw = append(raw, rawEntry{
			centralName:    []byte(file.Name),
			localName:      []byte(file.Name),
			centralExtra:   append([]byte(nil), file.Extra...),
			externalAttrs:  file.ExternalAttrs,
			creatorVersion: file.CreatorVersion,
		})
	}
	return d.adjudicate(raw)
}

type rawEntry struct {
	centralName    []byte
	localName      []byte
	centralExtra   []byte
	localExtra     []byte
	creatorVersion uint16
	externalAttrs  uint32
}

func (d *Decoder) adjudicate(raw []rawEntry) (Archive, error) {
	rootAbs, err := absoluteRoot(d.Root)
	if err != nil {
		return nil, err
	}

	archive := make(Archive, 0, len(raw))
	for _, item := range raw {
		name := string(item.centralName)
		entry := Entry{
			Name:           name,
			LocalName:      string(item.localName),
			CreatorVersion: item.creatorVersion,
			ExternalAttrs:  item.externalAttrs,
			Extra:          append([]byte(nil), item.centralExtra...),
			LocalExtra:     append([]byte(nil), item.localExtra...),
			IsSymlink:      isSymlink(item.creatorVersion, item.externalAttrs),
		}

		rel, err := normalizeEntryPath(name, "entry name")
		if err != nil {
			return nil, withEntry(err, name)
		}
		entry.NamePath = rel
		entry.Path = rel

		if entry.LocalName != name {
			if _, err := normalizeEntryPath(entry.LocalName, "local entry name"); err != nil {
				return nil, withEntry(err, name)
			}
		}

		effectiveName := name
		if unicodePaths, err := unicodePathsFrom(item.centralExtra, item.localExtra); err != nil {
			return nil, err
		} else if len(unicodePaths) > 0 {
			for _, unicodePath := range unicodePaths {
				rel, err := normalizeEntryPath(unicodePath, "unicode path")
				if err != nil {
					return nil, withEntry(err, name)
				}
				if entry.UnicodePath == "" {
					entry.UnicodePath = unicodePath
					entry.Path = rel
					effectiveName = rel
				}
			}
		}
		targets, err := unixTargetsFrom(item.centralExtra, item.localExtra)
		if err != nil {
			return nil, err
		}
		for _, target := range targets {
			targetPath, err := ResolveLinkTarget(rootAbs, effectiveName, target)
			if err != nil {
				return nil, withEntry(err, name)
			}
			if !entry.HasLinkTarget {
				entry.LinkTarget = target
				entry.TargetPath = targetPath
				entry.HasLinkTarget = true
			}
		}

		// When the extras contain no path, the entry name is the path that
		// was already checked above. This branch documents that fallback and
		// keeps the adjudication explicit.
		if !entry.HasLinkTarget && entry.UnicodePath == "" {
			if _, err := normalizeEntryPath(effectiveName, "entry name fallback"); err != nil {
				return nil, withEntry(err, name)
			}
		}

		if full, err := NormalizePath(rootAbs, entry.Path); err != nil {
			return nil, withEntry(err, name)
		} else {
			entry.FullPath = full
		}
		archive = append(archive, entry)
	}
	return archive, nil
}

func unicodePathsFrom(extras ...[]byte) ([]string, error) {
	var paths []string
	for _, extra := range extras {
		next, err := UnicodePaths(extra)
		if err != nil {
			return nil, err
		}
		paths = append(paths, next...)
	}
	return paths, nil
}

func unixTargetsFrom(extras ...[]byte) ([]string, error) {
	var targets []string
	for _, extra := range extras {
		next, err := UnixLinkTargets(extra)
		if err != nil {
			return nil, err
		}
		targets = append(targets, next...)
	}
	return targets, nil
}

func withEntry(err error, name string) error {
	if pathErr, ok := err.(*PathError); ok && pathErr.Entry == "" {
		pathErr.Entry = name
	}
	return err
}

func isSymlink(creatorVersion uint16, externalAttrs uint32) bool {
	_ = creatorVersion
	mode := externalAttrs >> 16
	return mode&0170000 == 0120000
}

func parseDirectory(data []byte) ([]rawEntry, error) {
	eocd, err := findEnd(data)
	if err != nil {
		return nil, err
	}

	entries := uint64(binary.LittleEndian.Uint16(data[eocd+10:]))
	cdSize := uint64(binary.LittleEndian.Uint32(data[eocd+12:]))
	cdOffset := uint64(binary.LittleEndian.Uint32(data[eocd+16:]))

	if entries == 0xffff || cdSize == 0xffffffff || cdOffset == 0xffffffff {
		entries, cdSize, cdOffset, err = zip64Directory(data, eocd)
		if err != nil {
			return nil, err
		}
	}
	if err := checkDirectoryBounds(data, entries, cdSize, cdOffset); err != nil {
		return nil, err
	}

	result := make([]rawEntry, 0, entries)
	offset := int(cdOffset)
	for i := uint64(0); i < entries; i++ {
		if offset+46 > len(data) {
			return nil, fmt.Errorf("%w: truncated central directory", ErrInvalidArchive)
		}
		if binary.LittleEndian.Uint32(data[offset:]) != centralHeaderSignature {
			return nil, fmt.Errorf("%w: bad central directory signature at %d", ErrInvalidArchive, offset)
		}

		creatorVersion := binary.LittleEndian.Uint16(data[offset+4:])
		uncompressed := binary.LittleEndian.Uint32(data[offset+24:])
		compressed := binary.LittleEndian.Uint32(data[offset+20:])
		nameLen := int(binary.LittleEndian.Uint16(data[offset+28:]))
		extraLen := int(binary.LittleEndian.Uint16(data[offset+30:]))
		commentLen := int(binary.LittleEndian.Uint16(data[offset+32:]))
		externalAttrs := binary.LittleEndian.Uint32(data[offset+38:])
		localOffset := uint64(binary.LittleEndian.Uint32(data[offset+42:]))

		nameStart := offset + 46
		nameEnd := nameStart + nameLen
		extraEnd := nameEnd + extraLen
		recordEnd := extraEnd + commentLen
		if nameEnd > len(data) || extraEnd > len(data) || recordEnd > len(data) {
			return nil, fmt.Errorf("%w: central entry exceeds archive", ErrInvalidArchive)
		}

		centralName := append([]byte(nil), data[nameStart:nameEnd]...)
		centralExtra := append([]byte(nil), data[nameEnd:extraEnd]...)
		if localOffset == 0xffffffff {
			localOffset, err = localOffsetFromZip64(centralExtra, uncompressed, compressed)
			if err != nil {
				return nil, err
			}
		}
		localName, localExtra, err := readLocalHeader(data, localOffset)
		if err != nil {
			return nil, err
		}

		result = append(result, rawEntry{
			centralName:    centralName,
			localName:      localName,
			centralExtra:   centralExtra,
			localExtra:     localExtra,
			creatorVersion: creatorVersion,
			externalAttrs:  externalAttrs,
		})
		offset = recordEnd
	}
	return result, nil
}

func findEnd(data []byte) (int, error) {
	if len(data) < 22 {
		return 0, fmt.Errorf("%w: too short for end record", ErrInvalidArchive)
	}
	start := len(data) - 22
	limit := start - 65535
	if limit < 0 {
		limit = 0
	}
	for i := start; i >= limit; i-- {
		if binary.LittleEndian.Uint32(data[i:]) != endSignature {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(data[i+20:]))
		if i+22+commentLen == len(data) {
			if binary.LittleEndian.Uint16(data[i+4:]) != 0 ||
				binary.LittleEndian.Uint16(data[i+6:]) != 0 {
				return 0, fmt.Errorf("%w: multi-disk archives are not supported", ErrInvalidArchive)
			}
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: end of central directory not found", ErrInvalidArchive)
}

func zip64Directory(data []byte, eocd int) (uint64, uint64, uint64, error) {
	locator := eocd - 20
	if locator < 0 || binary.LittleEndian.Uint32(data[locator:]) != zip64LocatorSignature {
		return 0, 0, 0, fmt.Errorf("%w: missing zip64 locator", ErrInvalidArchive)
	}
	recordOffset := int64(binary.LittleEndian.Uint64(data[locator+8:]))
	if recordOffset < 0 || recordOffset+56 > int64(len(data)) {
		return 0, 0, 0, fmt.Errorf("%w: bad zip64 end record offset", ErrInvalidArchive)
	}
	record := int(recordOffset)
	if binary.LittleEndian.Uint32(data[record:]) != zip64EndSignature {
		return 0, 0, 0, fmt.Errorf("%w: bad zip64 end signature", ErrInvalidArchive)
	}
	entries := binary.LittleEndian.Uint64(data[record+32:])
	cdSize := binary.LittleEndian.Uint64(data[record+40:])
	cdOffset := binary.LittleEndian.Uint64(data[record+48:])
	return entries, cdSize, cdOffset, nil
}

func checkDirectoryBounds(data []byte, entries, cdSize, cdOffset uint64) error {
	if cdOffset > uint64(len(data)) || cdSize > uint64(len(data)) ||
		cdOffset+cdSize > uint64(len(data)) {
		return fmt.Errorf("%w: central directory outside archive", ErrInvalidArchive)
	}
	if entries > uint64(len(data))/46+1 {
		return fmt.Errorf("%w: implausible entry count", ErrInvalidArchive)
	}
	return nil
}

func localOffsetFromZip64(extra []byte, uncompressed, compressed uint32) (uint64, error) {
	fields, err := ParseExtra(extra)
	if err != nil {
		return 0, err
	}
	for _, field := range fields {
		if field.ID != zip64ExtraID {
			continue
		}
		pos := 0
		if uncompressed == 0xffffffff {
			pos += 8
		}
		if compressed == 0xffffffff {
			pos += 8
		}
		if pos+8 > len(field.Data) {
			return 0, fmt.Errorf("%w: truncated zip64 local offset", ErrInvalidArchive)
		}
		return binary.LittleEndian.Uint64(field.Data[pos : pos+8]), nil
	}
	return 0, fmt.Errorf("%w: missing zip64 local offset", ErrInvalidArchive)
}

func readLocalHeader(data []byte, offset uint64) ([]byte, []byte, error) {
	if offset > uint64(len(data)) || offset+30 > uint64(len(data)) {
		return nil, nil, fmt.Errorf("%w: local header outside archive", ErrInvalidArchive)
	}
	start := int(offset)
	if binary.LittleEndian.Uint32(data[start:]) != localHeaderSignature {
		return nil, nil, fmt.Errorf("%w: bad local header signature at %d", ErrInvalidArchive, offset)
	}
	nameLen := int(binary.LittleEndian.Uint16(data[start+26:]))
	extraLen := int(binary.LittleEndian.Uint16(data[start+28:]))
	nameStart := start + 30
	nameEnd := nameStart + nameLen
	extraEnd := nameEnd + extraLen
	if nameEnd > len(data) || extraEnd > len(data) {
		return nil, nil, fmt.Errorf("%w: local header exceeds archive", ErrInvalidArchive)
	}
	return append([]byte(nil), data[nameStart:nameEnd]...),
		append([]byte(nil), data[nameEnd:extraEnd]...), nil
}
