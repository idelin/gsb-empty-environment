package netsec

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const defaultImageRegistry = "docker.io"

var (
	imagePathComponentRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
	imageTagRE           = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	hexRE                = regexp.MustCompile(`^[0-9a-f]+$`)
	gitCommitRE          = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	registryHostRE       = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)
	ipv6HostRE           = regexp.MustCompile(`^[0-9a-f:]+$`)
)

func canonicalizeSource(explicit SourceKind, source string, attrs map[string]string) (SourceKind, string, error) {
	if explicit != "" && !explicit.valid() {
		return explicit, "", fmt.Errorf("invalid source kind %q", explicit)
	}
	if err := validateIdentityText(source); err != nil {
		return explicit, "", err
	}

	kind := explicit
	if kind == "" {
		kind = inferKind(source, attrs)
	}
	canonical, err := canonicalizeAsKind(kind, source, attrs)
	if err != nil {
		return kind, "", err
	}
	return kind, canonical, nil
}

func inferKind(source string, attrs map[string]string) SourceKind {
	lower := strings.ToLower(source)
	switch {
	case strings.HasPrefix(lower, "docker-image://"), strings.HasPrefix(lower, "image://"):
		return KindImage
	case strings.HasPrefix(lower, "local://"), strings.HasPrefix(lower, "context://"):
		return KindLocal
	case strings.HasPrefix(lower, "git://"), strings.HasPrefix(lower, "git+ssh://"),
		strings.HasPrefix(lower, "git+https://"), strings.HasPrefix(lower, "git+http://"),
		strings.HasPrefix(lower, "ssh://"):
		return KindGit
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if hasGitAttributes(attrs) {
			return KindGit
		}
		return KindHTTP
	default:
		if hasGitAttributes(attrs) {
			return KindGit
		}
		return KindImage
	}
}

func hasGitAttributes(attrs map[string]string) bool {
	for key := range attrs {
		if strings.HasPrefix(strings.ToLower(key), "git.") {
			return true
		}
	}
	return false
}

func canonicalizeAsKind(kind SourceKind, value string, attrs map[string]string) (string, error) {
	if err := validateIdentityText(value); err != nil {
		return "", err
	}
	switch kind {
	case KindImage:
		return canonicalImageReference(value)
	case KindGit:
		return canonicalGitReference(value, attrs)
	case KindHTTP:
		return canonicalHTTPReference(value, attrs)
	case KindLocal:
		return canonicalLocalReference(value)
	default:
		return "", fmt.Errorf("unsupported source kind %q", kind)
	}
}

func validateIdentityText(value string) error {
	if value == "" {
		return fmt.Errorf("empty source identifier")
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("source identifier contains leading or trailing whitespace")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("source identifier contains whitespace or control characters")
		}
	}
	return nil
}

// canonicalImageReference accepts references with or without a registry and
// returns a fully qualified, lower-case reference. A digest always replaces a
// tag when both are present.
func canonicalImageReference(raw string) (string, error) {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "docker-image://") {
		raw = raw[len("docker-image://"):]
	} else if strings.HasPrefix(lower, "image://") {
		raw = raw[len("image://"):]
	}
	if raw == "" {
		return "", fmt.Errorf("empty image reference")
	}
	if strings.Contains(raw, "://") {
		return "", fmt.Errorf("image reference contains an unexpected URL scheme")
	}
	if strings.Count(raw, "@") > 1 {
		return "", fmt.Errorf("image reference contains multiple @ separators")
	}

	var digest string
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		digestPart := raw[at+1:]
		raw = raw[:at]
		if raw == "" || digestPart == "" {
			return "", fmt.Errorf("malformed image digest reference")
		}
		canonical, err := canonicalDigest(digestPart)
		if err != nil {
			return "", fmt.Errorf("invalid image digest: %w", err)
		}
		digest = canonical
	}

	lastSlash := strings.LastIndex(raw, "/")
	lastColon := strings.LastIndex(raw, ":")
	var tag string
	if lastColon > lastSlash {
		tag = raw[lastColon+1:]
		raw = raw[:lastColon]
		if tag == "" {
			return "", fmt.Errorf("empty image tag")
		}
		if !imageTagRE.MatchString(tag) {
			return "", fmt.Errorf("invalid image tag %q", tag)
		}
	}
	if raw == "" {
		return "", fmt.Errorf("image repository is empty")
	}

	var registry, repository string
	if slash := strings.Index(raw, "/"); slash >= 0 {
		first := raw[:slash]
		if isRegistryComponent(first) {
			registry = first
			repository = raw[slash+1:]
		} else {
			registry = defaultImageRegistry
			repository = raw
		}
	} else {
		registry = defaultImageRegistry
		repository = raw
	}
	registry = strings.ToLower(registry)
	switch registry {
	case "index.docker.io", "registry-1.docker.io":
		registry = defaultImageRegistry
	}
	if err := validateRegistry(registry); err != nil {
		return "", err
	}
	if registry == defaultImageRegistry && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	if err := validateImageRepository(repository); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(registry)
	b.WriteByte('/')
	b.WriteString(repository)
	if digest != "" {
		b.WriteByte('@')
		b.WriteString(digest)
	} else if tag != "" {
		b.WriteByte(':')
		b.WriteString(tag)
	}
	return b.String(), nil
}

