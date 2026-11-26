package safeunpack

import (
	"bufio"
	"bytes"
	"io"
)

// Format identifies the archive grammar selected by the caller or detector.
type Format string

const (
	FormatAuto    Format = "auto"
	FormatTAR     Format = "tar"
	FormatTARGzip Format = "tar.gz"
	FormatZIP     Format = "zip"
	FormatUnknown Format = "unknown"
)

// String implements fmt.Stringer.
func (f Format) String() string {
	if f == "" {
		return string(FormatUnknown)
	}
	return string(f)
}

// DetectFormat classifies an archive from its leading bytes. Empty input is
// reported as unknown; a TAR is the default for non-ZIP, non-gzip bytes.
func DetectFormat(data []byte) Format {
	if len(data) == 0 {
		return FormatUnknown
	}
	if len(data) >= 4 {
		if bytes.Equal(data[0:4], []byte{'P', 'K', 3, 4}) ||
			bytes.Equal(data[0:4], []byte{'P', 'K', 5, 6}) ||
			bytes.Equal(data[0:4], []byte{'P', 'K', 7, 8}) {
			return FormatZIP
		}
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return FormatTARGzip
	}
	return FormatTAR
}

// UnpackTAR validates a raw USTAR/PAX stream and returns its virtual tree. If
// the stream begins with gzip magic it is transparently treated as tar.gz.
func UnpackTAR(r io.Reader, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackTAR(r)
}

// UnpackTARGzip validates a gzip-compressed USTAR/PAX stream.
func UnpackTARGzip(r io.Reader, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackTARGzip(r)
}

// UnpackZIP validates a ZIP archive and returns its virtual tree.
func UnpackZIP(r io.Reader, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackZIP(r)
}

// UnpackArchive detects ZIP, gzip-compressed TAR, or raw TAR and validates it.
func UnpackArchive(r io.Reader, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackArchive(r)
}

// UnpackTARBytes is the []byte convenience form of UnpackTAR.
func UnpackTARBytes(data []byte, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackTAR(bytes.NewReader(data))
}

// UnpackZIPBytes is the []byte convenience form of UnpackZIP.
func UnpackZIPBytes(data []byte, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackZIP(bytes.NewReader(data))
}

// UnpackArchiveBytes is the []byte convenience form of UnpackArchive.
func UnpackArchiveBytes(data []byte, opts ...Option) (*Result, error) {
	return effectiveScanner(opts).UnpackArchive(bytes.NewReader(data))
}

// ValidateTAR validates a TAR without exposing the decoded contents.
func ValidateTAR(r io.Reader, opts ...Option) error {
	_, err := UnpackTAR(r, opts...)
	return err
}

// ValidateTARGzip validates a gzip-compressed TAR.
func ValidateTARGzip(r io.Reader, opts ...Option) error {
	_, err := UnpackTARGzip(r, opts...)
	return err
}

// ValidateZIP validates a ZIP archive without exposing the decoded contents.
func ValidateZIP(r io.Reader, opts ...Option) error {
	_, err := UnpackZIP(r, opts...)
	return err
}

// ValidateArchive auto-detects the archive format and validates it.
func ValidateArchive(r io.Reader, opts ...Option) error {
	_, err := UnpackArchive(r, opts...)
	return err
}

// ValidateTARBytes is the []byte convenience form of ValidateTAR.
func ValidateTARBytes(data []byte, opts ...Option) error {
	_, err := UnpackTARBytes(data, opts...)
	return err
}

// ValidateZIPBytes is the []byte convenience form of ValidateZIP.
func ValidateZIPBytes(data []byte, opts ...Option) error {
	_, err := UnpackZIPBytes(data, opts...)
	return err
}

// ValidateArchiveBytes is the []byte convenience form of ValidateArchive.
func ValidateArchiveBytes(data []byte, opts ...Option) error {
	_, err := UnpackArchiveBytes(data, opts...)
	return err
}

// ScanTAR is an alias for UnpackTAR.
func ScanTAR(r io.Reader, opts ...Option) (*Result, error) { return UnpackTAR(r, opts...) }

// ScanZIP is an alias for UnpackZIP.
func ScanZIP(r io.Reader, opts ...Option) (*Result, error) { return UnpackZIP(r, opts...) }

// ScanArchive is an alias for UnpackArchive.
func ScanArchive(r io.Reader, opts ...Option) (*Result, error) { return UnpackArchive(r, opts...) }

// AnalyzeTAR is an alias for UnpackTAR.
func AnalyzeTAR(r io.Reader, opts ...Option) (*Result, error) { return UnpackTAR(r, opts...) }

// AnalyzeZIP is an alias for UnpackZIP.
func AnalyzeZIP(r io.Reader, opts ...Option) (*Result, error) { return UnpackZIP(r, opts...) }

// AnalyzeArchive is an alias for UnpackArchive.
func AnalyzeArchive(r io.Reader, opts ...Option) (*Result, error) { return UnpackArchive(r, opts...) }

func effectiveScanner(opts []Option) *Scanner {
	s := settings{limits: DefaultLimits()}
	for _, opt := range opts {
		if opt != nil {
			opt(&s)
		}
	}
	return &Scanner{limits: s.limits.normalized()}
}

// UnpackTAR validates a TAR stream with this Scanner's limits.
func (s *Scanner) UnpackTAR(r io.Reader) (*Result, error) {
	return unpackTARFrom(r, s.limits, FormatTAR)
}

// UnpackTARGzip validates a gzip-compressed TAR stream.
func (s *Scanner) UnpackTARGzip(r io.Reader) (*Result, error) {
	return unpackTARFrom(r, s.limits, FormatTARGzip)
}

// UnpackZIP validates a ZIP image with this Scanner's limits.
func (s *Scanner) UnpackZIP(r io.Reader) (*Result, error) {
	data, err := readAll(r, s.limits.MaxArchiveBytes)
	if err != nil {
		return nil, err
	}
	return scanZIP(data, s.limits)
}

// UnpackArchive detects and validates a TAR, TAR gzip, or ZIP stream.
func (s *Scanner) UnpackArchive(r io.Reader) (*Result, error) {
	br := bufio.NewReader(r)
	peek, _ := br.Peek(4)
	switch DetectFormat(peek) {
	case FormatZIP:
		data, err := readAll(br, s.limits.MaxArchiveBytes)
		if err != nil {
			return nil, err
		}
		return scanZIP(data, s.limits)
	case FormatTARGzip:
		return unpackTARFrom(br, s.limits, FormatTARGzip)
	default:
		return unpackTARFrom(br, s.limits, FormatTAR)
	}
}

// ValidateTAR validates a TAR stream without returning its tree.
func (s *Scanner) ValidateTAR(r io.Reader) error {
	_, err := s.UnpackTAR(r)
	return err
}

// ValidateTARGzip validates a gzip-compressed TAR stream.
func (s *Scanner) ValidateTARGzip(r io.Reader) error {
	_, err := s.UnpackTARGzip(r)
	return err
}

// ValidateZIP validates a ZIP stream.
func (s *Scanner) ValidateZIP(r io.Reader) error {
	_, err := s.UnpackZIP(r)
	return err
}

// ValidateArchive detects and validates an archive stream.
func (s *Scanner) ValidateArchive(r io.Reader) error {
	_, err := s.UnpackArchive(r)
	return err
}
