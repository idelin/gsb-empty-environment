package oci

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Linux capability numbers used by the policy. The complete numbering is
// included so reports can name arbitrary decoded masks.
const (
	CAP_CHOWN              Capability = 0
	CAP_DAC_OVERRIDE       Capability = 1
	CAP_DAC_READ_SEARCH    Capability = 2
	CAP_FOWNER             Capability = 3
	CAP_FSETID             Capability = 4
	CAP_KILL               Capability = 5
	CAP_SETGID             Capability = 6
	CAP_SETUID             Capability = 7
	CAP_SETPCAP            Capability = 8
	CAP_LINUX_IMMUTABLE    Capability = 9
	CAP_NET_BIND_SERVICE   Capability = 10
	CAP_NET_BROADCAST      Capability = 11
	CAP_NET_ADMIN          Capability = 12
	CAP_NET_RAW            Capability = 13
	CAP_IPC_LOCK           Capability = 14
	CAP_IPC_OWNER          Capability = 15
	CAP_SYS_MODULE         Capability = 16
	CAP_SYS_RAWIO          Capability = 17
	CAP_SYS_CHROOT         Capability = 18
	CAP_SYS_PTRACE         Capability = 19
	CAP_SYS_PACCT          Capability = 20
	CAP_SYS_ADMIN          Capability = 21
	CAP_SYS_BOOT           Capability = 22
	CAP_SYS_NICE           Capability = 23
	CAP_SYS_RESOURCE       Capability = 24
	CAP_SYS_TIME           Capability = 25
	CAP_SYS_TTY_CONFIG     Capability = 26
	CAP_MKNOD              Capability = 27
	CAP_LEASE              Capability = 28
	CAP_AUDIT_WRITE        Capability = 29
	CAP_AUDIT_CONTROL      Capability = 30
	CAP_SETFCAP            Capability = 31
	CAP_MAC_OVERRIDE       Capability = 32
	CAP_MAC_ADMIN          Capability = 33
	CAP_SYSLOG             Capability = 34
	CAP_WAKE_ALARM         Capability = 35
	CAP_BLOCK_SUSPEND      Capability = 36
	CAP_AUDIT_READ         Capability = 37
	CAP_PERFMON            Capability = 38
	CAP_BPF                Capability = 39
	CAP_CHECKPOINT_RESTORE Capability = 40
)

// VFS capability xattr constants from Linux's uapi capability.h.
const (
	VFSCapabilityRevisionMask  uint32 = 0xff000000
	VFSCapabilityFlagsMask     uint32 = 0x00ffffff
	VFSCapabilityFlagEffective uint32 = 0x000001

	VFSCapabilityRevision1 uint32 = 0x01000000
	VFSCapabilityRevision2 uint32 = 0x02000000
	VFSCapabilityRevision3 uint32 = 0x03000000

	SecurityCapabilityXattr = "security.capability"
)

// Capability is a Linux capability bit number.
type Capability uint

// CapabilitySet contains the decoded security.capability payload.
type CapabilitySet struct {
	// Magic is the raw little-endian magic_etc word.
	Magic uint32 `json:"magic"`
	// MagicEtc is an alias for Magic.
	MagicEtc uint32 `json:"magic_etc"`
	// Revision is the masked VFS_CAP_REVISION_* value.
	Revision uint32 `json:"revision"`
	// Version is an alias for Revision.
	Version uint32 `json:"version"`
	// Flags contains the low 24 flag bits.
	Flags uint32 `json:"flags"`
	// Effective reports VFS_CAP_FLAGS_EFFECTIVE.
	Effective bool `json:"effective"`
	// Permitted is the complete 64-bit permitted mask.
	Permitted uint64 `json:"permitted"`
	// Inheritable is the complete 64-bit inheritable mask.
	Inheritable uint64 `json:"inheritable"`
	// RootID is present for revision 3 payloads.
	RootID uint32 `json:"root_id,omitempty"`
	// RootIDPresent distinguishes rootid zero from revision 2.
	RootIDPresent bool `json:"root_id_present"`
}

// Capability is retained as the common noun alias while the decoded payload
// type has the more explicit name CapabilitySet.
type CapabilityInfo = CapabilitySet

var (
	ErrCapabilityTooShort  = errors.New("security.capability payload is too short")
	ErrCapabilityLength    = errors.New("invalid security.capability payload length")
	ErrCapabilityRevision  = errors.New("unsupported security.capability revision/magic")
	ErrCapabilityTruncated = errors.New("truncated security.capability payload")
)

