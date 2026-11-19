package netsec

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// SecurityLevel selects the policy used to validate a gem archive.
//
// HighSecurity requires valid signatures for both metadata.gz and data.tar.gz.
// MediumSecurity verifies every signature that is present, but accepts an
// entirely unsigned gem. In both modes a signature set that is only partially
// present is rejected.
type SecurityLevel int

const (
	HighSecurity SecurityLevel = iota
	MediumSecurity
)

// Short and descriptive aliases for callers that prefer them.
const (
	High               = HighSecurity
	Medium             = MediumSecurity
	SecurityHigh       = HighSecurity
	SecurityMedium     = MediumSecurity
	HighSecurityMode   = HighSecurity
	MediumSecurityMode = MediumSecurity
)

const (
	metadataMember     = "metadata.gz"
	dataMember         = "data.tar.gz"
	checksumsMember    = "checksums.yaml.gz"
	signatureSuffix    = ".sig"
	defaultMaximumSize = int64(512 << 20)
)

var (
	// ErrDuplicateMember is returned when an archive contains the same member
	// name (after path normalisation) more than once.
	ErrDuplicateMember = errors.New("netsec: duplicate gem member")
	// ErrMissingMember is returned when a required gem member is absent.
	ErrMissingMember = errors.New("netsec: missing gem member")
	// ErrUnsigned is returned by HighSecurity when the gem has no signatures.
	ErrUnsigned = errors.New("netsec: gem is not signed")
	// ErrPartialSignature is returned when only part of a signature set is
	// present. This includes removing either metadata.gz.sig or data.tar.gz.sig.
	ErrPartialSignature = errors.New("netsec: gem has a partial signature set")
	// ErrInvalidSignature is returned for a malformed signature, an unsupported
	// algorithm, or a signature that does not match its member.
	ErrInvalidSignature = errors.New("netsec: invalid gem signature")
	// ErrUntrustedCertificate is returned when a signer's certificate chain
	// does not terminate at a configured trust root.
	ErrUntrustedCertificate = errors.New("netsec: untrusted gem signing certificate")
	// ErrNoTrustRoots is returned when signature verification is required but no
	// trust roots were supplied.
	ErrNoTrustRoots = errors.New("netsec: no trust roots configured")
	// ErrChecksumMismatch is returned when checksums.yaml.gz does not describe
	// the actual archive members.
	ErrChecksumMismatch = errors.New("netsec: gem checksum mismatch")
	// ErrChecksums is returned when checksums.yaml.gz cannot be parsed.
	ErrChecksums = errors.New("netsec: invalid gem checksums")
	// ErrUnsafeMember is returned for absolute or parent-traversing member names.
	ErrUnsafeMember = errors.New("netsec: unsafe gem member name")
)

// Options configures validation. Only Security/Level and Roots are normally
// required. Signer, SignerCert, SigningCert, and Certificates are useful for
// signature formats that keep the certificate outside the .sig member.
//
// Fields are intentionally permissive so that both simple and certificate-aware
// callers can use the package.
type Options struct {
	Security SecurityLevel
	Level    SecurityLevel
	Policy   SecurityLevel
	Mode     SecurityLevel

	Roots        *x509.CertPool
	RootPool     *x509.CertPool
	TrustRoots   *x509.CertPool
	TrustedRoots *x509.CertPool
	CertPool     *x509.CertPool
	Root         *x509.Certificate
	RootCert     *x509.Certificate
	TrustAnchor  *x509.Certificate

	RootCertificates      []*x509.Certificate
	TrustRootCertificates []*x509.Certificate

	Signer      *x509.Certificate
	SignerCert  *x509.Certificate
	SigningCert *x509.Certificate

	Certificates      []*x509.Certificate
	SignerChain       []*x509.Certificate
	Chain             []*x509.Certificate
	Intermediates     []*x509.Certificate
	IntermediatesPool *x509.CertPool

	Clock func() time.Time
	Now   time.Time

	// MaxSize bounds the total uncompressed tar member size. Zero uses 512 MiB.
	MaxSize int64

	// RequireChecksumSignature also requires checksums.yaml.gz.sig when the gem
	// is signed. The default is false because the signed-gem contract requires
	// metadata.gz and data.tar.gz; checksums.yaml.gz.sig is verified when
	// present.
	RequireChecksumSignature bool
}

