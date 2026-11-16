package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

var (
	// ErrAmbiguousKnownHostsEntry prevents a key removal from silently
	// affecting other hosts through a wildcard known_hosts pattern.
	ErrAmbiguousKnownHostsEntry = errors.New("cannot safely remove key from wildcard known_hosts entry")
)

type knownHostsSnapshot struct {
	path       string
	mode       fs.FileMode
	contents   []byte
	host       string
	lines      []knownHostsLine
	keys       []HostKey
	revoked    []HostKey
	hasEntry   bool
	useHashing bool
}

type knownHostsLine struct {
	raw       string
	ending    string
	marker    string
	hostField string
	keyType   string
	encoded   string
	blob      []byte
	comment   string
	valid     bool
	matches   bool
	hashed    bool
	exact     bool
}

func loadKnownHosts(path, host string) (*knownHostsSnapshot, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoKnownHostsEntry, path)
		}
		return nil, fmt.Errorf("read known_hosts: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat known_hosts: %w", err)
	}

	snapshot := &knownHostsSnapshot{
		path:     path,
		mode:     info.Mode(),
		contents: contents,
		host:     host,
	}
	keySet := make(map[string]struct{})
	revokedSet := make(map[string]struct{})
	for _, raw := range splitLines(contents) {
		line := parseKnownHostsLine(raw, host)
		snapshot.lines = append(snapshot.lines, line)
		if !line.valid || !line.matches {
			continue
		}
		if line.hashed {
			snapshot.useHashing = true
		}
		key := HostKey{Type: line.keyType, Blob: append([]byte(nil), line.blob...)}
		switch line.marker {
		case "":
			snapshot.hasEntry = true
			if _, exists := keySet[string(key.Blob)]; !exists {
				keySet[string(key.Blob)] = struct{}{}
				snapshot.keys = append(snapshot.keys, key)
			}
		case "@revoked":
			if _, exists := revokedSet[string(key.Blob)]; !exists {
				revokedSet[string(key.Blob)] = struct{}{}
				snapshot.revoked = append(snapshot.revoked, key)
			}
		}
	}
	return snapshot, nil
}

func splitLines(contents []byte) []string {
	if len(contents) == 0 {
		return nil
	}
	var lines []string
	for len(contents) > 0 {
		index := bytes.IndexByte(contents, '\n')
		if index < 0 {
			lines = append(lines, string(contents))
			break
		}
		lines = append(lines, string(contents[:index+1]))
		contents = contents[index+1:]
	}
	return lines
}

func parseKnownHostsLine(raw, host string) knownHostsLine {
	line := knownHostsLine{raw: raw}
	text := raw
	switch {
	case strings.HasSuffix(text, "\r\n"):
		line.ending = "\r\n"
		text = strings.TrimSuffix(text, "\r\n")
	case strings.HasSuffix(text, "\n"):
		line.ending = "\n"
		text = strings.TrimSuffix(text, "\n")
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return line
	}
	if strings.HasPrefix(trimmed, "@") {
		space := strings.IndexAny(trimmed, " \t")
		if space < 0 {
			return line
		}
		line.marker = trimmed[:space]
		if line.marker != "@revoked" && line.marker != "@cert-authority" {
			return line
		}
		trimmed = strings.TrimLeft(trimmed[space:], " \t")
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 3 {
		return line
	}
	line.hostField = fields[0]
	line.keyType = fields[1]
	line.encoded = fields[2]

	keyOffset := strings.Index(text, line.encoded)
	if keyOffset >= 0 {
		line.comment = text[keyOffset+len(line.encoded):]
	}

	blob, err := base64.StdEncoding.DecodeString(line.encoded)
	if err != nil {
		return line
	}
	blobType, err := parsePublicKeyType(blob)
	if err != nil || blobType != line.keyType {
		return line
	}
	line.blob = blob

	matches, hashed, exact, err := knownHostsHostMatch(line.hostField, host)
	if err != nil {
		return line
	}
	line.matches = matches
	line.hashed = hashed
	line.exact = exact
	line.valid = true
	return line
}

func knownHostsHostMatch(field, host string) (matches, hashed, exact bool, err error) {
	if strings.HasPrefix(field, "|") {
		matched, err := matchHashedHost(field, host)
		return matched, true, matched, err
	}

	patterns := strings.Split(field, ",")
	positiveMatches := 0
	exactMatches := 0
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		if negated {
			pattern = strings.TrimPrefix(pattern, "!")
		}
		matched := wildcardHostMatch(pattern, host)
		if !matched {
			continue
		}
		if negated {
			return false, false, false, nil
		}
		positiveMatches++
		if strings.EqualFold(pattern, host) {
			exactMatches++
		}
	}
	return positiveMatches > 0, false, positiveMatches == 1 && exactMatches == 1 && len(patterns) == 1, nil
}