// DecodeCapability decodes a raw binary security.capability xattr.
func DecodeCapability(data []byte) (*CapabilitySet, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("%w: need at least 4 bytes, got %d", ErrCapabilityTooShort, len(data))
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	revision := magic & VFSCapabilityRevisionMask
	flags := magic & VFSCapabilityFlagsMask
	caps := &CapabilitySet{
		Magic:     magic,
		MagicEtc:  magic,
		Revision:  revision,
		Version:   revision,
		Flags:     flags,
		Effective: flags&VFSCapabilityFlagEffective != 0,
	}

	var u32Count int
	switch revision {
	case VFSCapabilityRevision1:
		u32Count = 1
		if len(data) != 12 {
			return nil, fmt.Errorf("%w: revision 1 requires 12 bytes, got %d", ErrCapabilityLength, len(data))
		}
	case VFSCapabilityRevision2:
		u32Count = 2
		if len(data) != 20 {
			return nil, fmt.Errorf("%w: revision 2 requires 20 bytes, got %d", ErrCapabilityLength, len(data))
		}
	case VFSCapabilityRevision3:
		u32Count = 2
		if len(data) != 24 {
			return nil, fmt.Errorf("%w: revision 3 requires 24 bytes, got %d", ErrCapabilityLength, len(data))
		}
	default:
		return nil, fmt.Errorf("%w: 0x%08x", ErrCapabilityRevision, revision)
	}

	readU32 := func(offset int) (uint32, error) {
		if offset+4 > len(data) {
			return 0, ErrCapabilityTruncated
		}
		return binary.LittleEndian.Uint32(data[offset : offset+4]), nil
	}

	// data[0] contains the low 32 bits; data[1] contains bits 32..63.
	for i := 0; i < u32Count; i++ {
		permittedLow, err := readU32(4 + i*8)
		if err != nil {
			return nil, err
		}
		inheritableLow, err := readU32(8 + i*8)
		if err != nil {
			return nil, err
		}
		caps.Permitted |= uint64(permittedLow) << (32 * i)
		caps.Inheritable |= uint64(inheritableLow) << (32 * i)
	}

	if revision == VFSCapabilityRevision3 {
		rootID, err := readU32(20)
		if err != nil {
			return nil, err
		}
		caps.RootID = rootID
		caps.RootIDPresent = true
	}

	return caps, nil
}

// DecodeCapabilityValue accepts raw xattr bytes or the string forms commonly
// produced by tar/PAX and libarchive.
func DecodeCapabilityValue(value any) (*CapabilitySet, error) {
	payload, err := capabilityBytes(value)
	if err != nil {
		return nil, err
	}
	if caps, err := DecodeCapability(payload); err == nil {
		return caps, nil
	} else {
		rawErr := err
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if decoded, derr := enc.DecodeString(strings.TrimSpace(string(payload))); derr == nil {
				if caps, err := DecodeCapability(decoded); err == nil {
					return caps, nil
				}
			}
		}
		if decoded, herr := hex.DecodeString(strings.TrimSpace(string(payload))); herr == nil {
			if caps, err := DecodeCapability(decoded); err == nil {
				return caps, nil
			}
		}
		return nil, rawErr
	}
}

func capabilityBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return nil, ErrCapabilityTooShort
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("unsupported capability xattr type %T", value)
	}
}

// EncodeCapability encodes a v2 or v3 payload. Revision zero defaults to v2;
// RootIDPresent or a nonzero RootID selects v3.
func EncodeCapability(caps *CapabilitySet) ([]byte, error) {
	if caps == nil {
		return nil, ErrCapabilityTooShort
	}
	revision := caps.Revision
	if revision == 0 {
		revision = caps.Version
	}
	if revision == 0 {
		if caps.RootIDPresent || caps.RootID != 0 {
			revision = VFSCapabilityRevision3
		} else {
			revision = VFSCapabilityRevision2
		}
	}
	if revision != VFSCapabilityRevision2 && revision != VFSCapabilityRevision3 && revision != VFSCapabilityRevision1 {
		return nil, fmt.Errorf("%w: 0x%08x", ErrCapabilityRevision, revision)
	}

	magic := revision | (caps.Flags & VFSCapabilityFlagsMask)
	if caps.Effective {
		magic |= VFSCapabilityFlagEffective
	}

	var size int
	var groups int
	switch revision {
	case VFSCapabilityRevision1:
		size, groups = 12, 1
	case VFSCapabilityRevision2:
		size, groups = 20, 2
	case VFSCapabilityRevision3:
		size, groups = 24, 2
	}
	out := make([]byte, size)
	binary.LittleEndian.PutUint32(out[0:4], magic)
	for i := 0; i < groups; i++ {
		binary.LittleEndian.PutUint32(out[4+i*8:8+i*8], uint32(caps.Permitted>>(32*i)))
		binary.LittleEndian.PutUint32(out[8+i*8:12+i*8], uint32(caps.Inheritable>>(32*i)))
	}
	if revision == VFSCapabilityRevision3 {
		binary.LittleEndian.PutUint32(out[20:24], caps.RootID)
	}
	return out, nil
}

// Has reports whether cap is present in the permitted mask.
func (c *CapabilitySet) Has(cap Capability) bool {
	return c != nil && c.Permitted&(uint64(1)<<uint(cap)) != 0
}

// HasPermitted is an explicit alias for Has.
func (c *CapabilitySet) HasPermitted(cap Capability) bool { return c.Has(cap) }

