package safeunpack

import "strings"

// normalizeEntryPath performs separator and traversal validation without using
// filepath.Clean, whose behavior is host-OS dependent.
func normalizeEntryPath(raw string, maxPath int) (string, []string, bool, error) {
	if raw == "" {
		return "", nil, false, wrapError(ErrUnsafePath, raw, -1, "empty entry path")
	}
	if strings.IndexByte(raw, 0) >= 0 {
		return "", nil, false, wrapError(ErrUnsafePath, raw, -1, "path contains NUL")
	}
	if strings.ContainsRune(raw, '\\') {
		return "", nil, false, wrapError(ErrUnsafePath, raw, -1, "path contains a Windows separator")
	}
	if strings.HasPrefix(raw, "/") {
		return "", nil, false, wrapError(ErrUnsafePath, raw, -1, "absolute path is not allowed")
	}
	if hasWindowsDrivePrefix(raw) {
		return "", nil, false, wrapError(ErrUnsafePath, raw, -1, "Windows drive prefix is not allowed")
	}
	if maxPath >= 0 && len(raw) > maxPath {
		return "", nil, false, wrapError(ErrLimitExceeded, raw, -1, "path exceeds %d bytes", maxPath)
	}

	trailingSlash := strings.HasSuffix(raw, "/")
	parts := make([]string, 0, strings.Count(raw, "/")+1)
	for _, part := range strings.Split(raw, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", nil, false, wrapError(ErrPathTraversal, raw, -1, "path contains a '..' component")
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "", nil, false, wrapError(ErrUnsafePath, raw, -1, "path normalizes to the archive root")
	}
	path := strings.Join(parts, "/")
	if maxPath >= 0 && len(path) > maxPath {
		return "", nil, false, wrapError(ErrLimitExceeded, path, -1, "normalized path exceeds %d bytes", maxPath)
	}
	return path, parts, trailingSlash, nil
}

func splitAndValidateLinkTarget(raw string, maxPath int, allowDotDot bool) ([]string, error) {
	if raw == "" {
		return nil, wrapError(ErrLinkEscape, raw, -1, "empty link target")
	}
	if strings.IndexByte(raw, 0) >= 0 {
		return nil, wrapError(ErrLinkEscape, raw, -1, "link target contains NUL")
	}
	if strings.ContainsRune(raw, '\\') {
		return nil, wrapError(ErrLinkEscape, raw, -1, "link target contains a Windows separator")
	}
	if strings.HasPrefix(raw, "/") {
		return nil, wrapError(ErrLinkEscape, raw, -1, "absolute link target is not allowed")
	}
	if hasWindowsDrivePrefix(raw) {
		return nil, wrapError(ErrLinkEscape, raw, -1, "Windows drive-prefixed link target is not allowed")
	}
	if maxPath >= 0 && len(raw) > maxPath {
		return nil, wrapError(ErrLimitExceeded, raw, -1, "link target exceeds %d bytes", maxPath)
	}

	parts := make([]string, 0, strings.Count(raw, "/")+1)
	for _, part := range strings.Split(raw, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if !allowDotDot {
				return nil, wrapError(ErrLinkEscape, raw, -1, "link target contains '..'")
			}
			parts = append(parts, part)
		default:
			parts = append(parts, part)
		}
	}
	return parts, nil
}

func hasWindowsDrivePrefix(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	c := path[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
