package netsec

import "fmt"

// Perm is a bitmask of device access permissions.
type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
	PermMknod
)

// PermAll grants read, write and mknod.
const PermAll = PermRead | PermWrite | PermMknod

// permBits lists permission bits in canonical comparison order.
var permBits = []Perm{PermRead, PermWrite, PermMknod}

func (p Perm) String() string {
	if p == 0 {
		return "-"
	}
	s := make([]byte, 0, 3)
	if p&PermRead != 0 {
		s = append(s, 'r')
	}
	if p&PermWrite != 0 {
		s = append(s, 'w')
	}
	if p&PermMknod != 0 {
		s = append(s, 'm')
	}
	return string(s)
}

// MaxDeviceNumber is the inclusive upper bound for major/minor numbers.
const MaxDeviceNumber = 4095

// Device types.
const (
	TypeChar  = 'c'
	TypeBlock = 'b'
	TypeAll   = 'a'
)

// Device identifies a single char or block device.
type Device struct {
	Type  byte // TypeChar or TypeBlock
	Major int
	Minor int
}

func (d Device) String() string {
	return fmt.Sprintf("%c %d:%d", d.Type, d.Major, d.Minor)
}

func (d Device) valid() error {
	if d.Type != TypeChar && d.Type != TypeBlock {
		return fmt.Errorf("device %v: type must be 'c' or 'b'", d)
	}
	if d.Major < 0 || d.Major > MaxDeviceNumber || d.Minor < 0 || d.Minor > MaxDeviceNumber {
		return fmt.Errorf("device %v: major/minor out of range 0..%d", d, MaxDeviceNumber)
	}
	return nil
}

// typeRank orders device types as c < b for minimal-witness selection.
func typeRank(t byte) int {
	if t == TypeChar {
		return 0
	}
	return 1
}

// deviceLess orders devices by type (c<b), then major, then minor.
func deviceLess(x, y Device) bool {
	if typeRank(x.Type) != typeRank(y.Type) {
		return typeRank(x.Type) < typeRank(y.Type)
	}
	if x.Major != y.Major {
		return x.Major < y.Major
	}
	return x.Minor < y.Minor
}

// DevicePerm pairs a device with a permission set.
type DevicePerm struct {
	Device
	Perms Perm
}

func (dp DevicePerm) String() string {
	return fmt.Sprintf("%v %v", dp.Device, dp.Perms)
}

// Effect is the action a rule applies to matched permissions.
type Effect int

const (
	Allow Effect = iota
	Deny
)

func (e Effect) String() string {
	if e == Deny {
		return "deny"
	}
	return "allow"
}

// Rule is one device policy rule. A nil Major or Minor is the wildcard '*'.
// Type may be 'a' (matches both char and block devices), 'c' or 'b'.
type Rule struct {
	Effect Effect
	Type   byte
	Major  *int
	Minor  *int
	Perms  Perm
}

func (r Rule) String() string {
	num := func(p *int) string {
		if p == nil {
			return "*"
		}
		return fmt.Sprintf("%d", *p)
	}
	return fmt.Sprintf("%s %c %s %s %v", r.Effect, r.Type, num(r.Major), num(r.Minor), r.Perms)
}

func (r Rule) valid() error {
	if r.Effect != Allow && r.Effect != Deny {
		return fmt.Errorf("rule %q: unknown effect", r)
	}
	if r.Type != TypeAll && r.Type != TypeChar && r.Type != TypeBlock {
		return fmt.Errorf("rule %q: type must be 'a', 'c' or 'b'", r)
	}
	for _, p := range []*int{r.Major, r.Minor} {
		if p != nil && (*p < 0 || *p > MaxDeviceNumber) {
			return fmt.Errorf("rule %q: major/minor out of range 0..%d", r, MaxDeviceNumber)
		}
	}
	if r.Perms&^PermAll != 0 {
		return fmt.Errorf("rule %q: unknown permission bits", r)
	}
	return nil
}