// Config is an alias for Options.
type Config = Options

// Checksums contains the digest maps read from checksums.yaml.gz.
type Checksums struct {
	// SHA512 maps member names to decoded SHA-512 digests.
	SHA512 map[string][]byte
	// Raw contains the textual values grouped by algorithm name.
	Raw map[string]map[string]string
}

// SignatureInfo describes a verified member signature.
type SignatureInfo struct {
	Member        string
	Algorithm     string
	SignerSubject string
	Chain         []*x509.Certificate
}

// Gem is a parsed and normally validated gem archive.
type Gem struct {
	Members map[string][]byte
	Names   []string

	MetadataGz  []byte
	DataGz      []byte
	ChecksumsGz []byte
	Checksums   *Checksums

	Signatures map[string]*SignatureInfo
	Signed     bool
	Security   SecurityLevel
}

type verifyConfig struct {
	level              SecurityLevel
	levelSet           bool
	roots              *x509.CertPool
	rootCerts          []*x509.Certificate
	signer             *x509.Certificate
	certs              []*x509.Certificate
	intermediates      *x509.CertPool
	clock              func() time.Time
	maxSize            int64
	requireChecksumSig bool
}

func (c *verifyConfig) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

func (c *verifyConfig) trustPool() *x509.CertPool {
	if c.roots != nil {
		return c.roots
	}
	pool := x509.NewCertPool()
	added := false
	for _, cert := range c.rootCerts {
		if cert != nil {
			pool.AddCert(cert)
			added = true
		}
	}
	for _, cert := range c.certs {
		if cert != nil && cert.IsCA {
			pool.AddCert(cert)
			added = true
		}
	}
	if !added {
		return nil
	}
	return pool
}

func (c *verifyConfig) addCert(cert *x509.Certificate) {
	if cert == nil {
		return
	}
	c.certs = append(c.certs, cert)
	if c.signer == nil && !cert.IsCA {
		c.signer = cert
	}
}

func (c *verifyConfig) addCerts(certs []*x509.Certificate) {
	for _, cert := range certs {
		c.addCert(cert)
	}
}

// ValidateGem validates a gem archive under the requested security level.
//
// source may be an io.Reader, []byte, or a filesystem path string. Optional
// arguments may include a SecurityLevel (or int/string equivalent), an
// *x509.CertPool of trust roots, one or more *x509.Certificate values, or an
// Options/Config value.
func ValidateGem(source any, args ...any) error {
	_, err := inspectAndValidate(source, args)
	return err
}

// VerifyGem is an alias for ValidateGem.
func VerifyGem(source any, args ...any) error {
	return ValidateGem(source, args...)
}

// CheckGem is an alias for ValidateGem.
func CheckGem(source any, args ...any) error {
	return ValidateGem(source, args...)
}

// Validate is an alias for ValidateGem.
func Validate(source any, args ...any) error {
	return ValidateGem(source, args...)
}

// Verify is an alias for ValidateGem.
func Verify(source any, args ...any) error {
	return ValidateGem(source, args...)
}

// ValidateGemFile validates a gem at path.
func ValidateGemFile(path string, args ...any) error {
	return ValidateGem(path, args...)
}

// VerifyGemFile validates a gem at path.
func VerifyGemFile(path string, args ...any) error {
	return ValidateGem(path, args...)
}

// ValidateBytes validates an in-memory gem archive.
func ValidateBytes(data []byte, args ...any) error {
	return ValidateGem(data, args...)
}

