//go:build !linux

package netsec

import "time"

// newDefaultWatcher returns a polling watcher on non-Linux platforms. Real
// sysfs/inotify only exist on Linux; the polling backend keeps the package
// buildable and fully testable everywhere else.
func newDefaultWatcher(forcePoll bool, interval time.Duration) (Watcher, error) {
	return NewPollingWatcher(interval), nil
}