func wildcardHostMatch(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	patternIndex, hostIndex := 0, 0
	starIndex, starHostIndex := -1, 0
	for hostIndex < len(host) {
		if patternIndex < len(pattern) && (pattern[patternIndex] == '?' || pattern[patternIndex] == host[hostIndex]) {
			patternIndex++
			hostIndex++
			continue
		}
		if patternIndex < len(pattern) && pattern[patternIndex] == '*' {
			starIndex = patternIndex
			starHostIndex = hostIndex
			patternIndex++
			continue
		}
		if starIndex < 0 {
			return false
		}
		patternIndex = starIndex + 1
		starHostIndex++
		hostIndex = starHostIndex
	}
	for patternIndex < len(pattern) && pattern[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(pattern)
}

func matchHashedHost(field, host string) (bool, error) {
	parts := strings.Split(field, "|")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "1" {
		return false, errors.New("invalid hashed known_hosts host")
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, err
	}
	expected, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, err
	}
	mac := hmac.New(sha1.New, salt)
	_, _ = mac.Write([]byte(host))
	if hmac.Equal(mac.Sum(nil), expected) {
		return true, nil
	}
	if lower := strings.ToLower(host); lower != host {
		mac := hmac.New(sha1.New, salt)
		_, _ = mac.Write([]byte(lower))
		return hmac.Equal(mac.Sum(nil), expected), nil
	}
	return false, nil
}

func hashedHostField(host string) (string, error) {
	salt := make([]byte, sha1.Size)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate known_hosts salt: %w", err)
	}
	mac := hmac.New(sha1.New, salt)
	_, _ = mac.Write([]byte(host))
	return fmt.Sprintf("|1|%s|%s",
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(mac.Sum(nil))), nil
}

func (snapshot *knownHostsSnapshot) isRevoked(blob []byte) bool {
	for _, key := range snapshot.revoked {
		if bytes.Equal(key.Blob, blob) {
			return true
		}
	}
	return false
}