// VerifyBytes validates an in-memory gem archive.
func VerifyBytes(data []byte, args ...any) error {
	return ValidateGem(data, args...)
}

// ValidateReader validates a gem read from r.
func ValidateReader(r io.Reader, args ...any) error {
	return ValidateGem(r, args...)
}

// VerifyReader validates a gem read from r.
func VerifyReader(r io.Reader, args ...any) error {
	return ValidateGem(r, args...)
}

// InspectGem validates a gem and returns its parsed representation.
func InspectGem(source any, args ...any) (*Gem, error) {
	return inspectAndValidate(source, args)
}

// VerifyGemPackage validates a gem and returns its parsed representation.
func VerifyGemPackage(source any, args ...any) (*Gem, error) {
	return inspectAndValidate(source, args)
}

// ParseGem scans the archive, rejects duplicate/unsafe members, and parses its
// checksum map. It does not perform signature verification.
func ParseGem(source any, args ...any) (*Gem, error) {
	cfg, err := parseOptions(args)
	if err != nil {
		return nil, err
	}
	data, err := readSource(source, cfg.maxSize)
	if err != nil {
		return nil, err
	}
	gem, err := parseArchive(data, cfg.maxSize)
	if err != nil {
		return nil, err
	}
	if err := gem.loadChecksums(cfg.maxSize); err != nil {
		return nil, err
	}
	return gem, nil
}

// OpenGem is an alias for ParseGem.
func OpenGem(source any, args ...any) (*Gem, error) {
	return ParseGem(source, args...)
}

func inspectAndValidate(source any, args []any) (*Gem, error) {
	cfg, err := parseOptions(args)
	if err != nil {
		return nil, err
	}
	data, err := readSource(source, cfg.maxSize)
	if err != nil {
		return nil, err
	}
	gem, err := parseArchive(data, cfg.maxSize)
	if err != nil {
		return nil, err
	}
	if err := gem.loadChecksums(cfg.maxSize); err != nil {
		return nil, err
	}
	if err := gem.validate(cfg); err != nil {
		return nil, err
	}
	gem.Security = cfg.level
	return gem, nil
}

func readSource(source any, maxSize int64) ([]byte, error) {
	switch v := source.(type) {
	case nil:
		return nil, errors.New("netsec: nil gem source")
	case []byte:
		if maxSize > 0 && int64(len(v)) > maxSize {
			return nil, fmt.Errorf("netsec: gem exceeds maximum size of %d bytes", maxSize)
		}
		return v, nil
	case string:
		info, err := os.Stat(v)
		if err != nil {
			return nil, fmt.Errorf("netsec: open gem %q: %w", v, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("netsec: gem path %q is a directory", v)
		}
		f, err := os.Open(v)
		if err != nil {
			return nil, fmt.Errorf("netsec: open gem %q: %w", v, err)
		}
		defer f.Close()
		return readAllLimited(f, maxSize)
	case io.Reader:
		return readAllLimited(v, maxSize)
	default:
		return nil, fmt.Errorf("netsec: unsupported gem source type %T", source)
	}
}

func readAllLimited(r io.Reader, maxSize int64) ([]byte, error) {
	if maxSize <= 0 {
		maxSize = defaultMaximumSize
	}
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("netsec: read gem: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("netsec: gem exceeds maximum size of %d bytes", maxSize)
	}
	return data, nil
}

func parseOptions(args []any) (*verifyConfig, error) {
	cfg := &verifyConfig{
		level:   HighSecurity,
		maxSize: defaultMaximumSize,
	}
	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue

		case SecurityLevel:
			cfg.level = v
			cfg.levelSet = true
		case int:
			level, err := levelFromInt(v)
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case int8:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case int16:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case int32:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case int64:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case uint:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case uint8:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case uint16:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case uint32:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case uint64:
			level, err := levelFromInt(int(v))
			if err != nil {
				return nil, err
			}
			cfg.level = level
			cfg.levelSet = true
		case string:
			level, ok := parseLevelString(v)
			if !ok {
				return nil, fmt.Errorf("netsec: unknown security level %q", v)
			}
			cfg.level = level
			cfg.levelSet = true

		case *x509.CertPool:
			cfg.roots = v
		case x509.CertPool:
			pool := v
			cfg.roots = &pool
		case *x509.Certificate:
			cfg.addCert(v)
		case x509.Certificate:
			cert := v
			cfg.addCert(&cert)
		case []*x509.Certificate:
			cfg.addCerts(v)
		case []byte:
			certs, err := parseCertificateBytes(v)
			if err != nil {
				return nil, err
			}
			cfg.addCerts(certs)
		case []any:
			nested, err := parseOptions(v)
			if err != nil {
				return nil, err
			}
			mergeConfig(cfg, nested)
		case Options:
			mergeConfig(cfg, configFromOptions(v))
		case *Options:
			if v != nil {
				mergeConfig(cfg, configFromOptions(*v))
			}
		case Option:
			if v != nil {
				options := Options{}
				v(&options)
				mergeConfig(cfg, configFromOptions(options))
			}
		case []Option:
			for _, option := range v {
				if option == nil {
					continue
				}
				options := Options{}
				option(&options)
				mergeConfig(cfg, configFromOptions(options))
			}
		case func() time.Time:
			cfg.clock = v
		case time.Time:
			at := v
			cfg.clock = func() time.Time { return at }
		default:
			return nil, fmt.Errorf("netsec: unsupported option type %T", arg)
		}
	}
	if cfg.maxSize <= 0 {
		cfg.maxSize = defaultMaximumSize
	}
	return cfg, nil
}

