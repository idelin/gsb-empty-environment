package safeunpack

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math"
)

const (
	tarBlockSize = 512
	readChunk    = 256 << 10
	maxTarTail   = 1 << 20
)

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type ratioMeter struct {
	compressed *countReader
	decoded    int64
	maxRatio   float64
	format     Format
}

func (m *ratioMeter) add(n int64) error {
	m.decoded += n
	if m.maxRatio <= 0 || m.compressed == nil || m.compressed.n <= 0 {
		return nil
	}
	// Compressed TAR headers contain long runs of zeros and can legitimately
	// compress very well. Ratio checking starts once enough decoded data exists.
	if m.decoded < 64<<10 {
		return nil
	}
	ratio := float64(m.decoded) / float64(m.compressed.n)
	if ratio > m.maxRatio {
		return wrapError(ErrCompressionBomb, "", -1,
			"%s compression ratio %.1f exceeds limit %.1f", m.format, ratio, m.maxRatio)
	}
	return nil
}

type tarScanner struct {
	r       io.Reader
	t       *treeState
	limits  Limits
	offset  int64
	entries int
	meta    int64
	global  map[string]string
	pending map[string]string
	ratio   *ratioMeter
}

func scanTar(r io.Reader, lim Limits, meter *ratioMeter) (*Result, error) {
	s := &tarScanner{
		r:      r,
		t:      newTree(),
		limits: lim,
		global: make(map[string]string),
		ratio:  meter,
	}
	if err := s.scan(); err != nil {
		return nil, err
	}
	if s.pending != nil {
		return nil, wrapError(ErrMalformed, "", s.offset, "local PAX header is not followed by an entry")
	}
	var compressed int64
	var maxRatio float64
	format := FormatTAR
	if meter != nil {
		format = FormatTARGzip
		compressed = meter.compressed.n
		if compressed > 0 {
			maxRatio = float64(s.offset) / float64(compressed)
		}
	}
	return s.t.result(format, compressed, maxRatio), nil
}

