package hostkeys

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// readLimited reads a known_hosts file while enforcing maxKnownHostsSize. A
// missing file is reported as an empty buffer so the first rotation can create
// it; in practice the hostIsKnown gate means this path is only reached for an
// existing file.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	limited := io.LimitReader(f, maxKnownHostsSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxKnownHostsSize {
		return nil, fmt.Errorf("hostkeys: known_hosts file exceeds %d bytes", maxKnownHostsSize)
	}
	return data, nil
}

// hostPattern returns the known_hosts hostname token for new lines: either a
// hashed hostname marker or the plain host:port normalization that
// knownhosts.Line uses.
func (r *Rotator) hostPattern(hostname string) (string, error) {
	normalized, err := knownhosts.Normalize(hostname)
	if err != nil {
		return "", err
	}
	if r.opts.HashHostnames {
		return knownhosts.HashHostname(normalized)
	}
	return normalized, nil
}

// rewriteKnownHosts atomically replaces the known_hosts file: proved keys are
// ensured present, and matching host entries whose key is no longer listed are
// removed. It is only called after every proof in the batch succeeded.
func (r *Rotator) rewriteKnownHosts(hostname string, remote net.Addr, proved map[string]ssh.PublicKey) error {
	path := r.opts.KnownHostsPath
	data, err := readLimited(path)
	if err != nil {
		return err
	}

	pattern, err := r.hostPattern(hostname)
	if err != nil {
		return err
	}

	// wanted holds canonical key blobs from the proved batch.
	wanted := make(map[string]bool, len(proved))
	for blob := range proved {
		wanted[blob] = true
	}

	var kept [][]byte
	present := make(map[string]bool)
	dropped := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxKnownHostsSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' || trimmed[0] == '@' {
			// Keep blank lines, comments and marker lines (revoked,
			// cert-authority, ...) untouched.
			kept = append(kept, append([]byte(nil), line...))
			continue
		}

		fields := strings.Fields(string(line))
		if len(fields) < 3 {
			// Preserve unparseable lines rather than silently dropping them.
			kept = append(kept, append([]byte(nil), line...))
			continue
		}

		blob, match, err := classifyLine(fields, hostname)
		if err != nil {
			kept = append(kept, append([]byte(nil), line...))
			continue
		}
		if !match {
			kept = append(kept, append([]byte(nil), line...))
			continue
		}

		// The line belongs to this host. Drop it only when its key is absent
		// from the proved batch (i.e. the server no longer lists it).
		if wanted[blob] {
			present[blob] = true
			kept = append(kept, append([]byte(nil), line...))
		} else {
			dropped = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Append proved keys that did not already have a matching line.
	added := false
	for blob, key := range proved {
		if present[blob] {
			continue
		}
		kept = append(kept, []byte(pattern+" "+key.Type()+" "+base64.StdEncoding.EncodeToString([]byte(blob))))
		added = true
	}
	if !added && !dropped {
		// Nothing changed; avoid rewriting the file at all.
		return nil
	}

	return atomicWrite(path, bytes.Join(kept, []byte("\n")))
}

// classifyLine parses a plain known_hosts line and reports whether it matches
// the given host and the canonical key blob it carries.
func classifyLine(fields []string, hostname string) (blob string, matches bool, err error) {
	// Plain key lines have the form [host-patterns] keytype base64 [comment].
	key, parseErr := ssh.ParseAuthorizedKey([]byte(strings.Join(fields[1:], " ")))
	if parseErr != nil {
		return "", false, parseErr
	}

	for _, pattern := range strings.Split(fields[0], ",") {
		match, matchErr := lineMatchesHost(pattern, hostname)
		if matchErr != nil {
			return "", false, matchErr
		}
		if match {
			return string(key.Marshal()), true, nil
		}
	}
	return string(key.Marshal()), false, nil
}

// lineMatchesHost evaluates one host pattern, including ssh-keygen -H hashed
// entries, against the connection's hostname and address.
func lineMatchesHost(pattern, hostname string) (bool, error) {
	if strings.HasPrefix(pattern, "|1|") {
		normalized, err := knownhosts.Normalize(hostname)
		if err != nil {
			return false, err
		}
		return matchHashed(pattern, normalized), nil
	}
	return matchPlainPattern(pattern, hostname), nil
}

// matchHashed reimplements the |1|<salt>|<mac> verification used by
// knownhosts.HashHostname (HMAC-SHA1, base64).
func matchHashed(hashed, normalized string) bool {
	parts := strings.Split(hashed, "|")
	if len(parts) != 4 || parts[1] != "1" {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	mac, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	h := hmac.New(sha1.New, salt)
	h.Write([]byte(normalized))
	return hmac.Equal(h.Sum(nil), mac)
}

// matchPlainPattern handles exact, negated and wildcard patterns produced by
// knownhosts.Normalize.
func matchPlainPattern(pattern, hostname string) bool {
	negated := false
	if strings.HasPrefix(pattern, "!") {
		negated = true
		pattern = pattern[1:]
	}

	normalized, err := knownhosts.Normalize(hostname)
	if err != nil {
		return false
	}
	matched, err := filepath.Match(pattern, normalized)
	if err != nil {
		matched = pattern == normalized
	}
	if negated {
		return !matched
	}
	return matched
}

// atomicWrite writes data to a temp file in the destination directory, fsyncs
// it and renames it over the known_hosts file with conservative 0600 perms.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".known_hosts.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
