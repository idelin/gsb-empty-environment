package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

type semanticVersion struct {
	raw        string
	components [3]uint64
	prerelease []string
}

func parseVersion(value string) (semanticVersion, error) {
	raw := strings.TrimSpace(value)
	version := raw
	if strings.HasPrefix(version, "v") || strings.HasPrefix(version, "V") {
		version = version[1:]
	}
	if version == "" {
		return semanticVersion{}, fmt.Errorf("empty version")
	}
	if i := strings.IndexByte(version, '+'); i >= 0 {
		version = version[:i]
	}

	var prerelease string
	if i := strings.IndexByte(version, '-'); i >= 0 {
		prerelease = version[i+1:]
		version = version[:i]
		if prerelease == "" {
			return semanticVersion{}, fmt.Errorf("empty prerelease")
		}
	}

	parts := strings.Split(version, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return semanticVersion{}, fmt.Errorf("invalid version %q", raw)
	}
	var components [3]uint64
	for i, part := range parts {
		if part == "" {
			return semanticVersion{}, fmt.Errorf("invalid version %q", raw)
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("invalid version %q", raw)
		}
		components[i] = number
	}

	var prereleaseParts []string
	if prerelease != "" {
		prereleaseParts = strings.Split(prerelease, ".")
		for _, part := range prereleaseParts {
			if part == "" {
				return semanticVersion{}, fmt.Errorf("invalid prerelease in %q", raw)
			}
			for _, char := range part {
				if !isVersionIdentifierChar(char) {
					return semanticVersion{}, fmt.Errorf("invalid prerelease in %q", raw)
				}
			}
		}
	}

	return semanticVersion{
		raw:        raw,
		components: components,
		prerelease: prereleaseParts,
	}, nil
}

func isVersionIdentifierChar(char rune) bool {
	return char >= '0' && char <= '9' ||
		char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char == '-'
}

func (v semanticVersion) compare(other semanticVersion) int {
	for i := range v.components {
		if v.components[i] < other.components[i] {
			return -1
		}
		if v.components[i] > other.components[i] {
			return 1
		}
	}

	if len(v.prerelease) == 0 && len(other.prerelease) == 0 {
		return 0
	}
	if len(v.prerelease) == 0 {
		return 1
	}
	if len(other.prerelease) == 0 {
		return -1
	}

	for i := 0; i < len(v.prerelease) && i < len(other.prerelease); i++ {
		comparison := comparePrereleaseIdentifier(v.prerelease[i], other.prerelease[i])
		if comparison != 0 {
			return comparison
		}
	}
	if len(v.prerelease) < len(other.prerelease) {
		return -1
	}
	if len(v.prerelease) > len(other.prerelease) {
		return 1
	}
	return 0
}

func comparePrereleaseIdentifier(left, right string) int {
	leftNumber, leftNumeric := parseNumericIdentifier(left)
	rightNumber, rightNumeric := parseNumericIdentifier(right)
	switch {
	case leftNumeric && rightNumeric:
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	case leftNumeric:
		return -1
	case rightNumeric:
		return 1
	default:
		return strings.Compare(left, right)
	}
}

func parseNumericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return number, err == nil
}