func (s *tarScanner) scan() error {
	for {
		header, err := s.readBlock()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if bytes.Equal(header, make([]byte, tarBlockSize)) {
			return s.drainZeroTail()
		}
		headerOffset := s.offset - tarBlockSize
		if err := s.validateHeader(header, headerOffset); err != nil {
			return err
		}

		s.entries++
		if s.limits.MaxEntries >= 0 && s.entries > s.limits.MaxEntries {
			return wrapError(ErrLimitExceeded, "", headerOffset,
				"entry count %d exceeds limit %d", s.entries, s.limits.MaxEntries)
		}

		typeflag := header[156]
		size, err := parseTarNumber(header[124:136], "size", true)
		if err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid size field: %v", err)
		}

		switch typeflag {
		case 'x', 'g':
			if s.pending != nil {
				return wrapError(ErrMalformed, "", headerOffset,
					"local PAX header is followed by another extension header")
			}
			data, err := s.readMetadata(size, headerOffset)
			if err != nil {
				return err
			}
			records, err := parsePAX(data)
			if err != nil {
				return err
			}
			if typeflag == 'x' {
				s.pending = records
			} else {
				mergePAX(s.global, records)
			}
			continue
		}

		rawPath := tarHeaderPath(header)
		rawLink := tarString(header[157:257])
		local := s.pending
		s.pending = nil
		path, link, effectiveSize, err := applyPAX(rawPath, rawLink, size, local, s.global)
		if err != nil {
			return err
		}

		mode, err := parseTarNumber(header[100:108], "mode", true)
		if err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid mode field: %v", err)
		}
		if _, err := parseTarNumber(header[108:116], "uid", true); err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid uid field: %v", err)
		}
		if _, err := parseTarNumber(header[116:124], "gid", true); err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid gid field: %v", err)
		}
		if _, err := parseTarNumber(header[136:148], "mtime", true); err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid mtime field: %v", err)
		}
		if _, err := parseTarNumber(header[329:337], "devmajor", false); err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid devmajor field: %v", err)
		}
		if _, err := parseTarNumber(header[337:345], "devminor", false); err != nil {
			return wrapError(ErrMalformed, "", headerOffset, "invalid devminor field: %v", err)
		}

		normPath, parts, trailingSlash, err := normalizeEntryPath(path, s.limits.MaxPathBytes)
		if err != nil {
			return decorate(err, path, headerOffset)
		}

		switch typeflag {
		case 0, '0', '7':
			if trailingSlash {
				return wrapError(ErrUnsafePath, normPath, headerOffset, "regular file path has a trailing slash")
			}
			if err := s.t.preflightNonDirectory(normPath, parts, headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
			if s.limits.MaxFileBytes >= 0 && effectiveSize > s.limits.MaxFileBytes {
				return wrapError(ErrLimitExceeded, normPath, headerOffset,
					"file size %d exceeds per-file limit %d", effectiveSize, s.limits.MaxFileBytes)
			}
			if err := s.t.charge(effectiveSize, s.limits.MaxFileBytes, s.limits.MaxTotalBytes); err != nil {
				return decorate(err, normPath, headerOffset)
			}
			data, err := s.readData(effectiveSize)
			if err != nil {
				return decorate(err, normPath, headerOffset)
			}
			if err := s.t.addRegular(normPath, parts, uint32(mode), data, headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
		case '5':
			if effectiveSize != 0 {
				return wrapError(ErrMalformed, normPath, headerOffset, "directory entry has non-zero size")
			}
			if err := s.t.addDirectory(normPath, parts, uint32(mode), headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
		case '2':
			if effectiveSize != 0 {
				return wrapError(ErrMalformed, normPath, headerOffset, "symbolic link entry has non-zero size")
			}
			if trailingSlash {
				return wrapError(ErrUnsafePath, normPath, headerOffset, "symbolic link path has a trailing slash")
			}
			if err := s.t.preflightNonDirectory(normPath, parts, headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
			resolved, err := s.t.resolveSymlinkTarget(normPath, parts[:len(parts)-1], link, s.limits.MaxPathBytes)
			if err != nil {
				return decorate(err, normPath, headerOffset)
			}
			if err := s.t.addSymlink(normPath, parts, uint32(mode), link, resolved, 0, headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
		case '1':
			if effectiveSize != 0 {
				return wrapError(ErrMalformed, normPath, headerOffset, "hardlink entry has non-zero size")
			}
			if trailingSlash {
				return wrapError(ErrUnsafePath, normPath, headerOffset, "hardlink path has a trailing slash")
			}
			if err := s.t.preflightNonDirectory(normPath, parts, headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
			target, err := s.t.resolveHardlinkTarget(link, s.limits.MaxPathBytes)
			if err != nil {
				return decorate(err, normPath, headerOffset)
			}
			if err := s.t.addHardlink(normPath, parts, uint32(mode), link, target, headerOffset); err != nil {
				return decorate(err, normPath, headerOffset)
			}
		default:
			return wrapError(ErrUnsupported, normPath, headerOffset, "unsupported TAR type flag %q", typeflag)
		}
	}
}

func (s *tarScanner) drainZeroTail() error {
	buf := make([]byte, readChunk)
	tail := 0
	for tail < maxTarTail {
		n, err := s.r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if !bytes.Equal(chunk, make([]byte, n)) {
				return wrapError(ErrMalformed, "", s.offset, "non-zero data after TAR end marker")
			}
			tail += n
			s.offset += int64(n)
			if s.ratio != nil {
				if err := s.ratio.add(int64(n)); err != nil {
					return err
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return wrapError(ErrMalformed, "", s.offset, "invalid trailing TAR stream: %v", err)
		}
	}
	return wrapError(ErrMalformed, "", s.offset, "TAR trailing zero padding exceeds %d bytes", maxTarTail)
}

func (s *tarScanner) readBlock() ([]byte, error) {
	block := make([]byte, tarBlockSize)
	n, err := io.ReadFull(s.r, block)
	if err == io.EOF && n == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, wrapError(ErrMalformed, "", s.offset,
			"truncated TAR header: read %d of %d bytes", n, tarBlockSize)
	}
	s.offset += tarBlockSize
	if s.ratio != nil {
		if err := s.ratio.add(tarBlockSize); err != nil {
			return nil, err
		}
	}
	return block, nil
}

func (s *tarScanner) readMetadata(size, headerOffset int64) ([]byte, error) {
	if size < 0 {
		return nil, wrapError(ErrMalformed, "", headerOffset, "negative PAX header size")
	}
	if s.limits.MaxFileBytes >= 0 && size > s.limits.MaxFileBytes {
		return nil, wrapError(ErrLimitExceeded, "", headerOffset,
			"PAX metadata size %d exceeds per-file limit %d", size, s.limits.MaxFileBytes)
	}
	if s.limits.MaxTotalBytes >= 0 && (s.meta > s.limits.MaxTotalBytes || size > s.limits.MaxTotalBytes-s.meta) {
		return nil, wrapError(ErrLimitExceeded, "", headerOffset,
			"PAX metadata exceeds total limit %d", s.limits.MaxTotalBytes)
	}
	s.meta += size
	return s.readData(size)
}

func (s *tarScanner) readData(size int64) ([]byte, error) {
	if size < 0 {
		return nil, wrapError(ErrMalformed, "", s.offset, "negative data size")
	}
	if size > int64(math.MaxInt) {
		return nil, wrapError(ErrLimitExceeded, "", s.offset, "data size does not fit in memory")
	}
	data := make([]byte, 0, int(size))
	for remaining := size; remaining > 0; {
		chunk := int64(readChunk)
		if remaining < chunk {
			chunk = remaining
		}
		buf := make([]byte, int(chunk))
		n, err := io.ReadFull(s.r, buf)
		if err != nil {
			return nil, wrapError(ErrMalformed, "", s.offset,
				"truncated TAR data: read %d of %d bytes", n, chunk)
		}
		data = append(data, buf...)
		s.offset += chunk
		remaining -= chunk
		if s.ratio != nil {
			if err := s.ratio.add(chunk); err != nil {
				return nil, err
			}
		}
	}
	pad := (tarBlockSize - size%tarBlockSize) % tarBlockSize
	if pad > 0 {
		padding := make([]byte, int(pad))
		n, err := io.ReadFull(s.r, padding)
		if err != nil {
			return nil, wrapError(ErrMalformed, "", s.offset,
				"truncated TAR padding: read %d of %d bytes", n, pad)
		}
		if !bytes.Equal(padding, make([]byte, int(pad))) {
			return nil, wrapError(ErrMalformed, "", s.offset, "non-zero TAR padding")
		}
		s.offset += pad
		if s.ratio != nil {
			if err := s.ratio.add(pad); err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}

func (s *tarScanner) validateHeader(h []byte, offset int64) error {
	stored, err := parseTarNumber(h[148:156], "checksum", true)
	if err != nil {
		return wrapError(ErrMalformed, "", offset, "invalid checksum field: %v", err)
	}
	var sumZero, sumSpace int64
	for i, b := range h {
		if i >= 148 && i < 156 {
			continue
		}
		sumZero += int64(b)
		sumSpace += int64(b)
	}
	sumSpace += int64(8 * ' ')
	if sumZero != stored && sumSpace != stored {
		return wrapError(ErrChecksum, "", offset,
			"header checksum is %d (or %d with space padding), stored value is %d",
			sumZero, sumSpace, stored)
	}
	magic := string(h[257:263])
	if magic != "" && magic != "ustar\x00" && magic != "ustar " {
		return wrapError(ErrUnsupported, "", offset, "unsupported TAR magic %q", magic)
	}
	return nil
}

func parseTarNumber(field []byte, name string, required bool) (int64, error) {
	i := 0
	for i < len(field) && field[i] == ' ' {
		i++
	}
	if i == len(field) {
		if required {
			return 0, fmt.Errorf("%s field is empty", name)
		}
		return 0, nil
	}
	if field[i] == 0 {
		for ; i < len(field); i++ {
			if field[i] != 0 {
				return 0, fmt.Errorf("%s field has embedded NUL", name)
			}
		}
		if required {
			return 0, fmt.Errorf("%s field is empty", name)
		}
		return 0, nil
	}
	if field[i] == '-' || field[i] == '+' {
		return 0, fmt.Errorf("%s field has a sign", name)
	}
	var value int64
	start := i
	for i < len(field) && field[i] >= '0' && field[i] <= '7' {
		d := int64(field[i] - '0')
		if value > (math.MaxInt64-d)/8 {
			return 0, fmt.Errorf("%s field overflows", name)
		}
		value = value*8 + d
		i++
	}
	if i == start {
		return 0, fmt.Errorf("%s field is not octal", name)
	}
	for ; i < len(field); i++ {
		if field[i] != 0 && field[i] != ' ' {
			return 0, fmt.Errorf("%s field has invalid trailing bytes", name)
		}
	}
	return value, nil
}

func tarString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func tarHeaderPath(h []byte) string {
	name := tarString(h[0:100])
	prefix := tarString(h[345:500])
	if prefix != "" {
		return prefix + "/" + name
	}
	return name
}

func applyPAX(rawPath, rawLink string, headerSize int64, local, global map[string]string) (string, string, int64, error) {
	path := rawPath
	link := rawLink
	size := headerSize
	if global != nil {
		if v, ok := global["path"]; ok {
			path = v
		}
		if v, ok := global["linkpath"]; ok {
			link = v
		}
		if v, ok := global["size"]; ok {
			n, err := parsePAXSize(v)
			if err != nil {
				return "", "", 0, err
			}
			size = n
		}
	}
	if local != nil {
		if v, ok := local["path"]; ok {
			path = v
		}
		if v, ok := local["linkpath"]; ok {
			link = v
		}
		if v, ok := local["size"]; ok {
			n, err := parsePAXSize(v)
			if err != nil {
				return "", "", 0, err
			}
			size = n
		}
	}
	if path == "" {
		return "", "", 0, wrapError(ErrUnsafePath, "", -1, "effective path is empty")
	}
	return path, link, size, nil
}

func decorate(err error, path string, offset int64) error {
	if e, ok := err.(*Error); ok {
		if e.Path == "" {
			e.Path = path
		}
		if e.Offset < 0 {
			e.Offset = offset
		}
		return e
	}
	return &Error{Kind: err, Path: path, Offset: offset}
}

func unpackTARFrom(r io.Reader, lim Limits, format Format) (*Result, error) {
	br := bufio.NewReader(r)
	if format == FormatAuto || format == FormatTARGzip {
		magic, _ := br.Peek(2)
		if bytes.Equal(magic, []byte{0x1f, 0x8b}) {
			compressed := &countReader{r: br}
			gz, err := gzip.NewReader(compressed)
			if err != nil {
				return nil, wrapError(ErrMalformed, "", 0, "invalid gzip stream: %v", err)
			}
			defer gz.Close()
			meter := &ratioMeter{
				compressed: compressed,
				maxRatio:   lim.MaxCompressionRatio,
				format:     FormatTARGzip,
			}
			return scanTar(gz, lim, meter)
		}
		if format == FormatTARGzip {
			return nil, wrapError(ErrMalformed, "", 0, "TAR gzip stream has no gzip magic")
		}
	}
	return scanTar(br, lim, nil)
}