func levelFromInt(v int) (SecurityLevel, error) {
	switch SecurityLevel(v) {
	case HighSecurity:
		return HighSecurity, nil
	case MediumSecurity:
		return MediumSecurity, nil
	default:
		return 0, fmt.Errorf("netsec: unknown security level %d", v)
	}
}

func parseLevelString(s string) (SecurityLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high", "highsecurity", "high-security", "high_security", "strict":
		return HighSecurity, true
	case "medium", "mediumsecurity", "medium-security", "medium_security":
		return MediumSecurity, true
	case "low", "lowsecurity", "low-security", "low_security":
		return MediumSecurity, true
	default:
		return 0, false
	}
}

func configFromOptions(o Options) *verifyConfig {
	cfg := &verifyConfig{
		level:              o.Security,
		levelSet:           true,
		maxSize:            o.MaxSize,
		clock:              o.Clock,
		requireChecksumSig: o.RequireChecksumSignature,
	}
	if o.Level != HighSecurity {
		cfg.level = o.Level
	}
	if o.Policy != HighSecurity {
		cfg.level = o.Policy
	}
	if o.Mode != HighSecurity {
		cfg.level = o.Mode
	}
	switch {
	case o.Roots != nil:
		cfg.roots = o.Roots
	case o.RootPool != nil:
		cfg.roots = o.RootPool
	case o.TrustRoots != nil:
		cfg.roots = o.TrustRoots
	case o.TrustedRoots != nil:
		cfg.roots = o.TrustedRoots
	case o.CertPool != nil:
		cfg.roots = o.CertPool
	}
	for _, cert := range []*x509.Certificate{o.Root, o.RootCert, o.TrustAnchor} {
		if cert != nil {
			cfg.certs = append(cfg.certs, cert)
			cfg.rootCerts = append(cfg.rootCerts, cert)
		}
	}
	cfg.certs = append(cfg.certs, o.RootCertificates...)
	cfg.certs = append(cfg.certs, o.TrustRootCertificates...)
	cfg.rootCerts = append(cfg.rootCerts, o.RootCertificates...)
	cfg.rootCerts = append(cfg.rootCerts, o.TrustRootCertificates...)
	switch {
	case o.Signer != nil:
		cfg.signer = o.Signer
	case o.SignerCert != nil:
		cfg.signer = o.SignerCert
	case o.SigningCert != nil:
		cfg.signer = o.SigningCert
	}
	if o.Signer != nil {
		cfg.certs = append(cfg.certs, o.Signer)
	}
	if o.SignerCert != nil {
		cfg.certs = append(cfg.certs, o.SignerCert)
	}
	if o.SigningCert != nil {
		cfg.certs = append(cfg.certs, o.SigningCert)
	}
	cfg.certs = append(cfg.certs, o.Certificates...)
	cfg.certs = append(cfg.certs, o.SignerChain...)
	cfg.certs = append(cfg.certs, o.Chain...)
	cfg.certs = append(cfg.certs, o.Intermediates...)
	cfg.intermediates = o.IntermediatesPool
	if cfg.clock == nil && !o.Now.IsZero() {
		at := o.Now
		cfg.clock = func() time.Time { return at }
	}
	return cfg
}

