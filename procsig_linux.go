//go:build linux

package hostlock

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

var bootID = sync.OnceValue(func() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
})

// processStartSig returns bootID + process start time (jiffies since boot),
// which uniquely identifies a process instance on this host boot. A reused
// PID yields a different signature.
func processStartSig(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// comm (field 2) may contain spaces and parentheses; fields after the
	// last ')' start at field 3 (state). starttime is field 22.
	s := string(data)
	idx := strings.LastIndexByte(s, ')')
	if idx < 0 || idx+2 >= len(s) {
		return ""
	}
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 20 {
		return ""
	}
	return bootID() + ":" + fields[19]
}