func (snapshot *knownHostsSnapshot) commit(additions, removals []HostKey) ([]HostKey, bool, error) {
	removeSet := make(map[string]struct{}, len(removals))
	for _, key := range removals {
		removeSet[string(key.Blob)] = struct{}{}
	}

	plans := make(map[int]string, len(snapshot.lines))
	for index, line := range snapshot.lines {
		if !line.valid || !line.matches || line.marker != "" {
			continue
		}
		if _, remove := removeSet[string(line.blob)]; !remove {
			continue
		}
		replacement, drop, err := removalPlan(line, snapshot.host)
		if err != nil {
			return nil, false, err
		}
		if drop {
			plans[index] = ""
		} else {
			plans[index] = replacement
		}
	}

	var out bytes.Buffer
	removed := make(map[string]HostKey)
	for index, line := range snapshot.lines {
		replacement, changed := plans[index]
		if !changed {
			out.WriteString(line.raw)
			continue
		}
		if replacement == "" {
			if _, ok := removeSet[string(line.blob)]; ok {
				removed[string(line.blob)] = HostKey{Type: line.keyType, Blob: append([]byte(nil), line.blob...)}
			}
			continue
		}
		out.WriteString(replacement)
		if _, ok := removeSet[string(line.blob)]; ok {
			removed[string(line.blob)] = HostKey{Type: line.keyType, Blob: append([]byte(nil), line.blob...)}
		}
	}

	if len(additions) > 0 {
		if out.Len() > 0 {
			current := out.Bytes()
			if current[len(current)-1] != '\n' {
				out.WriteByte('\n')
			}
		}
		hostField := snapshot.host
		if snapshot.useHashing {
			var err error
			hostField, err = hashedHostField(snapshot.host)
			if err != nil {
				return nil, false, err
			}
		}
		for _, key := range additions {
			fmt.Fprintf(&out, "%s %s %s\n", hostField, key.Type, base64.StdEncoding.EncodeToString(key.Blob))
		}
	}

	if bytes.Equal(out.Bytes(), snapshot.contents) {
		return nil, false, nil
	}
	if err := writeKnownHostsAtomically(snapshot.path, snapshot.mode, out.Bytes()); err != nil {
		return nil, false, err
	}

	actualRemoved := make([]HostKey, 0, len(removed))
	for _, key := range removals {
		if removedKey, ok := removed[string(key.Blob)]; ok {
			actualRemoved = append(actualRemoved, removedKey)
			delete(removed, string(key.Blob))
		}
	}
	return actualRemoved, true, nil
}

func removalPlan(line knownHostsLine, host string) (replacement string, drop bool, err error) {
	if line.hashed || line.exact {
		return "", true, nil
	}
	if strings.HasPrefix(line.hostField, "|") {
		return "", false, fmt.Errorf("%w: %s", ErrAmbiguousKnownHostsEntry, line.hostField)
	}

	patterns := strings.Split(line.hostField, ",")
	kept := make([]string, 0, len(patterns))
	removedExact := false
	for _, pattern := range patterns {
		if !strings.HasPrefix(pattern, "!") && strings.EqualFold(pattern, host) {
			removedExact = true
			continue
		}
		kept = append(kept, pattern)
	}
	if !removedExact {
		return "", false, fmt.Errorf("%w: %s", ErrAmbiguousKnownHostsEntry, line.hostField)
	}
	for _, pattern := range kept {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		if wildcardHostMatch(pattern, host) {
			return "", false, fmt.Errorf("%w: %s", ErrAmbiguousKnownHostsEntry, line.hostField)
		}
	}
	if len(kept) == 0 {
		return "", true, nil
	}

	prefix := ""
	if line.marker != "" {
		prefix = line.marker + " "
	}
	return fmt.Sprintf("%s%s %s %s%s%s",
		prefix,
		strings.Join(kept, ","),
		line.keyType,
		line.encoded,
		line.comment,
		line.ending), false, nil
}

func writeKnownHostsAtomically(path string, mode fs.FileMode, contents []byte) error {
	directory := "."
	if index := strings.LastIndexAny(path, `/\`); index >= 0 {
		directory = path[:index]
		if directory == "" {
			directory = "/"
		}
	}
	temp, err := os.CreateTemp(directory, ".known_hosts.tmp-*")
	if err != nil {
		return fmt.Errorf("create known_hosts temporary file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	permissions := mode.Perm()
	if permissions == 0 {
		permissions = 0o600
	}
	if _, err := temp.Write(contents); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write known_hosts temporary file: %w", err)
	}
	if err := temp.Chmod(permissions); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set known_hosts temporary file mode: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync known_hosts temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close known_hosts temporary file: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace known_hosts: %w", err)
	}
	return nil
}

func validateKnownHostsHost(host string) error {
	if host == "" {
		return errors.New("known_hosts host is empty")
	}
	if strings.TrimSpace(host) != host || strings.ContainsAny(host, " \t\r\n,#!*?@|") {
		return fmt.Errorf("invalid known_hosts host %q", host)
	}
	return nil
}
