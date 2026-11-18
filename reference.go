package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrInvalidReference is returned when an image reference is malformed.
	ErrInvalidReference = errors.New("invalid image reference")
	// ErrInvalidDigest is returned when a digest is malformed.
	ErrInvalidDigest = errors.New("invalid digest")
	// ErrInvalidScope is returned when a registry scope is malformed.
	ErrInvalidScope = errors.New("invalid registry scope")
)

// ImageReference is a parsed OCI image reference. Tag and Digest are optional;
// at least a registry and repository are required.
type ImageReference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
	Original   string
}

// ParseImageReference parses a reference of the form
//
//	registry/repository[:tag][@algorithm:encoded]
//
// The registry is mandatory. Wildcards, tags without a value and malformed
// digests are rejected. The parser deliberately does not attempt to resolve
// implicit Docker Hub registries: a trust policy must name its registry
// explicitly.
func ParseImageReference(ref string) (ImageReference, error) {
	if ref == "" {
		return ImageReference{}, fmt.Errorf("%w: empty reference", ErrInvalidReference)
	}
	if ref != strings.TrimSpace(ref) {
		return ImageReference{}, fmt.Errorf("%w: surrounding whitespace", ErrInvalidReference)
	}
	if strings.Contains(ref, "://") {
		return ImageReference{}, fmt.Errorf("%w: schemes are not allowed", ErrInvalidReference)
	}
	if strings.ContainsAny(ref, " \t\r\n\x00") {
		return ImageReference{}, fmt.Errorf("%w: control or whitespace characters", ErrInvalidReference)
	}

	original := ref
	namePart := ref
	digest := ""
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		if strings.ContainsRune(ref[at+1:], '@') {
			return ImageReference{}, fmt.Errorf("%w: multiple digest separators", ErrInvalidReference)
		}
		namePart = ref[:at]
		digest = ref[at+1:]
		if namePart == "" {
			return ImageReference{}, fmt.Errorf("%w: missing repository before digest", ErrInvalidReference)
		}
		if digest == "" {
			return ImageReference{}, fmt.Errorf("%w: empty digest", ErrInvalidReference)
		}
		if _, err := normalizeDigest(digest); err != nil {
			return ImageReference{}, fmt.Errorf("%w: %w", ErrInvalidReference, err)
		}
	}

	tag := ""
	lastSlash := strings.LastIndexByte(namePart, '/')
	if colon := strings.LastIndexByte(namePart, ':'); colon > lastSlash {
		tag = namePart[colon+1:]
		namePart = namePart[:colon]
		if tag == "" {
			return ImageReference{}, fmt.Errorf("%w: empty tag", ErrInvalidReference)
		}
		if err := validateTag(tag); err != nil {
			return ImageReference{}, fmt.Errorf("%w: %v", ErrInvalidReference, err)
		}
	}

	slash := strings.IndexByte(namePart, '/')
	if slash < 0 {
		return ImageReference{}, fmt.Errorf("%w: reference must have registry/repository form", ErrInvalidReference)
	}
	if slash == 0 || slash == len(namePart)-1 {
		return ImageReference{}, fmt.Errorf("%w: empty registry or repository", ErrInvalidReference)
	}

	registry, err := normalizeRegistry(namePart[:slash])
	if err != nil {
		return ImageReference{}, fmt.Errorf("%w: %v", ErrInvalidReference, err)
	}
	repository := namePart[slash+1:]
	if err := validateRepository(repository); err != nil {
		return ImageReference{}, fmt.Errorf("%w: %v", ErrInvalidReference, err)
	}

	return ImageReference{
		Registry:   registry,
		Repository: repository,
		Tag:        tag,
		Digest:     digest,
		Original:   original,
	}, nil
}

// Name returns the registry/repository portion of the reference. Tags and
// digests are intentionally excluded because registry scopes never match them.
func (r ImageReference) Name() string {
	registry := strings.ToLower(strings.TrimSpace(r.Registry))
	if registry == "" {
		return r.Repository
	}
	if r.Repository == "" {
		return registry
	}
	return registry + "/" + r.Repository
}

// String reconstructs the reference. The exact input is returned when the
// reference came from ParseImageReference.
func (r ImageReference) String() string {
	if r.Original != "" {
		return r.Original
	}
	var b strings.Builder
	b.WriteString(r.Name())
	if r.Tag != "" {
		b.WriteByte(':')
		b.WriteString(r.Tag)
	}
	if r.Digest != "" {
		b.WriteByte('@')
		b.WriteString(r.Digest)
	}
	return b.String()
}

// NormalizeDigest validates a digest and returns its canonical form. For
// SHA-2 digests this means lower-cased hexadecimal.
func NormalizeDigest(digest string) (string, error) {
	return normalizeDigest(digest)
}

// DigestsEqual reports whether two digests identify the same content.
func DigestsEqual(a, b string) bool {
	na, err := normalizeDigest(a)
	if err != nil {
		return false
	}
	nb, err := normalizeDigest(b)
	if err != nil {
		return false
	}
	return na == nb
}

