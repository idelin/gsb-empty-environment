package netsec

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Lockfile holds the security-relevant sections of a Podfile.lock.
//
// SpecRepos maps a source repository key (e.g. "trunk" or a git URL) to the
// pod names resolved from it. Checksums maps a pod name to the SHA-1 recorded
// under SPEC CHECKSUMS.
type Lockfile struct {
	SpecRepos map[string][]string
	Checksums map[string]string

	podRepos map[string]string
}

// ParseLockfile parses a Podfile.lock. Only the SPEC REPOS and SPEC CHECKSUMS
// sections are needed; all other sections are accepted but ignored.
func ParseLockfile(r io.Reader) (*Lockfile, error) {
	lock := &Lockfile{
		SpecRepos: make(map[string][]string),
		Checksums: make(map[string]string),
		podRepos:  make(map[string]string),
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	section := ""
	currentRepo := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		indented := line[0] == ' ' || line[0] == '\t'
		if !indented {
			// A top-level section header or top-level key.
			currentRepo = ""
			switch strings.TrimSuffix(strings.TrimSpace(line), ":") {
			case "SPEC REPOS":
				section = "repos"
			case "SPEC CHECKSUMS":
				section = "checksums"
			default:
				section = ""
			}
			continue
		}

		switch section {
		case "repos":
			trimmed := strings.TrimSpace(line)
			if strings.HasSuffix(trimmed, ":") {
				currentRepo = normalizeRepoKey(strings.TrimSuffix(trimmed, ":"))
				if _, ok := lock.SpecRepos[currentRepo]; !ok {
					lock.SpecRepos[currentRepo] = nil
				}
				continue
			}
			name := podNameFromEntry(trimmed)
			if name == "" || currentRepo == "" {
				return nil, fmt.Errorf("%w: malformed SPEC REPOS entry on line %d", ErrInvalidPodspec, lineNo)
			}
			lock.SpecRepos[currentRepo] = append(lock.SpecRepos[currentRepo], name)
			if _, seen := lock.podRepos[name]; !seen {
				lock.podRepos[name] = currentRepo
			}
		case "checksums":
			entry := strings.TrimSpace(line)
			colon := strings.IndexByte(entry, ':')
			if colon <= 0 {
				return nil, fmt.Errorf("%w: malformed SPEC CHECKSUMS entry on line %d", ErrInvalidPodspec, lineNo)
			}
			name := strings.TrimSpace(entry[:colon])
			checksum := strings.TrimSpace(entry[colon+1:])
			lock.Checksums[name] = checksum
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPodspec, err)
	}
	return lock, nil
}

// RepoForPod returns the source repository the lock recorded for the pod.
func (l *Lockfile) RepoForPod(name string) (string, bool) {
	repo, ok := l.podRepos[name]
	return repo, ok
}

// ChecksumForPod returns the recorded SHA-1 for the pod.
func (l *Lockfile) ChecksumForPod(name string) (string, bool) {
	sum, ok := l.Checksums[name]
	return sum, ok
}

// normalizeRepoKey canonicalizes a SPEC REPOS key so the same repository is
// not treated as two sources because of a trailing slash.
func normalizeRepoKey(key string) string {
	key = strings.TrimSpace(key)
	return strings.TrimRight(key, "/")
}

// podNameFromEntry turns "- Name (1.2.3)" or "- Name" into "Name".
func podNameFromEntry(entry string) string {
	entry = strings.TrimSpace(entry)
	entry = strings.TrimPrefix(entry, "-")
	entry = strings.TrimSpace(entry)
	if paren := strings.IndexByte(entry, '('); paren >= 0 {
		entry = strings.TrimSpace(entry[:paren])
	}
	return entry
}