func isRegistryComponent(value string) bool {
	return value == "localhost" || strings.ContainsAny(value, ".:")
}

func validateRegistry(registry string) error {
	if registry == "" {
		return fmt.Errorf("empty image registry")
	}
	if strings.ContainsAny(registry, "@/?#\\") {
		return fmt.Errorf("invalid image registry %q", registry)
	}
	if strings.HasPrefix(registry, ".") || strings.HasSuffix(registry, ".") || strings.Contains(registry, "..") {
		return fmt.Errorf("invalid image registry %q", registry)
	}

	host, port, err := splitRegistryPort(registry)
	if err != nil {
		return err
	}
	if strings.HasPrefix(host, "[") {
		inner := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if !strings.HasSuffix(host, "]") || !ipv6HostRE.MatchString(inner) {
			return fmt.Errorf("invalid IPv6 image registry %q", host)
		}
	} else if !registryHostRE.MatchString(host) {
		return fmt.Errorf("invalid image registry host %q", host)
	}
	if port != "" {
		portNumber := 0
		for _, r := range port {
			if r < '0' || r > '9' {
				return fmt.Errorf("invalid image registry port in %q", registry)
			}
			portNumber = portNumber*10 + int(r-'0')
		}
		if portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("invalid image registry port in %q", registry)
		}
	}
	return nil
}

func splitRegistryPort(registry string) (host, port string, err error) {
	if strings.HasPrefix(registry, "[") {
		end := strings.Index(registry, "]")
		if end < 0 {
			return "", "", fmt.Errorf("invalid IPv6 image registry %q", registry)
		}
		host = registry[:end+1]
		rest := registry[end+1:]
		if rest == "" {
			return host, "", nil
		}
		if !strings.HasPrefix(rest, ":") || len(rest) == 1 {
			return "", "", fmt.Errorf("invalid image registry %q", registry)
		}
		return host, rest[1:], nil
	}
	if strings.Count(registry, ":") == 0 {
		return registry, "", nil
	}
	if strings.Count(registry, ":") > 1 {
		return "", "", fmt.Errorf("invalid image registry %q", registry)
	}
	host, port, _ = strings.Cut(registry, ":")
	if host == "" || port == "" {
		return "", "", fmt.Errorf("invalid image registry %q", registry)
	}
	return host, port, nil
}

func validateImageRepository(repository string) error {
	if repository == "" {
		return fmt.Errorf("empty image repository")
	}
	for _, component := range strings.Split(repository, "/") {
		if component == "" {
			return fmt.Errorf("empty component in image repository %q", repository)
		}
		if component == "." || component == ".." || !imagePathComponentRE.MatchString(component) {
			return fmt.Errorf("invalid image repository component %q", component)
		}
	}
	return nil
}

func canonicalDigest(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty digest")
	}
	if strings.TrimSpace(raw) != raw || strings.Contains(raw, "@") {
		return "", fmt.Errorf("malformed digest")
	}
	algorithm, encoded, ok := strings.Cut(raw, ":")
	if !ok || algorithm == "" || encoded == "" {
		return "", fmt.Errorf("digest must have algorithm:encoded form")
	}
	algorithm = strings.ToLower(algorithm)
	encoded = strings.ToLower(encoded)
	if !hexRE.MatchString(encoded) {
		return "", fmt.Errorf("digest encoding is not lower-case hexadecimal")
	}

	var expected int
	switch algorithm {
	case "sha256":
		expected = 64
	case "sha384":
		expected = 96
	case "sha512":
		expected = 128
	default:
		return "", fmt.Errorf("unsupported digest algorithm %q", algorithm)
	}
	if len(encoded) != expected {
		return "", fmt.Errorf("digest %s must contain %d hexadecimal characters", algorithm, expected)
	}
	return algorithm + ":" + encoded, nil
}

