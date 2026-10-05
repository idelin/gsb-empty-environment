package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RecoveryReport describes what Open found and did during recovery.
type RecoveryReport struct {
	// UncommittedStaging lists staging directories left over from
	// interrupted publishes. They are moved to quarantine, never deleted.
	UncommittedStaging []string
	// BadPointer is true when the pointer file was missing (while
	// committed versions exist), unparsable, or pointed at a
	// missing/invalid version.
	BadPointer bool
	// PointerTarget is the offending pointer content, if any.
	PointerTarget string
	// Quarantined lists directories moved into quarantine during recovery.
	Quarantined []string
	// UnknownEntries lists root entries recovery does not recognize.
	// They are left completely untouched.
	UnknownEntries []string
	// Recovered is the version id selected as current after recovery
	// ("" when the store has no valid committed version).
	Recovered string
}

// recover inspects the store root and restores a consistent state:
// uncommitted staging directories are quarantined, a bad pointer is
// rebuilt from the newest valid committed version, and unknown entries
// are reported but never modified or deleted.
func (s *Store) recover() error {
	rep := s.report

	// 1. Leftover staging directories are uncommitted publishes.
	stagingDir := filepath.Join(s.root, stagingDirName)
	if entries, err := os.ReadDir(stagingDir); err != nil {
		return err
	} else {
		for _, e := range entries {
			dst, err := s.quarantine("staging-" + e.Name())
			if err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(stagingDir, e.Name()), dst); err != nil {
				return err
			}
			rep.UncommittedStaging = append(rep.UncommittedStaging, e.Name())
			rep.Quarantined = append(rep.Quarantined, dst)
		}
	}

	// 2. A leftover pointer tmp file is a recognized protocol artifact
	// from an interrupted pointer swap; quarantine it.
	tmpPtr := filepath.Join(s.root, pointerTmpName)
	if _, err := os.Lstat(tmpPtr); err == nil {
		dst, err := s.quarantine(pointerTmpName)
		if err != nil {
			return err
		}
		if err := os.Rename(tmpPtr, dst); err != nil {
			return err
		}
		rep.Quarantined = append(rep.Quarantined, dst)
	} else if !os.IsNotExist(err) {
		return err
	}

	// 3. Classify committed versions: valid ones are recovery candidates,
	// malformed version ids failing validation are quarantined, and
	// anything not shaped like a version directory is left alone.
	var valid []string
	versionsDir := filepath.Join(s.root, versionsDirName)
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		seq, ok := parseVersionID(name)
		if !e.IsDir() || !ok {
			rep.UnknownEntries = append(rep.UnknownEntries, filepath.Join(versionsDirName, name))
			continue
		}
		dir := filepath.Join(versionsDir, name)
		if err := validateVersionDir(dir); err != nil {
			dst, qerr := s.quarantine("corrupt-" + name)
			if qerr != nil {
				return qerr
			}
			if err := os.Rename(dir, dst); err != nil {
				return err
			}
			rep.Quarantined = append(rep.Quarantined, dst)
			continue
		}
		valid = append(valid, name)
		if seq > s.seq {
			s.seq = seq
		}
	}
	sort.Strings(valid)

	// 4. Resolve the pointer.
	newest := ""
	if len(valid) > 0 {
		newest = valid[len(valid)-1]
	}
	data, err := os.ReadFile(filepath.Join(s.root, pointerFileName))
	switch {
	case os.IsNotExist(err):
		if newest != "" {
			rep.BadPointer = true
			s.current = newest
			if err := s.rewritePointer(newest); err != nil {
				return err
			}
		}
	case err != nil:
		return err
	default:
		target := strings.TrimSpace(string(data))
		if isValidVersion(valid, target) {
			s.current = target
		} else {
			rep.BadPointer = true
			rep.PointerTarget = target
			s.current = newest
			if newest != "" {
				if err := s.rewritePointer(newest); err != nil {
					return err
				}
			}
		}
	}
	rep.Recovered = s.current

	// 5. Report unknown root entries without touching them.
	rootEntries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, e := range rootEntries {
		switch e.Name() {
		case stagingDirName, versionsDirName, quarantineDirName, pointerFileName:
		default:
			rep.UnknownEntries = append(rep.UnknownEntries, e.Name())
		}
	}
	return nil
}

// quarantine reserves a unique destination path inside quarantine/.
func (s *Store) quarantine(base string) (string, error) {
	dir := filepath.Join(s.root, quarantineDirName)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); os.IsNotExist(err) {
			return dst, nil
		} else if err != nil {
			return "", err
		}
	}
}

// rewritePointer durably replaces the pointer file during recovery.
func (s *Store) rewritePointer(id string) error {
	tmp := filepath.Join(s.root, pointerTmpName)
	if err := writePointerFile(tmp, id); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.root, pointerFileName)); err != nil {
		return err
	}
	return syncDir(s.root)
}

func parseVersionID(name string) (int, bool) {
	var seq int
	if len(name) != 7 || name[0] != 'v' {
		return 0, false
	}
	for _, c := range name[1:] {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	if _, err := fmt.Sscanf(name, "v%06d", &seq); err != nil {
		return 0, false
	}
	return seq, true
}

func isValidVersion(valid []string, id string) bool {
	i := sort.SearchStrings(valid, id)
	return i < len(valid) && valid[i] == id
}