func mergeConfig(dst, src *verifyConfig) {
	if src == nil {
		return
	}
	if src.levelSet {
		dst.level = src.level
		dst.levelSet = true
	}
	if src.roots != nil {
		dst.roots = src.roots
	}
	dst.rootCerts = append(dst.rootCerts, src.rootCerts...)
	if src.signer != nil {
		dst.signer = src.signer
	}
	dst.certs = append(dst.certs, src.certs...)
	if src.intermediates != nil {
		dst.intermediates = src.intermediates
	}
	if src.clock != nil {
		dst.clock = src.clock
	}
	if src.maxSize > 0 {
		dst.maxSize = src.maxSize
	}
	if src.requireChecksumSig {
		dst.requireChecksumSig = true
	}
}

func parseCertificateBytes(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("netsec: parse certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) > 0 {
		return certs, nil
	}
	cert, err := x509.ParseCertificate(data)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse certificate bytes: %w", err)
	}
	return []*x509.Certificate{cert}, nil
}

func parseArchive(data []byte, maxSize int64) (*Gem, error) {
	if maxSize <= 0 {
		maxSize = defaultMaximumSize
	}
	tr := tar.NewReader(bytes.NewReader(data))
	gem := &Gem{
		Members:    make(map[string][]byte),
		Signatures: make(map[string]*SignatureInfo),
	}
	seen := make(map[string]string)
	var total int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("netsec: read gem tar: %w", err)
		}
		name, err := cleanMemberName(hdr.Name)
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[name]; ok {
			return nil, fmt.Errorf("%w: %q and %q normalise to %q",
				ErrDuplicateMember, previous, hdr.Name, name)
		}
		seen[name] = hdr.Name

		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			if hdr.Typeflag == tar.TypeDir {
				continue
			}
			return nil, fmt.Errorf("netsec: gem member %q is not a regular file", hdr.Name)
		}
		if hdr.Size < 0 || hdr.Size > maxSize || total > maxSize-hdr.Size {
			return nil, fmt.Errorf("netsec: gem member %q exceeds maximum size", hdr.Name)
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size+1))
		if err != nil {
			return nil, fmt.Errorf("netsec: read gem member %q: %w", hdr.Name, err)
		}
		if int64(len(content)) != hdr.Size {
			return nil, fmt.Errorf("netsec: gem member %q has inconsistent size", hdr.Name)
		}
		total += hdr.Size
		gem.Members[name] = content
		gem.Names = append(gem.Names, name)
	}

	gem.MetadataGz = gem.Members[metadataMember]
	gem.DataGz = gem.Members[dataMember]
	gem.ChecksumsGz = gem.Members[checksumsMember]
	return gem, nil
}

func cleanMemberName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("%w: empty or NUL-containing name", ErrUnsafeMember)
	}
	clean := path.Clean(name)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q", ErrUnsafeMember, name)
	}
	return clean, nil
}

