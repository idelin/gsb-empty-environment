package dkim

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// Signature is a parsed DKIM-Signature header.
type Signature struct {
	Header         Header
	Tags           map[string]string
	TagOrder       []string
	Domain         string
	Selector       string
	Identity       string
	Algorithm      string
	HeaderNames    []string
	Canonicalize   string
	BodyHash       []byte
	Value          []byte
	Timestamp      int64
	Expiration     int64
	HasExpiration  bool
	Length         int
	HasLength      bool
	QueryMethod    string
	KeyType        string
	RawTagList     string
	DuplicateTags  []string
	UnsupportedTag string
}

// ParseSignature parses a DKIM-Signature header.
func ParseSignature(header Header) (*Signature, error) {
	if !strings.EqualFold(strings.TrimSpace(header.Name), "DKIM-Signature") {
		return nil, errors.New("dkim: not a DKIM-Signature header")
	}
	tags, order, duplicates, err := parseTagList(header.ValueRaw)
	if err != nil {
		return nil, err
	}
	sig := &Signature{
		Header:        header,
		Tags:          tags,
		TagOrder:      order,
		DuplicateTags: duplicates,
		RawTagList:    header.ValueRaw,
	}
	if len(duplicates) > 0 {
		return nil, errors.New("dkim: duplicate signature tag " + duplicates[0])
	}
	if tags["v"] != "1" {
		return nil, errors.New("dkim: unsupported signature version")
	}
	sig.Domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(tags["d"])), ".")
	sig.Selector = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(tags["s"])), ".")
	sig.Identity = strings.TrimSpace(tags["i"])
	sig.Algorithm = strings.ToLower(strings.TrimSpace(tags["a"]))
	if sig.Domain == "" || sig.Selector == "" || sig.Algorithm == "" {
		return nil, errors.New("dkim: missing d, s, or a tag")
	}
	for _, name := range strings.Split(tags["h"], ":") {
		name = strings.TrimSpace(name)
		if name != "" {
			sig.HeaderNames = append(sig.HeaderNames, name)
		}
	}
	if len(sig.HeaderNames) == 0 {
		return nil, errors.New("dkim: missing h tag")
	}
	sig.Canonicalize = strings.ToLower(strings.TrimSpace(tags["c"]))
	if sig.Canonicalize == "" {
		sig.Canonicalize = "simple/simple"
	}
	sig.BodyHash, err = decodeBase64Tag(tags["bh"])
	if err != nil {
		return nil, errors.New("dkim: invalid bh tag")
	}
	sig.Value, err = decodeBase64Tag(tags["b"])
	if err != nil {
		return nil, errors.New("dkim: invalid b tag")
	}
	sig.QueryMethod = strings.TrimSpace(tags["q"])
	sig.KeyType = strings.ToLower(strings.TrimSpace(tags["k"]))
	if raw := strings.TrimSpace(tags["t"]); raw != "" {
		sig.Timestamp, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errors.New("dkim: invalid t tag")
		}
	}
	if raw := strings.TrimSpace(tags["x"]); raw != "" {
		sig.Expiration, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errors.New("dkim: invalid x tag")
		}
		sig.HasExpiration = true
	}
	if raw := strings.TrimSpace(tags["l"]); raw != "" {
		sig.Length, err = strconv.Atoi(raw)
		if err != nil || sig.Length < 0 {
			return nil, errors.New("dkim: invalid l tag")
		}
		sig.HasLength = true
	}
	return sig, nil
}

// CanonicalizationMethods returns the header and body canonicalization names.
func (s *Signature) CanonicalizationMethods() (headerMode, bodyMode string, err error) {
	parts := strings.Split(s.Canonicalize, "/")
	if len(parts) > 2 || parts[0] == "" {
		return "", "", errors.New("dkim: invalid c tag")
	}
	headerMode = parts[0]
	bodyMode = headerMode
	if len(parts) == 2 {
		if parts[1] == "" {
			return "", "", errors.New("dkim: invalid c tag")
		}
		bodyMode = parts[1]
	}
	if headerMode != "simple" && headerMode != "relaxed" {
		return "", "", errors.New("dkim: unsupported header canonicalization")
	}
	if bodyMode != "simple" && bodyMode != "relaxed" {
		return "", "", errors.New("dkim: unsupported body canonicalization")
	}
	return headerMode, bodyMode, nil
}

func parseTagList(value string) (map[string]string, []string, []string, error) {
	tags := make(map[string]string)
	order := make([]string, 0, 12)
	var duplicates []string
	for _, segment := range strings.Split(value, ";") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		name, tagValue, ok := strings.Cut(segment, "=")
		if !ok {
			return nil, nil, nil, errors.New("dkim: malformed tag")
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return nil, nil, nil, errors.New("dkim: empty tag name")
		}
		if _, exists := tags[name]; exists {
			duplicates = append(duplicates, name)
			continue
		}
		tags[name] = strings.TrimSpace(tagValue)
		order = append(order, name)
	}
	return tags, order, duplicates, nil
}

func decodeBase64Tag(value string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, value)
	if cleaned == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(cleaned)
}
