package netsec

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// CanonicalResourcePath validates an absolute slash-separated resource path.
// Dot segments, duplicate slashes, and embedded NUL bytes are rejected rather
// than silently rewritten. A single trailing slash is ignored.
func CanonicalResourcePath(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("resource path is empty")
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("resource path %q is not absolute", value)
	}
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("resource path contains NUL")
	}

	clean := path.Clean(value)
	if value != clean && value != clean+"/" {
		return "", fmt.Errorf("resource path %q is not canonical", value)
	}
	if clean == "/" && value != "/" {
		return "", fmt.Errorf("resource path %q is not canonical", value)
	}
	return clean, nil
}

// pathHasPrefix reports whether resource is the same as prefix or is below it
// at a slash component boundary. It is intentionally not strings.HasPrefix:
// "/data" must not match "/database".
func pathHasPrefix(prefix, resource string) bool {
	if prefix == "/" {
		return true
	}
	return resource == prefix || strings.HasPrefix(resource, prefix+"/")
}

func intersectStringSets(a, b []string) []string {
	result := make([]string, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			result = append(result, a[i])
			i++
			j++
		}
	}
	return result
}

// intersectPrefixes computes the language intersection of two path-prefix
// sets. Two prefixes intersect only when one is a component prefix of the
// other; the narrower prefix is the result.
func intersectPrefixes(a, b []string) []string {
	intersection := make([]string, 0, len(a)*len(b))
	for _, left := range a {
		for _, right := range b {
			switch {
			case pathHasPrefix(left, right):
				intersection = append(intersection, right)
			case pathHasPrefix(right, left):
				intersection = append(intersection, left)
			}
		}
	}
	return simplifyPrefixes(intersection)
}

func simplifyPrefixes(prefixes []string) []string {
	sort.Strings(prefixes)
	unique := prefixes[:0]
	for _, prefix := range prefixes {
		if len(unique) == 0 || unique[len(unique)-1] != prefix {
			unique = append(unique, prefix)
		}
	}

	result := make([]string, 0, len(unique))
	for i, prefix := range unique {
		covered := false
		for j, candidate := range unique {
			if i != j && pathHasPrefix(candidate, prefix) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, prefix)
		}
	}
	return result
}