func (g *Gem) loadChecksums(maxSize int64) error {
	if len(g.ChecksumsGz) == 0 {
		return fmt.Errorf("%w: %s", ErrMissingMember, checksumsMember)
	}
	zr, err := gzip.NewReader(bytes.NewReader(g.ChecksumsGz))
	if err != nil {
		return fmt.Errorf("%w: gzip checksums: %v", ErrChecksums, err)
	}
	defer zr.Close()
	if maxSize <= 0 {
		maxSize = defaultMaximumSize
	}
	data, err := io.ReadAll(io.LimitReader(zr, maxSize+1))
	if err != nil {
		return fmt.Errorf("%w: decompress checksums: %v", ErrChecksums, err)
	}
	if int64(len(data)) > maxSize {
		return fmt.Errorf("%w: checksums file exceeds maximum size", ErrChecksums)
	}
	checksums, err := parseChecksums(data)
	if err != nil {
		return err
	}
	g.Checksums = checksums
	return nil
}

func parseChecksums(data []byte) (*Checksums, error) {
	result := &Checksums{
		SHA512: make(map[string][]byte),
		Raw:    make(map[string]map[string]string),
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	section := ""
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		line = strings.TrimPrefix(line, "\ufeff")
		line = strings.TrimSpace(line)
		if line == "" || line == "---" || line == "..." || strings.HasPrefix(line, "#") {
			continue
		}
		line = stripYAMLComment(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") {
			continue
		}
		keyPart, valuePart, ok := splitYAMLKeyValue(line)
		if !ok {
			continue
		}
		keyPart = unquoteYAML(keyPart)
		valuePart = unquoteYAML(valuePart)
		if valuePart == "" && isChecksumAlgorithm(keyPart) {
			section = strings.ToUpper(keyPart)
			if result.Raw[section] == nil {
				result.Raw[section] = make(map[string]string)
			}
			continue
		}
		if section == "" {
			continue
		}
		if result.Raw[section] == nil {
			result.Raw[section] = make(map[string]string)
		}
		result.Raw[section][keyPart] = valuePart
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: read YAML: %v", ErrChecksums, err)
	}

	rawSHA512 := result.Raw["SHA512"]
	if len(rawSHA512) == 0 {
		return nil, fmt.Errorf("%w: no SHA512 section", ErrChecksums)
	}
	for member, value := range rawSHA512 {
		digest, err := decodeDigest(value)
		if err != nil {
			return nil, fmt.Errorf("%w: bad SHA512 for %q: %v", ErrChecksums, member, err)
		}
		clean, err := cleanMemberName(member)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrChecksums, err)
		}
		result.SHA512[clean] = digest
	}
	return result, nil
}

func isChecksumAlgorithm(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "SHA1", "SHA256", "SHA384", "SHA512":
		return true
	default:
		return false
	}
}

func stripYAMLComment(line string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return strings.TrimSpace(line[:i])
			}
		}
	}
	return strings.TrimSpace(line)
}

func splitYAMLKeyValue(line string) (string, string, bool) {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ':':
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
		}
	}
	return "", "", false
}

func unquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			body := s[1 : len(s)-1]
			if s[0] == '"' {
				if unquoted, err := strconvUnquote(body); err == nil {
					return unquoted
				}
			}
			return strings.ReplaceAll(body, "''", "'")
		}
	}
	return s
}

func strconvUnquote(s string) (string, error) {
	// Keep the YAML parser dependency-free; implement the common escapes.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"', '\\', '/':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func decodeDigest(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "0x")
	if len(value) == sha512.Size*2 {
		if digest, err := hex.DecodeString(value); err == nil {
			return digest, nil
		}
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		digest, err := encoding.DecodeString(value)
		if err == nil && len(digest) == sha512.Size {
			return digest, nil
		}
	}
	return nil, fmt.Errorf("expected a 64-byte SHA-512 digest")
}

