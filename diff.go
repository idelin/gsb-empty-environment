package netsec

import "bytes"

// DeviceListDiff describes observable changes between two complete list
// snapshots. Online-only changes are reported separately and never count as
// identity changes.
type DeviceListDiff struct {
	OldVersion      uint64
	NewVersion      uint64
	Added           []string
	Removed         []string
	KeyChanged      []string
	VersionChanged  []string
	PresenceChanged []string
	Reordered       bool
}

// IdentityChanged reports whether any device identity changed: addition,
// removal, public-key change, or added-version change. Online changes are
// excluded.
func (d DeviceListDiff) IdentityChanged() bool {
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.KeyChanged) > 0 || len(d.VersionChanged) > 0
}

// RevokesConfirmation reports whether the change must revoke a user
// confirmation. A list-version change with an identical device identity set
// makes the old binding stale but is not itself a device identity change.
func (d DeviceListDiff) RevokesConfirmation() bool {
	return d.IdentityChanged()
}

// RequiresReverification reports whether the old confirmation/verification
// binding can no longer describe the current list.
func (d DeviceListDiff) RequiresReverification() bool {
	return d.IdentityChanged() || d.OldVersion != d.NewVersion
}

// DiffDeviceLists compares two complete snapshots.
func DiffDeviceLists(oldSnapshot, newSnapshot *DeviceListSnapshot) (DeviceListDiff, error) {
	if oldSnapshot == nil || newSnapshot == nil {
		return DeviceListDiff{}, ErrNilSnapshot
	}
	left, err := CanonicalDevices(oldSnapshot.Devices)
	if err != nil {
		return DeviceListDiff{}, err
	}
	right, err := CanonicalDevices(newSnapshot.Devices)
	if err != nil {
		return DeviceListDiff{}, err
	}

	type entry struct {
		key          []byte
		addedVersion uint64
		online       bool
	}
	oldByID := make(map[string]entry, len(left))
	newByID := make(map[string]entry, len(right))
	for _, device := range left {
		oldByID[device.DeviceID] = entry{device.IdentityPublicKey, device.AddedVersion, device.Online}
	}
	for _, device := range right {
		newByID[device.DeviceID] = entry{device.IdentityPublicKey, device.AddedVersion, device.Online}
	}

	diff := DeviceListDiff{OldVersion: oldSnapshot.Version, NewVersion: newSnapshot.Version}
	for deviceID, oldEntry := range oldByID {
		newEntry, exists := newByID[deviceID]
		if !exists {
			diff.Removed = append(diff.Removed, deviceID)
			continue
		}
		if !bytes.Equal(oldEntry.key, newEntry.key) {
			diff.KeyChanged = append(diff.KeyChanged, deviceID)
		}
		if oldEntry.addedVersion != newEntry.addedVersion {
			diff.VersionChanged = append(diff.VersionChanged, deviceID)
		}
		if oldEntry.online != newEntry.online {
			diff.PresenceChanged = append(diff.PresenceChanged, deviceID)
		}
	}
	for deviceID := range newByID {
		if _, exists := oldByID[deviceID]; !exists {
			diff.Added = append(diff.Added, deviceID)
		}
	}

	diff.Reordered = len(left) == len(right) && !sameDeviceIDOrder(left, right)
	diff.Added = sortedStrings(diff.Added)
	diff.Removed = sortedStrings(diff.Removed)
	diff.KeyChanged = sortedStrings(diff.KeyChanged)
	diff.VersionChanged = sortedStrings(diff.VersionChanged)
	diff.PresenceChanged = sortedStrings(diff.PresenceChanged)
	return diff, nil
}

// DiffSnapshots is an alias for DiffDeviceLists.
func DiffSnapshots(oldSnapshot, newSnapshot *DeviceListSnapshot) (DeviceListDiff, error) {
	return DiffDeviceLists(oldSnapshot, newSnapshot)
}

func sameDeviceIDOrder(left, right []Device) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].DeviceID != right[i].DeviceID {
			return false
		}
	}
	return true
}

func sortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}
