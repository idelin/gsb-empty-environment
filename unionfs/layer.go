// Package unionfs assembles a read-only, path-addressable union view from a
// stack of tar archive layers.
//
// Layers follow the overlay/OCI whiteout conventions:
//
//   - a ".wh.<name>" entry hides <name> (and, when it is a directory, its
//     whole subtree) contributed by lower layers only;
//   - a ".wh..wh..opq" entry makes the containing directory opaque: lower
//     layer entries below it are hidden, entries of the marking layer remain
//     visible;
//   - markers never affect entries of the same layer, so the order of entries
//     inside a tar archive is irrelevant.
package unionfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const blockSize = 512

const (
	whiteoutPrefix = ".wh."
	opaqueWhiteout = ".wh..wh..opq"
)

// ErrEscapesRoot is returned when a link or entry path would resolve outside
// of the union root.
var ErrEscapesRoot = errors.New("unionfs: path escapes the union root")

// Layer is a single read-only tar archive exposed through an io.ReaderAt.
// Header indexing is performed lazily on first use and file content bytes are
// never touched until a regular file is opened.
type Layer struct {
	ra   io.ReaderAt
	size int64
	idx  int

	once sync.Once
	err  error

	nodes     map[string]*node
	whiteouts map[string]struct{}
	opaques   map[string]struct{}
}

// NewLayer creates a lazy layer over the given tar archive. size is the total
// length of the archive in bytes. No I/O happens until the layer is queried.
func NewLayer(ra io.ReaderAt, size int64) *Layer {
	return &Layer{ra: ra, size: size}
}

type nodeType uint8

const (
	typeFile nodeType = iota
	typeDir
	typeSymlink
	typeHardlink
	typeOther
)

// fileObject is the concrete, immutable backing storage of a regular file's
// content. Hardlinks bind to the same fileObject as their target instead of
// re-resolving a path at read time.
type fileObject struct {
	layer  int
	ra     io.ReaderAt
	offset int64
	size   int64
}

type node struct {
	path     string
	typ      nodeType
	mode     os.FileMode
	modTime  time.Time
	layer    int
	size     int64
	obj      *fileObject
	link     string
	linkErr  error
	implicit bool
}

func (l *Layer) ensureIndexed() error {
	l.once.Do(l.scan)
	return l.err
}

// scan walks header blocks only. Every file data section is skipped with a
// Seek, so indexing never reads file content through the ReaderAt.
func (l *Layer) scan() {
	l.nodes = map[string]*node{}
	l.whiteouts = map[string]struct{}{}
	l.opaques = map[string]struct{}{}

	sr := io.NewSectionReader(l.ra, 0, l.size)
	pos := int64(0)
	global := map[string]string{}

	for pos < l.size {
		pending := map[string]string{}
		var h tar.Header
		dataOff := pos

	headers:
		for {
			var block [blockSize]byte
			n, err := io.ReadFull(sr, block[:])
			pos += int64(n)
			if errors.Is(err, io.EOF) {
				return
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				l.err = io.ErrUnexpectedEOF
				return
			}
			if err != nil {
				l.err = err
				return
			}
			if isZeroBlock(block) {
				return
			}
			if err := parseTarHeader(block[:], &h); err != nil {
				l.err = fmt.Errorf("unionfs: invalid tar header at offset %d: %w", pos-int64(n), err)
				return
			}
			dataOff = pos

			switch h.Typeflag {
			case tar.TypeXHeader, tar.TypeXGlobalHeader:
				data, err := readSection(sr, h.Size)
				if err != nil {
					l.err = err
					return
				}
				pos += int64(len(data))
				recs := parsePAXRecords(data)
				if h.Typeflag == tar.TypeXGlobalHeader {
					for k, v := range recs {
						global[k] = v
					}
				} else {
					for k, v := range recs {
						pending[k] = v
					}
				}
				pos = dataOff + pad512(h.Size)
				if _, err := sr.Seek(pos, io.SeekStart); err != nil {
					l.err = err
					return
				}
			case tar.TypeGNULongName, tar.TypeGNULongLink:
				data, err := readSection(sr, h.Size)
				if err != nil {
					l.err = err
					return
				}
				pos += int64(len(data))
				val := strings.TrimRight(string(data), "\x00")
				if h.Typeflag == tar.TypeGNULongName {
					pending["path"] = val
				} else {
					pending["linkpath"] = val
				}
				pos = dataOff + pad512(h.Size)
				if _, err := sr.Seek(pos, io.SeekStart); err != nil {
					l.err = err
					return
				}
			case tar.TypeGNUSparse:
				l.err = errors.New("unionfs: GNU sparse tar entries are not supported")
				return
			default:
				applyHeaderOverride(&h, global, pending)
				if err := l.record(&h, dataOff); err != nil {
					l.err = err
					return
				}
				pos = dataOff + pad512(h.Size)
				if _, err := sr.Seek(pos, io.SeekStart); err != nil {
					l.err = err
					return
				}
				break headers
			}
		}
	}
}