func (g *Gem) validate(cfg *verifyConfig) error {
	if len(g.MetadataGz) == 0 {
		return fmt.Errorf("%w: %s", ErrMissingMember, metadataMember)
	}
	if len(g.DataGz) == 0 {
		return fmt.Errorf("%w: %s", ErrMissingMember, dataMember)
	}
	if len(g.ChecksumsGz) == 0 {
		return fmt.Errorf("%w: %s", ErrMissingMember, checksumsMember)
	}

	hasMetadataSig := len(g.Members[metadataMember+signatureSuffix]) > 0
	hasDataSig := len(g.Members[dataMember+signatureSuffix]) > 0
	hasChecksumsSig := len(g.Members[checksumsMember+signatureSuffix]) > 0

	coreSigned := hasMetadataSig && hasDataSig
	coreAbsent := !hasMetadataSig && !hasDataSig

	if hasMetadataSig != hasDataSig {
		return fmt.Errorf("%w: %s=%t, %s=%t", ErrPartialSignature,
			metadataMember+signatureSuffix, hasMetadataSig,
			dataMember+signatureSuffix, hasDataSig)
	}
	if hasChecksumsSig && !coreSigned {
		return fmt.Errorf("%w: %s present without metadata/data signatures",
			ErrPartialSignature, checksumsMember+signatureSuffix)
	}
	if cfg.requireChecksumSig && coreSigned && !hasChecksumsSig {
		return fmt.Errorf("%w: %s is missing", ErrPartialSignature, checksumsMember+signatureSuffix)
	}

	switch cfg.level {
	case HighSecurity:
		if !coreSigned {
			return fmt.Errorf("%w: HighSecurity requires %s and %s",
				ErrUnsigned, metadataMember+signatureSuffix, dataMember+signatureSuffix)
		}
	case MediumSecurity:
		if coreAbsent && !hasChecksumsSig {
			// MediumSecurity deliberately permits wholly unsigned gems.
		}
	default:
		return fmt.Errorf("netsec: unknown security level %d", cfg.level)
	}

	if coreSigned {
		if err := g.verifySignature(metadataMember, cfg); err != nil {
			return err
		}
		if err := g.verifySignature(dataMember, cfg); err != nil {
			return err
		}
		if hasChecksumsSig {
			if err := g.verifySignature(checksumsMember, cfg); err != nil {
				return err
			}
		}
	}
	g.Signed = coreSigned

	return g.verifyChecksums()
}

func (g *Gem) verifySignature(member string, cfg *verifyConfig) error {
	content, ok := g.Members[member]
	if !ok {
		return fmt.Errorf("%w: %s", ErrMissingMember, member)
	}
	signature, ok := g.Members[member+signatureSuffix]
	if !ok || len(signature) == 0 {
		return fmt.Errorf("%w: %s%s", ErrPartialSignature, member, signatureSuffix)
	}
	info, err := verifyDetachedSignature(signature, content, cfg, member)
	if err != nil {
		return err
	}
	info.Member = member
	g.Signatures[member] = info
	return nil
}

func (g *Gem) verifyChecksums() error {
	if g.Checksums == nil {
		return fmt.Errorf("%w: checksums were not loaded", ErrChecksums)
	}
	if len(g.Checksums.SHA512) == 0 {
		return fmt.Errorf("%w: no SHA512 entries", ErrChecksums)
	}
	for _, required := range []string{metadataMember, dataMember} {
		if _, ok := g.Checksums.SHA512[required]; !ok {
			return fmt.Errorf("%w: no SHA512 entry for %s", ErrChecksumMismatch, required)
		}
	}
	for member, expected := range g.Checksums.SHA512 {
		actual, ok := g.Members[member]
		if !ok {
			return fmt.Errorf("%w: checksum names missing member %q", ErrChecksumMismatch, member)
		}
		sum := sha512.Sum512(actual)
		if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
			return fmt.Errorf("%w: %s", ErrChecksumMismatch, member)
		}
	}
	return nil
}
