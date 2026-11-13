package netsec

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"time"
)

// DefaultAbsoluteTarget is placed in the Unix extra field by
// BuildMaliciousArchive. It is absolute and therefore outside every extraction
// root.
const DefaultAbsoluteTarget = "/tmp/q0339-link-target"

// WriteEntry describes an entry for BuildArchive.
type WriteEntry struct {
	Name       string
	LinkTarget string
	Data       []byte
	Mode       os.FileMode
	Extra      []byte
}

// BuildArchive builds a ZIP archive from entries. A non-empty LinkTarget is
// written verbatim into a 0x000d UNIX extra field.
func BuildArchive(entries ...WriteEntry) ([]byte, error) {
	var out bytes.Buffer
	if err := WriteArchive(&out, entries...); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// WriteArchive writes a ZIP archive to w.
func WriteArchive(w io.Writer, entries ...WriteEntry) error {
	writer := zip.NewWriter(w)
	for _, item := range entries {
		header := &zip.FileHeader{
			Name:     item.Name,
			Method:   zip.Store,
			Modified: time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC),
		}

		mode := item.Mode
		if mode == 0 {
			mode = 0o644
		}
		header.SetMode(mode)

		extra := append([]byte(nil), item.Extra...)
		if item.LinkTarget != "" {
			extra = append(extra, UnixLinkExtras(item.LinkTarget)...)
		}
		header.Extra = extra

		file, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if item.Data != nil {
			if _, err := file.Write(item.Data); err != nil {
				return err
			}
		}
	}
	return writer.Close()
}

// UnixLinkExtras writes the target into both the PKWARE UNIX (0x000d) and
// Info-ZIP Unix (0x5855) fields. The former is where the documented
// linked-to-name tail lives; the latter keeps the fixture compatible with
// readers that identify the field as Info-ZIP Unix.
func UnixLinkExtras(target string) []byte {
	extra := UnixExtraWithID(InfoZipUnixExtraID, target)
	return append(extra, UnixExtraWithID(UnixExtraID, target)...)
}

// BuildAbsoluteLinkArchive builds an archive whose lone symlink entry has an
// absolute link target in its Unix extra field.
func BuildAbsoluteLinkArchive(target string) ([]byte, error) {
	if target == "" {
		target = DefaultAbsoluteTarget
	}
	return BuildArchive(WriteEntry{
		Name:       "link",
		LinkTarget: target,
		Mode:       os.ModeSymlink | 0o777,
		Data:       []byte(target),
	})
}

// BuildMaliciousArchive builds the absolute-link fixture using
// DefaultAbsoluteTarget.
func BuildMaliciousArchive() ([]byte, error) {
	return BuildAbsoluteLinkArchive(DefaultAbsoluteTarget)
}

// MaliciousArchive is an alias for BuildMaliciousArchive.
func MaliciousArchive() ([]byte, error) {
	return BuildMaliciousArchive()
}

// WriteMaliciousArchive writes the absolute-link fixture to w.
func WriteMaliciousArchive(w io.Writer) error {
	archive, err := BuildMaliciousArchive()
	if err != nil {
		return err
	}
	_, err = w.Write(archive)
	return err
}

// CreateMaliciousArchive writes the absolute-link fixture to path.
func CreateMaliciousArchive(path string) error {
	archive, err := BuildMaliciousArchive()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		return fmt.Errorf("write malicious archive: %w", err)
	}
	return nil
}