func readSection(sr *io.SectionReader, size int64) ([]byte, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(sr, data); err != nil {
		return nil, fmt.Errorf("unionfs: reading tar extension header: %w", err)
	}
	return data, nil
}

func applyHeaderOverride(h *tar.Header, global, pending map[string]string) {
	if v, ok := global["path"]; ok {
		h.Name = v
	}
	if v, ok := global["linkpath"]; ok {
		h.Linkname = v
	}
	if v, ok := pending["path"]; ok {
		h.Name = v
	}
	if v, ok := pending["linkpath"]; ok {
		h.Linkname = v
	}
	if v, ok := pending["size"]; ok {
		if size, err := strconv.ParseInt(v, 10, 64); err == nil {
			h.Size = size
		}
	}
}

func (l *Layer) record(h *tar.Header, dataOff int64) error {
	name, err := safeClean(h.Name)
	if err != nil {
		return fmt.Errorf("unionfs: unsafe entry path %q: %w", h.Name, err)
	}

	if base := path.Base(name); strings.HasPrefix(base, whiteoutPrefix) {
		dir := path.Dir(name)
		if base == opaqueWhiteout {
			l.opaques[dir] = struct{}{}
		} else {
			target := path.Join(dir, strings.TrimPrefix(base, whiteoutPrefix))
			l.whiteouts[target] = struct{}{}
		}
		return nil
	}

	n := &node{
		path:    name,
		layer:   l.idx,
		mode:    entryMode(h),
		modTime: h.ModTime,
		size:    h.Size,
	}
	switch h.Typeflag {
	case tar.TypeDir:
		n.typ = typeDir
		n.size = 0
	case tar.TypeReg, tar.TypeRegA:
		n.typ = typeFile
		n.obj = &fileObject{
			layer:  l.idx,
			ra:     l.ra,
			offset: dataOff,
			size:   h.Size,
		}
	case tar.TypeSymlink:
		n.typ = typeSymlink
		n.link = h.Linkname
	case tar.TypeLink:
		n.typ = typeHardlink
		n.link = h.Linkname
	default:
		n.typ = typeOther
	}
	// Duplicate paths inside one archive: the later header wins, but whiteout
	// handling is independent of this choice.
	l.nodes[name] = n
	return nil
}

func entryMode(h *tar.Header) os.FileMode {
	perm := os.FileMode(h.Mode).Perm()
	switch h.Typeflag {
	case tar.TypeDir:
		return os.ModeDir | perm
	case tar.TypeSymlink:
		return os.ModeSymlink | perm
	case tar.TypeLink, tar.TypeReg, tar.TypeRegA:
		return perm
	default:
		return otherMode(h)
	}
}

func otherMode(h *tar.Header) os.FileMode {
	perm := os.FileMode(h.Mode).Perm()
	switch h.Typeflag {
	case tar.TypeChar:
		return os.ModeCharDevice | os.ModeDevice | perm
	case tar.TypeBlock:
		return os.ModeDevice | perm
	case tar.TypeFifo:
		return os.ModeNamedPipe | perm
	case tar.TypeCont:
		return perm
	default:
		return perm
	}
}

