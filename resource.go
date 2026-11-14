package netsec

import (
	"fmt"
	"net/url"
	"strings"
)

type resourcePattern struct {
	raw       string
	bucket    string
	key       string
	hasKey    bool
	universal bool
}

func parseResourcePattern(raw string) (resourcePattern, error) {
	var out resourcePattern
	value := strings.TrimSpace(raw)
	if value == "" {
		return out, fmt.Errorf("empty resource")
	}
	out.raw = value

	if value == "*" || value == "*/*" {
		out.universal = true
		out.bucket = "*"
		out.key = "*"
		out.hasKey = true
		return out, nil
	}

	switch {
	case strings.HasPrefix(value, "arn:"):
		parts := strings.SplitN(value, ":", 6)
		if len(parts) != 6 || !strings.EqualFold(parts[0], "arn") ||
			!strings.EqualFold(parts[2], "s3") {
			return out, fmt.Errorf("unsupported resource ARN %q", value)
		}
		return parseBucketResource(parts[5])
	case strings.HasPrefix(value, "s3://"):
		return parseBucketResource(strings.TrimPrefix(value, "s3://"))
	case strings.HasPrefix(value, "s3:"):
		return out, fmt.Errorf("unsupported resource %q", value)
	default:
		return parseBucketResource(value)
	}
}

func parseBucketResource(value string) (resourcePattern, error) {
	var out resourcePattern
	out.raw = value
	if value == "" {
		return out, fmt.Errorf("empty resource")
	}
	slash := strings.Index(value, "/")
	if slash < 0 {
		out.bucket = value
		return out, nil
	}
	out.bucket = value[:slash]
	out.key = value[slash+1:]
	out.hasKey = true
	if out.bucket == "" {
		return out, fmt.Errorf("resource %q has an empty bucket", value)
	}
	return out, nil
}

func (rp resourcePattern) matches(actionKind actionKind, bucket, key string) bool {
	if rp.universal {
		return true
	}
	if !globMatch(rp.bucket, bucket) {
		return false
	}
	switch actionKind {
	case actionKindBucket:
		return !rp.hasKey
	case actionKindObject:
		if !rp.hasKey || rp.key == "" {
			return false
		}
		return globMatch(rp.key, key)
	default:
		return false
	}
}

func (rp resourcePattern) fixedKeyPrefix() string {
	if !rp.hasKey || rp.universal {
		return ""
	}
	if i := strings.IndexAny(rp.key, "*?"); i >= 0 {
		return rp.key[:i]
	}
	return rp.key
}

func unsafeObjectKey(value string) bool {
	if strings.ContainsRune(value, '\x00') {
		return true
	}
	for i := 0; i < 3 && strings.Contains(value, "%"); i++ {
		decoded, err := url.PathUnescape(value)
		if err != nil || decoded == value {
			break
		}
		value = decoded
	}
	lower := strings.ToLower(value)
	normalized := strings.ReplaceAll(lower, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func globMatch(pattern string, value string) bool {
	patternRunes := []rune(pattern)
	valueRunes := []rune(value)
	var patternIndex, valueIndex int
	starIndex := -1
	starMatchIndex := 0

	for valueIndex < len(valueRunes) {
		if patternIndex < len(patternRunes) &&
			(patternRunes[patternIndex] == '?' || patternRunes[patternIndex] == valueRunes[valueIndex]) {
			patternIndex++
			valueIndex++
			continue
		}
		if patternIndex < len(patternRunes) && patternRunes[patternIndex] == '*' {
			starIndex = patternIndex
			starMatchIndex = valueIndex
			patternIndex++
			continue
		}
		if starIndex >= 0 {
			patternIndex = starIndex + 1
			starMatchIndex++
			valueIndex = starMatchIndex
			continue
		}
		return false
	}
	for patternIndex < len(patternRunes) && patternRunes[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(patternRunes)
}