// HasInheritable reports whether cap is in the inheritable mask.
func (c *CapabilitySet) HasInheritable(cap Capability) bool {
	return c != nil && c.Inheritable&(uint64(1)<<uint(cap)) != 0
}

// CapabilityName returns the canonical Linux name for a capability.
func CapabilityName(cap Capability) string {
	if name, ok := capabilityNames[cap]; ok {
		return name
	}
	return fmt.Sprintf("CAP_UNKNOWN_%d", cap)
}

// ParseCapabilityName parses names such as CAP_SYS_ADMIN or SYS_ADMIN.
func ParseCapabilityName(name string) (Capability, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(name))
	if !strings.HasPrefix(normalized, "CAP_") {
		normalized = "CAP_" + normalized
	}
	for cap, candidate := range capabilityNames {
		if candidate == normalized {
			return cap, true
		}
	}
	return 0, false
}

// Names returns canonical names for all set permitted bits.
func (c *CapabilitySet) Names() []string {
	if c == nil {
		return nil
	}
	var names []string
	for bit := Capability(0); bit < 64; bit++ {
		if c.Permitted&(1<<uint(bit)) != 0 {
			names = append(names, CapabilityName(bit))
		}
	}
	return names
}

// Dangerous returns the dangerous policy capabilities present in Permitted.
func (c *CapabilitySet) Dangerous(dangerous ...Capability) []Capability {
	if c == nil {
		return nil
	}
	if len(dangerous) == 0 {
		dangerous = DefaultDangerousCapabilities
	}
	var found []Capability
	for _, cap := range dangerous {
		if c.Has(cap) {
			found = append(found, cap)
		}
	}
	return found
}

// DefaultDangerousCapabilities is the required minimum dangerous set.
var DefaultDangerousCapabilities = []Capability{
	CAP_SYS_ADMIN,
	CAP_SETUID,
	CAP_DAC_OVERRIDE,
	CAP_NET_RAW,
}

var capabilityNames = map[Capability]string{
	CAP_CHOWN:              "CAP_CHOWN",
	CAP_DAC_OVERRIDE:       "CAP_DAC_OVERRIDE",
	CAP_DAC_READ_SEARCH:    "CAP_DAC_READ_SEARCH",
	CAP_FOWNER:             "CAP_FOWNER",
	CAP_FSETID:             "CAP_FSETID",
	CAP_KILL:               "CAP_KILL",
	CAP_SETGID:             "CAP_SETGID",
	CAP_SETUID:             "CAP_SETUID",
	CAP_SETPCAP:            "CAP_SETPCAP",
	CAP_LINUX_IMMUTABLE:    "CAP_LINUX_IMMUTABLE",
	CAP_NET_BIND_SERVICE:   "CAP_NET_BIND_SERVICE",
	CAP_NET_BROADCAST:      "CAP_NET_BROADCAST",
	CAP_NET_ADMIN:          "CAP_NET_ADMIN",
	CAP_NET_RAW:            "CAP_NET_RAW",
	CAP_IPC_LOCK:           "CAP_IPC_LOCK",
	CAP_IPC_OWNER:          "CAP_IPC_OWNER",
	CAP_SYS_MODULE:         "CAP_SYS_MODULE",
	CAP_SYS_RAWIO:          "CAP_SYS_RAWIO",
	CAP_SYS_CHROOT:         "CAP_SYS_CHROOT",
	CAP_SYS_PTRACE:         "CAP_SYS_PTRACE",
	CAP_SYS_PACCT:          "CAP_SYS_PACCT",
	CAP_SYS_ADMIN:          "CAP_SYS_ADMIN",
	CAP_SYS_BOOT:           "CAP_SYS_BOOT",
	CAP_SYS_NICE:           "CAP_SYS_NICE",
	CAP_SYS_RESOURCE:       "CAP_SYS_RESOURCE",
	CAP_SYS_TIME:           "CAP_SYS_TIME",
	CAP_SYS_TTY_CONFIG:     "CAP_SYS_TTY_CONFIG",
	CAP_MKNOD:              "CAP_MKNOD",
	CAP_LEASE:              "CAP_LEASE",
	CAP_AUDIT_WRITE:        "CAP_AUDIT_WRITE",
	CAP_AUDIT_CONTROL:      "CAP_AUDIT_CONTROL",
	CAP_SETFCAP:            "CAP_SETFCAP",
	CAP_MAC_OVERRIDE:       "CAP_MAC_OVERRIDE",
	CAP_MAC_ADMIN:          "CAP_MAC_ADMIN",
	CAP_SYSLOG:             "CAP_SYSLOG",
	CAP_WAKE_ALARM:         "CAP_WAKE_ALARM",
	CAP_BLOCK_SUSPEND:      "CAP_BLOCK_SUSPEND",
	CAP_AUDIT_READ:         "CAP_AUDIT_READ",
	CAP_PERFMON:            "CAP_PERFMON",
	CAP_BPF:                "CAP_BPF",
	CAP_CHECKPOINT_RESTORE: "CAP_CHECKPOINT_RESTORE",
}