func normalizeDigest(digest string) (string, error) {
	if digest == "" {
		return "", fmt.Errorf("%w: empty digest", ErrInvalidDigest)
	}
	if digest != strings.TrimSpace(digest) {
		return "", fmt.Errorf("%w: surrounding whitespace", ErrInvalidDigest)
	}
	colon := strings.IndexByte(digest, ':')
	if colon <= 0 || colon == len(digest)-1 {
		return "", fmt.Errorf("%w: expected algorithm:encoded", ErrInvalidDigest)
	}
	algorithm := strings.ToLower(digest[:colon])
	encoded := digest[colon+1:]
	if !validDigestAlgorithm(algorithm) {
		return "", fmt.Errorf("%w: invalid algorithm %q", ErrInvalidDigest, algorithm)
	}
	if !validDigestEncoded(encoded) {
		return "", fmt.Errorf("%w: invalid encoded value", ErrInvalidDigest)
	}

	switch algorithm {
	case "sha256":
		if len(encoded) != 64 || !isHex(encoded) {
			return "", fmt.Errorf("%w: sha256 requires 64 hexadecimal characters", ErrInvalidDigest)
		}
		encoded = strings.ToLower(encoded)
	case "sha384":
		if len(encoded) != 96 || !isHex(encoded) {
			return "", fmt.Errorf("%w: sha384 requires 96 hexadecimal characters", ErrInvalidDigest)
		}
		encoded = strings.ToLower(encoded)
	case "sha512":
		if len(encoded) != 128 || !isHex(encoded) {
			return "", fmt.Errorf("%w: sha512 requires 128 hexadecimal characters", ErrInvalidDigest)
		}
		encoded = strings.ToLower(encoded)
	default:
		// An unverifiable content-addressing algorithm must not be treated as
		// a signature binding. Only the SHA-2 family is accepted.
		return "", fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidDigest, algorithm)
	}

	return algorithm + ":" + encoded, nil
}

func validDigestAlgorithm(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '+', '.', '_', '-':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validDigestEncoded(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '=', '_', '-':
		default:
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return false
	}
	return true
}

func validateTag(tag string) error {
	if tag == "" || len(tag) > 128 {
		return fmt.Errorf("invalid tag length")
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '_', '.', '-':
		default:
			return fmt.Errorf("invalid tag character %q", c)
		}
	}
	first := tag[0]
	if first == '.' || first == '-' {
		return fmt.Errorf("tag must start with an alphanumeric character or underscore")
	}
	return nil
}

func normalizeRegistry(registry string) (string, error) {
	if registry == "" {
		return "", fmt.Errorf("empty registry")
	}
	if strings.ContainsAny(registry, "/@* \t\r\n\x00") {
		return "", fmt.Errorf("invalid registry %q", registry)
	}

	host := registry
	port := ""

	if strings.HasPrefix(registry, "[") {
		closeBracket := strings.IndexByte(registry, ']')
		if closeBracket <= 1 {
			return "", fmt.Errorf("invalid IPv6 registry %q", registry)
		}
		host = registry[:closeBracket+1]
		rest := registry[closeBracket+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") || len(rest) == 1 {
				return "", fmt.Errorf("invalid registry port in %q", registry)
			}
			port = rest[1:]
		}
		inside := registry[1:closeBracket]
		if !strings.Contains(inside, ":") {
			return "", fmt.Errorf("invalid IPv6 registry %q", registry)
		}
		for i := 0; i < len(inside); i++ {
			c := inside[i]
			if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == ':' || c == '.' {
				continue
			}
			return "", fmt.Errorf("invalid IPv6 registry %q", registry)
		}
	} else {
		if strings.Count(registry, ":") > 1 {
			return "", fmt.Errorf("IPv6 registries must use brackets: %q", registry)
		}
		if colon := strings.LastIndexByte(registry, ':'); colon >= 0 {
			host = registry[:colon]
			port = registry[colon+1:]
			if host == "" || port == "" {
				return "", fmt.Errorf("invalid registry port in %q", registry)
			}
		}
		if host == "" {
			return "", fmt.Errorf("empty registry host")
		}
		for i := 0; i < len(host); i++ {
			c := host[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' {
				continue
			}
			return "", fmt.Errorf("invalid registry host %q", host)
		}
	}

	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", fmt.Errorf("invalid registry port %q", port)
		}
	}

	return strings.ToLower(registry), nil
}

func validateRepository(repository string) error {
	if repository == "" {
		return fmt.Errorf("empty repository")
	}
	if strings.ContainsAny(repository, "*@: \t\r\n\x00") {
		return fmt.Errorf("invalid repository %q", repository)
	}
	components := strings.Split(repository, "/")
	for _, component := range components {
		if component == "" {
			return fmt.Errorf("empty repository path component")
		}
		if component == "." || component == ".." {
			return fmt.Errorf("invalid repository path component %q", component)
		}
		if !isAlphaNumeric(component[0]) || !isAlphaNumeric(component[len(component)-1]) {
			return fmt.Errorf("repository component %q must start and end with an alphanumeric character", component)
		}
		for i := 0; i < len(component); i++ {
			c := component[i]
			if isAlphaNumeric(c) || c == '.' || c == '_' || c == '-' {
				continue
			}
			return fmt.Errorf("invalid repository character %q", c)
		}
	}
	return nil
}

func isAlphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
