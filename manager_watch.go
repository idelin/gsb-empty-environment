package netsec

import (
	"fmt"
	"path/filepath"
)

// noRevoke is the sentinel reason returned by diffGroup when the re-inspected
// group is still identical and safe.
const noRevoke RevokeReason = -1

// watchGroup registers the group's devices directory and every member's device
// directory. Watching the devices directory catches hot-plug/hot-remove of
// group members; watching each device directory catches its "driver" symlink
// appearing or disappearing on rebind.
func (m *Manager) watchGroup(ag *attachedGroup, info GroupInfo) error {
	resolver, ok := m.sys.(groupDirResolver)
	if !ok {
		return fmt.Errorf("sysfs backend %T cannot resolve group device directories", m.sys)
	}
	if err := ag.watcher.Add(resolver.GroupDevicesDir(ag.groupID)); err != nil {
		return err
	}
	for _, dev := range info.Devices {
		if err := ag.watcher.Add(dev.Path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) watchLoop(ag *attachedGroup) {
	defer close(ag.done)
	for {
		select {
		case <-ag.stop:
			return
		case _, ok := <-ag.watcher.Events():
			if !ok {
				m.evaluate(ag)
				return
			}
			if m.evaluate(ag) {
				return
			}
		}
	}
}

// evaluate re-inspects the group after a filesystem event and returns true if
// the watch loop should terminate because the group was revoked.
func (m *Manager) evaluate(ag *attachedGroup) bool {
	m.mu.Lock()
	// The group may have been released concurrently.
	if current, ok := m.groups[ag.groupID]; !ok || current != ag {
		m.mu.Unlock()
		return true
	}

	info, err := m.sys.InspectGroup(ag.groupID)
	if err != nil {
		m.mu.Unlock()
		m.revoke(ag, Revocation{
			GroupID:  ag.groupID,
			TenantID: ag.tenantID,
			Reason:   RevokeGroupGone,
			Detail:   err.Error(),
		})
		m.cleanupAfterRevoke(ag)
		return true
	}

	reason, detail := diffGroup(ag.baseline, info, m.allowed)
	if reason == noRevoke {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()

	m.revoke(ag, Revocation{
		GroupID:  ag.groupID,
		TenantID: ag.tenantID,
		Reason:   reason,
		Detail:   detail,
	})
	m.cleanupAfterRevoke(ag)
	return true
}

// revoke marks the lease invalid exactly once, delivers the Revocation to its
// channel and fires the configured hook asynchronously.
func (m *Manager) revoke(ag *attachedGroup, r Revocation) {
	ag.lease.once.Do(func() {
		ag.lease.revocation <- r
		close(ag.lease.revocation)
	})
	if m.hook != nil {
		go m.hook(r)
	}
}

// teardown stops the watch loop and closes the watcher exactly once, then
// waits for the loop goroutine to exit. It is safe to call from both the
// orderly Release/Close paths and the runtime revocation path.
func (m *Manager) teardown(ag *attachedGroup) {
	ag.shutdown.Do(func() {
		close(ag.stop)
		_ = ag.watcher.Close()
	})
	<-ag.done
}

// cleanupAfterRevoke removes the group from the registry and stops its
// watcher after a revocation has been decided.
func (m *Manager) cleanupAfterRevoke(ag *attachedGroup) {
	m.mu.Lock()
	if existing, ok := m.groups[ag.groupID]; ok && existing == ag {
		delete(m.groups, ag.groupID)
	}
	m.mu.Unlock()

	m.teardown(ag)
}

// diffGroup compares the baseline snapshot taken at attach time with the
// current snapshot and decides whether the group must be revoked. The event
// that triggered the re-inspection is used only to sharpen the reason; the
// snapshots are the source of truth.
//
// Priority: a newly appearing member (hot-plug) revokes before anything else;
// then a changed driver binding; then a removed member; then a generic
// integrity failure. A stable, still-safe group returns noRevoke.
func diffGroup(baseline, current GroupInfo, allowed map[string]struct{}) (RevokeReason, string) {
	base := indexDevices(baseline.Devices)
	cur := indexDevices(current.Devices)

	for id, dev := range cur {
		old, existed := base[id]
		if !existed {
			return RevokeHotplug, fmt.Sprintf("device %s appeared in group %d", id, current.ID)
		}
		if old.Driver != dev.Driver {
			return RevokeRebind, fmt.Sprintf("device %s driver changed %q -> %q",
				id, driverLabel(old.Driver), driverLabel(dev.Driver))
		}
	}

	for id := range base {
		if _, exists := cur[id]; !exists {
			return RevokeHotRemove, fmt.Sprintf("device %s disappeared from group %d", id, current.ID)
		}
	}

	// Membership and bindings are unchanged; still re-verify integrity so a
	// driver that silently flipped outside a watched path cannot survive.
	if err := CheckGroup(current, allowed); err != nil {
		return RevokeIntegrity, err.Error()
	}

	return noRevoke, ""
}

// sameMembership reports whether two snapshots describe the same devices with
// the same driver bindings.
func sameMembership(a, b GroupInfo) bool {
	if len(a.Devices) != len(b.Devices) {
		return false
	}
	ai := indexDevices(a.Devices)
	for _, dev := range b.Devices {
		other, ok := ai[dev.ID]
		if !ok || other.Driver != dev.Driver || other.Path != dev.Path {
			return false
		}
	}
	return true
}

func indexDevices(devs []Device) map[string]Device {
	out := make(map[string]Device, len(devs))
	for _, dev := range devs {
		out[dev.ID] = dev
	}
	return out
}

func driverLabel(driver string) string {
	if driver == "" {
		return "(none)"
	}
	return driver
}

// GroupDevicesDir returns the on-disk directory listing the members of a group.
func (s *LinuxSysfs) GroupDevicesDir(groupID int) string {
	return filepath.Join(s.groupPath(groupID), "devices")
}