func canonicalGitReference(raw string, attrs map[string]string) (string, error) {
	original, err := parseGitURL(raw)
	if err != nil {
		return "", err
	}

	base := original
	fullURLs, err := attributeValues(attrs, "git.fullurl", "git.url")
	if err != nil {
		return "", err
	}
	if len(fullURLs) > 1 {
		first, err := parseGitURL(fullURLs[0])
		if err != nil {
			return "", fmt.Errorf("invalid git.fullurl: %w", err)
		}
		for _, candidate := range fullURLs[1:] {
			parsed, err := parseGitURL(candidate)
			if err != nil {
				return "", fmt.Errorf("invalid git.fullurl: %w", err)
			}
			if parsed.String() != first.String() {
				return "", fmt.Errorf("conflicting git.fullurl attributes")
			}
		}
		base = first
	} else if len(fullURLs) == 1 {
		parsed, err := parseGitURL(fullURLs[0])
		if err != nil {
			return "", fmt.Errorf("invalid git.fullurl: %w", err)
		}
		base = parsed
	}

	refs := make([]string, 0, 4)
	for _, candidate := range []*url.URL{original, base} {
		if candidate.Fragment == "" {
			continue
		}
		ref, _, err := splitGitFragment(candidate.Fragment)
		if err != nil {
			return "", err
		}
		refs = append(refs, ref)
	}
	attrRefs, err := attributeValues(
		attrs,
		"git.ref", "git.commit", "ref", "commit",
		"git.branch", "branch", "git.tag", "tag",
	)
	if err != nil {
		return "", err
	}
	refs = append(refs, attrRefs...)
	if len(refs) == 0 {
		return "", fmt.Errorf("git source is not pinned to a commit")
	}

	var pinned string
	for _, ref := range refs {
		if err := validateIdentityText(ref); err != nil {
			return "", fmt.Errorf("invalid git ref %q: %w", ref, err)
		}
		normalized := strings.ToLower(ref)
		if !gitCommitRE.MatchString(normalized) {
			return "", fmt.Errorf("git ref %q is mutable; a full commit hash is required", ref)
		}
		if pinned == "" {
			pinned = normalized
		} else if pinned != normalized {
			return "", fmt.Errorf("conflicting git commit references")
		}
	}

	subdir := ""
	if original.Fragment != "" {
		_, subdir, _ = splitGitFragment(original.Fragment)
	}
	if base.Fragment != "" {
		_, subdir, _ = splitGitFragment(base.Fragment)
	}
	base.Fragment = pinned
	if subdir != "" {
		base.Fragment = pinned + ":" + subdir
	}
	base.RawFragment = ""
	return base.String(), nil
}

func parseGitURL(raw string) (*url.URL, error) {
	if err := validateIdentityText(raw); err != nil {
		return nil, fmt.Errorf("invalid git URL: %w", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid git URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "git", "ssh", "git+ssh", "http", "https", "git+http", "git+https":
	default:
		return nil, fmt.Errorf("unsupported git URL scheme %q", u.Scheme)
	}
	if u.Opaque != "" || u.Host == "" {
		return nil, fmt.Errorf("git URL must include a host")
	}
	if u.User != nil {
		return nil, fmt.Errorf("git URL must not contain user information")
	}
	if strings.Contains(raw, "\\") {
		return nil, fmt.Errorf("git URL must not contain backslashes")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func splitGitFragment(fragment string) (ref, subdir string, err error) {
	ref, subdir, _ = strings.Cut(fragment, ":")
	if ref == "" {
		return "", "", fmt.Errorf("empty git ref in URL fragment")
	}
	if err := validateIdentityText(ref); err != nil {
		return "", "", err
	}
	if subdir != "" {
		if err := validateIdentityText(subdir); err != nil {
			return "", "", err
		}
	}
	return ref, subdir, nil
}

func canonicalHTTPReference(raw string, attrs map[string]string) (string, error) {
	if err := validateIdentityText(raw); err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid HTTP URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("HTTP source must use http or https")
	}
	if u.Opaque != "" || u.Host == "" {
		return "", fmt.Errorf("HTTP URL must include a host")
	}
	if u.User != nil {
		return "", fmt.Errorf("HTTP URL must not contain user information")
	}
	if strings.Contains(raw, "\\") {
		return "", fmt.Errorf("HTTP URL must not contain backslashes")
	}

	checksums, err := attributeValues(attrs, "http.checksum", "http.checksum.sha256", "http.chksum", "checksum")
	if err != nil {
		return "", err
	}
	if len(checksums) == 0 {
		return "", fmt.Errorf("HTTP source requires a checksum")
	}
	var canonicalChecksum string
	for _, checksum := range checksums {
		canonical, err := canonicalDigest(checksum)
		if err != nil {
			return "", fmt.Errorf("invalid HTTP checksum: %w", err)
		}
		if canonicalChecksum == "" {
			canonicalChecksum = canonical
		} else if canonicalChecksum != canonical {
			return "", fmt.Errorf("conflicting HTTP checksum attributes")
		}
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawFragment = ""
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	return u.String(), nil
}

func canonicalLocalReference(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, "local://"):
		context := strings.TrimPrefix(raw, "local://")
		if context == "" {
			return "", fmt.Errorf("empty local context identifier")
		}
		return "local://" + context, nil
	case strings.HasPrefix(raw, "context://"):
		context := strings.TrimPrefix(raw, "context://")
		if context == "" {
			return "", fmt.Errorf("empty local context identifier")
		}
		return "local://" + context, nil
	}
	if raw == "" {
		return "", fmt.Errorf("empty local context identifier")
	}
	return raw, nil
}

func attributeValues(attrs map[string]string, keys ...string) ([]string, error) {
	var values []string
	for _, key := range keys {
		value, ok := attrs[key]
		if !ok {
			continue
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("source attribute %q is empty", key)
		}
		values = append(values, value)
	}
	return values, nil
}