// safeClean normalizes a slash separated, root-relative tar path and rejects
// any ".." traversal that would escape the archive root.
func safeClean(p string) (string, error) {
	p = strings.TrimPrefix(p, "/")
	depth := 0
	for _, comp := range strings.Split(p, "/") {
		switch comp {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return "", ErrEscapesRoot
			}
		default:
			depth++
		}
	}
	cleaned := path.Clean("/" + p)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return ".", nil
	}
	return cleaned, nil
}

func parsePAXRecords(b []byte) map[string]string {
	out := map[string]string{}
	for len(b) > 0 {
		sp := bytes.IndexByte(b, ' ')
		if sp < 0 {
			return out
		}
		recordLen, err := strconv.Atoi(string(b[:sp]))
		if err != nil || recordLen <= 0 || recordLen > len(b) {
			return out
		}
		rec := b[:recordLen]
		eq := bytes.IndexByte(rec, '=')
		if eq < 0 {
			return out
		}
		key := string(rec[sp+1 : eq])
		val := string(rec[eq+1 : len(rec)-1])
		out[key] = val
		b = b[recordLen:]
	}
	return out
}

func isZeroBlock(b [blockSize]byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// parseTarHeader decodes one 512-byte V7/USTAR header block. It covers the
// fixed fields used by the union index, including octal and base-256 numeric
// fields. PAX/GNU extension headers are handled by the caller.
func parseTarHeader(block []byte, h *tar.Header) error {
	if len(block) != blockSize {
		return errors.New("unionfs: short tar header")
	}
	name := cString(block[0:100])
	if bytes.HasPrefix(block[257:262], []byte("ustar")) {
		prefix := cString(block[345:500])
		if prefix != "" {
			name = prefix + "/" + name
		}
	}
	mode, err := parseNumeric(block[100:108])
	if err != nil {
		return err
	}
	uid, err := parseNumeric(block[108:116])
	if err != nil {
		return err
	}
	gid, err := parseNumeric(block[116:124])
	if err != nil {
		return err
	}
	size, err := parseNumeric(block[124:136])
	if err != nil {
		return err
	}
	mtime, err := parseNumeric(block[136:148])
	if err != nil {
		return err
	}

	h.Name = name
	h.Mode = mode
	h.Uid = int(uid)
	h.Gid = int(gid)
	h.Size = size
	h.ModTime = time.Unix(mtime, 0)
	h.Typeflag = block[156]
	if h.Typeflag == 0 {
		h.Typeflag = tar.TypeReg
	}
	h.Linkname = cString(block[157:257])
	if bytes.HasPrefix(block[257:262], []byte("ustar")) {
		h.Uname = cString(block[265:297])
		h.Gname = cString(block[297:329])
		devMajor, err1 := parseNumeric(block[329:337])
		devMinor, err2 := parseNumeric(block[337:345])
		if err1 == nil && err2 == nil {
			h.Devmajor, h.Devminor = devMajor, devMinor
		}
	}
	return nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// parseNumeric handles octal numeric fields and GNU base-256 encoded values.
func parseNumeric(b []byte) (int64, error) {
	for len(b) > 0 && (b[0] == ' ' || b[0] == 0) {
		b = b[1:]
	}
	if len(b) == 0 {
		return 0, nil
	}
	if b[0] == 0x80 {
		var v int64
		for _, c := range b[1:] {
			v = v<<8 | int64(c)
		}
		return v, nil
	}
	s := strings.TrimRight(strings.TrimSpace(string(b)), " \x00")
	s = strings.TrimRight(s, "\x00")
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(s, 8, 64)
	if err != nil {
		return 0, fmt.Errorf("unionfs: invalid numeric field %q: %w", s, err)
	}
	return v, nil
}

func pad512(size int64) int64 {
	return (size + blockSize - 1) / blockSize * blockSize
}
