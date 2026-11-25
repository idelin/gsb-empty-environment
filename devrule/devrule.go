// Package devrule compiles ordered container device allow/deny rules into a
// disjoint symbolic region decision diagram, and verifies the compiled
// result against a target policy without enumerating the device space.
//
// A device request is a (type, major, minor, permissions) tuple where type
// is 'c' (char) or 'b' (block) and permissions are drawn from {r, w, m}.
// Rules carry an effect (Allow or Deny), a type selector ('c', 'b' or 'a'
// for both), a major/minor number or '*', and a permission set. Rules apply
// in order: Allow unions matching permissions into each device's set, Deny
// subtracts them, and every device starts with the empty set. The 'm'
// permission only controls device-node creation; it never implies r or w.
package devrule

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxDeviceNumber is the largest valid major/minor number.
const MaxDeviceNumber = 4095

// Wildcard marks a rule or target entry that matches any major/minor number.
const Wildcard = -1

// Perm is a bitmask over read/write/mknod permissions.
type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
	PermMknod
)

// PermAll is the full {r, w, m} permission set.
const PermAll = PermRead | PermWrite | PermMknod

var permBits = []Perm{PermRead, PermWrite, PermMknod}

func (p Perm) String() string {
	if p == 0 {
		return "-"
	}
	var b strings.Builder
	if p&PermRead != 0 {
		b.WriteByte('r')
	}
	if p&PermWrite != 0 {
		b.WriteByte('w')
	}
	if p&PermMknod != 0 {
		b.WriteByte('m')
	}
	return b.String()
}

// Kind is a device type: char or block.
type Kind int

const (
	Char Kind = iota
	Block
)

func (k Kind) String() string {
	if k == Char {
		return "c"
	}
	return "b"
}

// TypeSel selects which device types a rule or target entry matches.
type TypeSel int

const (
	SelChar TypeSel = iota
	SelBlock
	SelAll
)

func (s TypeSel) kinds() []Kind {
	switch s {
	case SelChar:
		return []Kind{Char}
	case SelBlock:
		return []Kind{Block}
	default:
		return []Kind{Char, Block}
	}
}

func (s TypeSel) String() string {
	switch s {
	case SelChar:
		return "c"
	case SelBlock:
		return "b"
	default:
		return "a"
	}
}

// Effect is the effect of a rule: allow or deny.
type Effect int

const (
	Allow Effect = iota
	Deny
)

func (e Effect) String() string {
	if e == Allow {
		return "allow"
	}
	return "deny"
}

// Rule is a single ordered device policy rule. Major and Minor are either a
// number in [0, MaxDeviceNumber] or Wildcard.
type Rule struct {
	Effect Effect
	Type   TypeSel
	Major  int
	Minor  int
	Perms  Perm
}

// Validate checks that the rule's numbers and permissions are in range.
func (r Rule) Validate() error {
	if r.Perms == 0 || r.Perms&^PermAll != 0 {
		return fmt.Errorf("invalid permission set %q", r.Perms.String())
	}
	if r.Major != Wildcard && (r.Major < 0 || r.Major > MaxDeviceNumber) {
		return fmt.Errorf("major %d out of range [0, %d]", r.Major, MaxDeviceNumber)
	}
	if r.Minor != Wildcard && (r.Minor < 0 || r.Minor > MaxDeviceNumber) {
		return fmt.Errorf("minor %d out of range [0, %d]", r.Minor, MaxDeviceNumber)
	}
	return nil
}

func numOrWildcard(n int) string {
	if n == Wildcard {
		return "*"
	}
	return strconv.Itoa(n)
}

func (r Rule) String() string {
	return fmt.Sprintf("%s %s %s:%s %s", r.Effect, r.Type,
		numOrWildcard(r.Major), numOrWildcard(r.Minor), r.Perms)
}

// ParseRule parses a rule body of the form "b 8:0 rw" or "a *:* rwm" with
// the given effect.
func ParseRule(effect Effect, spec string) (Rule, error) {
	fields := strings.Fields(spec)
	if len(fields) != 3 {
		return Rule{}, fmt.Errorf("rule %q: want \"<type> <major>:<minor> <perms>\"", spec)
	}
	var sel TypeSel
	switch fields[0] {
	case "c":
		sel = SelChar
	case "b":
		sel = SelBlock
	case "a":
		sel = SelAll
	default:
		return Rule{}, fmt.Errorf("rule %q: unknown type %q", spec, fields[0])
	}
	nums := strings.Split(fields[1], ":")
	if len(nums) != 2 {
		return Rule{}, fmt.Errorf("rule %q: want major:minor", spec)
	}
	parseNum := func(s string) (int, error) {
		if s == "*" {
			return Wildcard, nil
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("rule %q: bad number %q", spec, s)
		}
		if v < 0 {
			return 0, fmt.Errorf("rule %q: negative number %q (use * for wildcard)", spec, s)
		}
		return v, nil
	}
	major, err := parseNum(nums[0])
	if err != nil {
		return Rule{}, err
	}
	minor, err := parseNum(nums[1])
	if err != nil {
		return Rule{}, err
	}
	var perms Perm
	for _, c := range fields[2] {
		switch c {
		case 'r':
			perms |= PermRead
		case 'w':
			perms |= PermWrite
		case 'm':
			perms |= PermMknod
		default:
			return Rule{}, fmt.Errorf("rule %q: unknown permission %q", spec, c)
		}
	}
	rule := Rule{Effect: effect, Type: sel, Major: major, Minor: minor, Perms: perms}
	if err := rule.Validate(); err != nil {
		return Rule{}, err
	}
	return rule, nil
}
