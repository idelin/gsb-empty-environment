//go:build linux

package timens

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procRoot is overridable in tests that stage a fake /proc tree.
var procRoot = "/proc"

// capSysTime is bit 25 of the capability sets, per Linux capabilities(7).
const capSysTime = 25

// ProcfsGuard implements NamespaceGuard by scanning /proc for tasks whose
// time-namespace inode matches the target namespace.
//
// The kernel is the authoritative gate: writes to a timens_offsets file fail
// with EPERM once the namespace has hosted a member. This scan is a
// defense-in-depth check that fails early with a typed error. The holder
// process (the PID referenced by the offsets path) is excluded, because that
// process is expected to be the runtime's own init waiting in the still-empty
// namespace for the offsets to be applied.
type ProcfsGuard struct{}

// NewProcfsGuard returns the Linux NamespaceGuard backed by /proc.
func NewProcfsGuard() *ProcfsGuard { return &ProcfsGuard{} }

// holderPID extracts the PID from a path of the form /proc/<pid>/timens_offsets.
func holderPID(offsetsPath string) (int, error) {
	abs, err := filepath.Abs(offsetsPath)
	if err != nil {
		return 0, fmt.Errorf("timens: resolving offsets path: %w", err)
	}
	dir, file := filepath.Split(abs)
	if file != "timens_offsets" {
		return 0, fmt.Errorf("timens: %q is not a timens_offsets path", offsetsPath)
	}
	pidText := filepath.Base(filepath.Clean(dir))
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return 0, fmt.Errorf("timens: cannot parse PID from %q: %w", offsetsPath, err)
	}
	return pid, nil
}

// readNamespaceInode returns the inode number in a "type:[inode]" ns link.
func readNamespaceInode(link string) (uint64, error) {
	start := strings.IndexByte(link, '[')
	end := strings.IndexByte(link, ']')
	if start < 0 || end <= start+1 {
		return 0, fmt.Errorf("unexpected namespace link %q", link)
	}
	inode, err := strconv.ParseUint(link[start+1:end], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing namespace inode %q: %w", link, err)
	}
	return inode, nil
}

// Empty returns ErrNamespaceNotWritable when any task other than the holder
// already belongs to the target time namespace.
func (g *ProcfsGuard) Empty(offsetsPath string) error {
	holder, err := holderPID(offsetsPath)
	if err != nil {
		return err
	}

	target, err := os.Readlink(fmt.Sprintf("%s/%d/ns/time", procRoot, holder))
	if err != nil {
		return fmt.Errorf("timens: reading holder time namespace: %w", err)
	}
	targetInode, err := readNamespaceInode(target)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return fmt.Errorf("timens: scanning %s: %w", procRoot, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == holder {
			continue
		}
		link, err := os.Readlink(fmt.Sprintf("%s/%d/ns/time", procRoot, pid))
		if err != nil {
			// Tasks can disappear while /proc is being walked.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			continue
		}
		inode, err := readNamespaceInode(link)
		if err != nil {
			continue
		}
		if inode == targetInode {
			return fmt.Errorf("%w: pid %d is already a member", ErrNamespaceNotWritable, pid)
		}
	}
	return nil
}

// StatusCapChecker reads CAP_SYS_TIME from /proc/self/status (CapEff).
type StatusCapChecker struct {
	// procSelfStatus defaults to /proc/self/status; tests may override it.
	procSelfStatus string
}

// NewStatusCapChecker returns a CapabilityChecker backed by procfs.
func NewStatusCapChecker() *StatusCapChecker {
	return &StatusCapChecker{procSelfStatus: procRoot + "/self/status"}
}

// HasSysTime reports whether CAP_SYS_TIME is set in the effective set.
func (c *StatusCapChecker) HasSysTime() (bool, error) {
	data, err := os.ReadFile(c.procSelfStatus)
	if err != nil {
		return false, fmt.Errorf("timens: reading capability status: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		mask, err := strconv.ParseUint(value, 16, 64)
		if err != nil {
			return false, fmt.Errorf("timens: parsing CapEff %q: %w", value, err)
		}
		return mask&(1<<capSysTime) != 0, nil
	}
	return false, errors.New("timens: CapEff line missing from process status")
}

// WriteProcOffsets is the Linux high-level entry point: it parses and
// validates the offsets against a live procfs, then writes them to
// /proc/<pid>/timens_offsets. Warnings (for example duplicate clock lines)
// are returned alongside a nil error.
func WriteProcOffsets(pid int, r io.Reader, hostMonotonic HostTime) ([]Warning, error) {
	offsetsPath := fmt.Sprintf("%s/%d/timens_offsets", procRoot, pid)
	offsets, warnings, err := PrepareOffsets(r, hostMonotonic, NewProcfsGuard(), NewStatusCapChecker(), offsetsPath)
	if err != nil {
		return warnings, err
	}
	// procfs nodes reject O_CREAT/O_TRUNC-style opens; mirror the runtime
	// convention and open the existing node write-only.
	f, err := os.OpenFile(offsetsPath, os.O_WRONLY, 0)
	if err != nil {
		return warnings, fmt.Errorf("timens: opening %s: %w", offsetsPath, err)
	}
	defer f.Close()
	if _, err := f.Write(Render(offsets)); err != nil {
		return warnings, err
	}
	return warnings, nil
}
