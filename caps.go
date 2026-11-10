package main

import (
	"fmt"
	"strconv"
	"strings"
)

// CapMask is a capability set. Bit c represents capability number c.
type CapMask uint64

// CapLastCap is the highest capability number modeled here (CAP_LAST_CAP in
// recent kernels). capset/exec inputs cannot reference anything above it.
const CapLastCap = 40

// AllCaps is the full set 0..CapLastCap.
const AllCaps CapMask = 1<<(CapLastCap+1) - 1

// capNames is the canonical capabilities(7) naming table, ordered by number.
var capNames = []string{
	"cap_chown",              // 0
	"cap_dac_override",       // 1
	"cap_dac_read_search",    // 2
	"cap_fowner",             // 3
	"cap_fsetid",             // 4
	"cap_kill",               // 5
	"cap_setgid",             // 6
	"cap_setuid",             // 7
	"cap_setpcap",            // 8
	"cap_linux_immutable",    // 9
	"cap_net_bind_service",   // 10
	"cap_net_broadcast",      // 11
	"cap_net_admin",          // 12
	"cap_net_raw",            // 13
	"cap_ipc_lock",           // 14
	"cap_ipc_owner",          // 15
	"cap_sys_module",         // 16
	"cap_sys_rawio",          // 17
	"cap_sys_chroot",         // 18
	"cap_sys_ptrace",         // 19
	"cap_sys_pacct",          // 20
	"cap_sys_admin",          // 21
	"cap_sys_boot",           // 22
	"cap_sys_nice",           // 23
	"cap_sys_resource",       // 24
	"cap_sys_time",           // 25
	"cap_sys_tty_config",     // 26
	"cap_mknod",              // 27
	"cap_lease",              // 28
	"cap_audit_write",        // 29
	"cap_audit_control",      // 30
	"cap_setfcap",            // 31
	"cap_mac_override",       // 32
	"cap_mac_admin",          // 33
	"cap_syslog",             // 34
	"cap_wake_alarm",         // 35
	"cap_block_suspend",      // 36
	"cap_audit_read",         // 37
	"cap_perfmon",            // 38
	"cap_bpf",                // 39
	"cap_checkpoint_restore", // 40
}

// CapSetPCAP is needed by prctl(PR_CAPBSET_DROP) and gates inheritable raises.
const CapSetPCAP CapMask = 1 << 8

// capNumber resolves a token such as "cap_net_admin" or "38" to its bit.
func capNumber(tok string) (uint, error) {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if n, err := strconv.ParseUint(tok, 10, 32); err == nil {
		if n > CapLastCap {
			return 0, fmt.Errorf("capability %d exceeds CAP_LAST_CAP (%d)", n, CapLastCap)
		}
		return uint(n), nil
	}
	for i, name := range capNames {
		if name == tok {
			return uint(i), nil
		}
	}
	return 0, fmt.Errorf("unknown capability token %q", tok)
}

// parseCapSet accepts "none", "all", or a comma-separated list of tokens.
// Whitespace around tokens is tolerated; the empty string means the empty set.
func parseCapSet(s string) (CapMask, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "none" {
		return 0, nil
	}
	if s == "all" || s == "*" {
		return AllCaps, nil
	}
	var mask CapMask
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		n, err := capNumber(tok)
		if err != nil {
			return 0, err
		}
		mask |= 1 << n
	}
	return mask, nil
}

// capName renders one bit, falling back to capNN for out-of-table numbers.
func capName(bit uint) string {
	if int(bit) < len(capNames) {
		return capNames[bit]
	}
	return fmt.Sprintf("cap%d", bit)
}

// renderSet prints a set as ordered, comma-separated capability names.
func renderSet(m CapMask) string {
	if m == 0 {
		return "none"
	}
	var parts []string
	for b := uint(0); b <= CapLastCap; b++ {
		if m&(1<<b) != 0 {
			parts = append(parts, capName(b))
		}
	}
	return strings.Join(parts, ",")
}

// bits returns the set bit numbers in ascending order.
func (m CapMask) bits() []uint {
	var out []uint
	for b := uint(0); b <= CapLastCap; b++ {
		if m&(1<<b) != 0 {
			out = append(out, b)
		}
	}
	return out
}
