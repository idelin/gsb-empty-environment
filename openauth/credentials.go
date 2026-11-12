package openauth

// AuthSysCreds is the parsed AUTH_SYS (RFC 5531) credential presented on the
// RPC call carrying the OPEN. Auxiliary groups beyond the primary GID are
// included for mode-bit based access decisions.
type AuthSysCreds struct {
	UID    uint32
	GID    uint32
	Groups []uint32
}

// Identity is the effective identity used for authorization after the
// caller supplied root squashing rule has been applied.
type Identity struct {
	UID    uint32
	GID    uint32
	Groups []uint32
	// Privileged marks identities that retain superuser semantics. Such an
	// identity may set the owner/group create attributes to arbitrary values.
	Privileged bool
}

// IsGroup reports whether gid is the primary group or one of the
// supplementary groups of the identity.
func (id Identity) IsGroup(gid uint32) bool {
	if id.GID == gid {
		return true
	}
	for _, g := range id.Groups {
		if g == gid {
			return true
		}
	}
	return false
}

// UIDMapper applies a site policy such as root squashing to the raw
// AUTH_SYS credential and returns the effective identity. Implementations
// must be safe for concurrent use.
type UIDMapper interface {
	Map(AuthSysCreds) Identity
}

// UIDMapFunc adapts a plain function to the UIDMapper interface.
type UIDMapFunc func(AuthSysCreds) Identity

// Map calls f(creds).
func (f UIDMapFunc) Map(creds AuthSysCreds) Identity { return f(creds) }

// SquashConfig configures the classic NFS export root-squashing policy.
type SquashConfig struct {
	// SquashUID/SquashGID replace the credential of a squashed caller
	// (traditionally 65534, the nobody/nogroup identity).
	SquashUID uint32
	SquashGID uint32

	// NoRootSquash disables squashing of uid 0 entirely. When true the
	// credential is passed through unchanged and root stays privileged.
	NoRootSquash bool

	// AllSquash maps every credential, not just root, to SquashUID/GID.
	AllSquash bool

	// UIDExempt/GIDExempt list identities that must never be squashed even
	// when AllSquash is enabled. uid 0 is always exempt when NoRootSquash
	// is set, regardless of these lists.
	UIDExempt []uint32
	GIDExempt []uint32

	// PassthroughPrivilegedUIDs are UIDs that keep Privileged=true after a
	// plain passthrough (no squashing). uid 0 is always privileged.
	PassthroughPrivilegedUIDs []uint32
}

// NewRootSquashMapper builds a UIDMapper implementing the configured
// squash policy. A zero value gives the conventional behavior: uid 0 (and
// gid 0) are squashed to the nobody identity (65534) and lose privilege.
func NewRootSquashMapper(cfg SquashConfig) UIDMapper {
	if cfg.SquashUID == 0 {
		cfg.SquashUID = 65534
	}
	if cfg.SquashGID == 0 {
		cfg.SquashGID = 65534
	}
	return UIDMapFunc(func(creds AuthSysCreds) Identity {
		root := creds.UID == 0
		if root && cfg.NoRootSquash {
			return Identity{
				UID:        creds.UID,
				GID:        creds.GID,
				Groups:     append([]uint32(nil), creds.Groups...),
				Privileged: true,
			}
		}

		squashed := cfg.AllSquash && !uidExempt(creds.UID, cfg.UIDExempt) &&
			!gidExempt(creds.GID, cfg.GIDExempt)
		if root || squashed {
			return Identity{
				UID:        cfg.SquashUID,
				GID:        cfg.SquashGID,
				Groups:     []uint32{cfg.SquashGID},
				Privileged: false,
			}
		}

		return Identity{
			UID:        creds.UID,
			GID:        creds.GID,
			Groups:     append([]uint32(nil), creds.Groups...),
			Privileged: uidExempt(creds.UID, cfg.PassthroughPrivilegedUIDs),
		}
	})
}

func uidExempt(uid uint32, list []uint32) bool {
	for _, u := range list {
		if u == uid {
			return true
		}
	}
	return false
}

func gidExempt(gid uint32, list []uint32) bool {
	for _, g := range list {
		if g == gid {
			return true
		}
	}
	return false
}
