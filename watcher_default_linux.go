//go:build linux

package netsec

import "time"

// newDefaultWatcher returns the preferred watcher for a production host.
// inotify is used for immediate kernel-driven notifications; a force-poll
// option keeps the portable backend available.
func newDefaultWatcher(forcePoll bool, interval time.Duration) (Watcher, error) {
	if forcePoll {
		return NewPollingWatcher(interval), nil
	}
	return NewInotifyWatcher()
}
