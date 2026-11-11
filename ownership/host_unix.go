//go:build darwin || linux

package ownership

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// EnvMarker is the environment variable used to hand a process its inherited
// tenant generation. A host's reaper/adoption loop reads it from newly
// observed children and feeds the value back through Host.ObserveSpawn; child
// processes that simply fork inherit the marker automatically.
const EnvMarker = "PROCESSHOST_TENANT_GEN"

// SystemKiller delivers POSIX signals. A process that has already exited
// (ESRCH) is treated as success because the desired end state has been
// reached.
type SystemKiller struct{}

// Terminate sends SIGTERM.
func (SystemKiller) Terminate(pid int) error {
	return killIgnoreGone(pid, syscall.SIGTERM)
}

// ForceKill sends SIGKILL.
func (SystemKiller) ForceKill(pid int) error {
	return killIgnoreGone(pid, syscall.SIGKILL)
}

func killIgnoreGone(pid int, sig syscall.Signal) error {
	err := syscall.Kill(pid, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// Host adapts the platform-independent Tracker to real OS processes. It is
// safe for concurrent use.
type Host struct {
	tracker *Tracker
	killer  Killer
	envKey  string
}

// NewHost wraps tracker with OS process integration.
func NewHost(tracker *Tracker) *Host {
	return &Host{
		tracker: tracker,
		killer:  SystemKiller{},
		envKey:  EnvMarker,
	}
}

// Tracker exposes the underlying registry for direct queries.
func (h *Host) Tracker() *Tracker { return h.tracker }

// SpawnRoot starts a new tenant root process in its own process group, tagged
// with g. If the generation was sealed concurrently, the freshly started
// process is killed and ErrGenerationSealed is returned, so no untracked
// process can escape.
func (h *Host) SpawnRoot(g Gen, name string, args []string) (*exec.Cmd, error) {
	if !g.Valid() {
		return nil, ErrInvalidGeneration
	}

	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), h.envKey+"="+g.String())
	// A private process group gives well-behaved tenants a clean boundary
	// without making ownership depend on group topology.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ev := Spawned{
		PID:        cmd.Process.Pid,
		Generation: g,
		PGID:       cmd.Process.Pid,
	}
	if err := h.tracker.Apply(ev); err != nil {
		_ = h.killer.ForceKill(cmd.Process.Pid)
		_ = cmd.Wait()
		return nil, err
	}
	return cmd, nil
}

// ObserveSpawn records a process observed through the host's wait/adoption
// loop (for example a grandchild reparented to a subreaper). The caller is
// responsible for setting Generation from the child's environment marker or
// inheriting it from the tracked parent.
func (h *Host) ObserveSpawn(e Spawned) error {
	return h.tracker.Apply(e)
}

// ObserveExit records a waitid/waitpid result.
func (h *Host) ObserveExit(pid, exitStatus int) error {
	return h.tracker.Apply(Exited{PID: pid, Status: exitStatus})
}

// ObserveGroupChanged records a setpgid/setsid transition observed externally.
// Ownership is unchanged; only diagnostic metadata moves.
func (h *Host) ObserveGroupChanged(pid, pgid, sid int) error {
	n, ok := h.tracker.Lookup(pid)
	if !ok {
		return ErrUnknownProcess
	}
	return h.tracker.Apply(GroupChanged{
		PID:      pid,
		PGID:     pgid,
		SID:      sid,
		FromPGID: n.PGID,
		FromSID:  n.SID,
	})
}

// CleanupTenant seals generation g and runs the full terminate-and-confirm
// protocol. It is a thin wrapper around Shutdown using real signals.
func (h *Host) CleanupTenant(ctx context.Context, g Gen, grace time.Duration) error {
	return Shutdown(ctx, h.tracker, g, ShutdownConfig{
		GracePeriod: grace,
		Killer:      h.killer,
	})
}
