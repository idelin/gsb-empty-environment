package netsec

import (
	"errors"
	"fmt"
	"reflect"
)

// SID is a security identifier. The package deliberately treats SIDs as
// opaque values; it does not need to parse the Windows binary representation.
type SID string

// Sid is an alias kept for callers that use Go's usual three-letter acronym
// spelling.
type Sid = SID

func (s SID) String() string { return string(s) }

// Privilege identifies a token privilege. Only the privileges explicitly
// described by this exercise have an effect during an access check.
type Privilege string

// PrivilegeID and TokenPrivilege are aliases for Privilege.
type PrivilegeID = Privilege
type TokenPrivilege = Privilege

const (
	SeBackupPrivilege        Privilege = "SeBackupPrivilege"
	SeTakeOwnershipPrivilege Privilege = "SeTakeOwnershipPrivilege"
	SeRestorePrivilege       Privilege = "SeRestorePrivilege"
	SeDebugPrivilege         Privilege = "SeDebugPrivilege"
	SeSecurityPrivilege      Privilege = "SeSecurityPrivilege"
	SeChangeNotifyPrivilege  Privilege = "SeChangeNotifyPrivilege"
)

// Common alternative spellings for the two effective privileges.
const (
	PrivilegeBackupRead    = SeBackupPrivilege
	PrivilegeTakeOwnership = SeTakeOwnershipPrivilege
	BackupReadPrivilege    = SeBackupPrivilege
	TakeOwnershipPrivilege = SeTakeOwnershipPrivilege
	SeBackup               = SeBackupPrivilege
	SeTakeOwnership        = SeTakeOwnershipPrivilege
	BackupPrivilege        = SeBackupPrivilege
	OwnershipPrivilege     = SeTakeOwnershipPrivilege
)

// IntegrityLevel is a small, ordered integrity domain. Larger values dominate
// smaller values.
type IntegrityLevel int

// Integrity and IntegrityLabel are aliases for IntegrityLevel.
type Integrity = IntegrityLevel
type IntegrityLabel = IntegrityLevel

const (
	IntegrityUntrusted IntegrityLevel = iota
	IntegrityLow
	IntegrityMedium
	IntegrityHigh
	IntegritySystem
)

const (
	Untrusted = IntegrityUntrusted
	Low       = IntegrityLow
	Medium    = IntegrityMedium
	High      = IntegrityHigh
	System    = IntegritySystem

	IntegrityLevelUntrusted = IntegrityUntrusted
	IntegrityLevelLow       = IntegrityLow
	IntegrityLevelMedium    = IntegrityMedium
	IntegrityLevelHigh      = IntegrityHigh
	IntegrityLevelSystem    = IntegritySystem
)

func (l IntegrityLevel) String() string {
	switch l {
	case IntegrityUntrusted:
		return "untrusted"
	case IntegrityLow:
		return "low"
	case IntegrityMedium:
		return "medium"
	case IntegrityHigh:
		return "high"
	case IntegritySystem:
		return "system"
	default:
		return fmt.Sprintf("integrity(%d)", int(l))
	}
}

// Token is the host-side model of the SIDs and privileges attached to a
// process. The three group lists are disjoint in a well-formed token.
type Token struct {
	UserSID           SID
	EnabledGroups     []SID
	DenyOnlyGroups    []SID
	DisabledGroups    []SID
	EnabledPrivileges []Privilege
	IntegrityLevel    IntegrityLevel
	RestrictedSIDs    []SID

	// Accepted alternative spellings. New tokens produced by this package
	// populate these only when the input token used them.
	User             SID
	UserSid          SID
	Groups           []SID
	DenyOnly         []SID
	Disabled         []SID
	Privileges       []Privilege
	Integrity        IntegrityLevel
	RestrictedSIDSet []SID
	RestrictedSids   []SID
}

// RestrictedToken and ProcessToken are aliases for Token.
type RestrictedToken = Token
type ProcessToken = Token

// SIDSet is a convenient set representation for callers that build SID
// collections programmatically.
type SIDSet map[SID]struct{}

// RestrictOptions describes a restricted-token derivation. Every operation is
// monotonic: groups may only move out of EnabledGroups, privileges may only be
// removed, integrity may only stay the same or decrease, and restricted SIDs
// may only be appended.
type RestrictOptions struct {
	// Groups to convert from enabled to deny-only. DenyOnly and ToDenyOnly are
	// accepted synonyms.
	DenyOnlyGroups []SID
	DenyOnly       []SID
	DenyOnlySIDs   []SID
	ToDenyOnly     []SID

	// Groups to move from enabled to disabled.
	DisabledGroups []SID
	DisableGroups  []SID
	Disable        []SID
	DisabledSIDs   []SID
	ToDisabled     []SID

	// Privileges to remove. Privileges, when non-nil, is the complete desired
	// final privilege list and may therefore also remove privileges.
	RemovePrivileges []Privilege
	RemovePrivilege  []Privilege
	Remove           []Privilege
	DeletePrivileges []Privilege
	Privileges       []Privilege
	KeepPrivileges   []Privilege
	AddPrivileges    []Privilege

	// IntegrityLevel is applied when non-zero. Integrity and NewIntegrity are
	// pointers so that an explicit transition to IntegrityUntrusted (zero) is
	// still representable.
	IntegrityLevel IntegrityLevel
	Integrity      *IntegrityLevel
	NewIntegrity   *IntegrityLevel
	LowerIntegrity *IntegrityLevel
	SetIntegrity   bool

	// Restricted SIDs are appended to the base token's restricted set.
	RestrictedSIDs    []SID
	AddRestrictedSIDs []SID
	Restricted        []SID
	RestrictedSidSet  []SID
	NewRestrictedSIDs []SID

	// These fields make attempts to widen the token explicit. Providing any of
	// them is reported as an escalation.
	EnableGroups []SID
	AddGroups    []SID
	NewGroups    []SID
	UserSID      *SID
	User         *SID
}

// DeriveOptions and RestrictedTokenOptions are conventional aliases.
type DeriveOptions = RestrictOptions
type RestrictedTokenOptions = RestrictOptions

var (
	ErrInvalidDerivation   = errors.New("netsec: invalid restricted-token derivation")
	ErrNewIdentity         = fmt.Errorf("%w: cannot add a new identity", ErrInvalidDerivation)
	ErrPrivilegeEscalation = fmt.Errorf("%w: cannot add a privilege", ErrInvalidDerivation)
	ErrIntegrityEscalation = fmt.Errorf("%w: cannot raise integrity", ErrInvalidDerivation)

	// More descriptive aliases.
	ErrCannotAddIdentity    = ErrNewIdentity
	ErrCannotAddPrivilege   = ErrPrivilegeEscalation
	ErrCannotRaiseIntegrity = ErrIntegrityEscalation
)

// DeriveRestricted derives a restricted token from base. Arguments may be a
// RestrictOptions value (or pointer), or the positional form:
//
//	disableGroups, denyOnlyGroups, restrictedSIDs []SID,
//	removePrivileges []Privilege, integrity IntegrityLevel
//
// Slices in the positional form are classified conservatively: a slice that
// contains an identity not already present in base can only be an appended
// restricted-SID set. Other slices fill the disabled and deny-only slots in
// that order.
func (t Token) DeriveRestricted(args ...any) (Token, error) {
	opts, err := parseRestrictOptions(t, args)
	if err != nil {
		return Token{}, err
	}
	return deriveRestricted(t, opts)
}

// Derive is an alias for DeriveRestricted.
func (t Token) Derive(args ...any) (Token, error) {
	return t.DeriveRestricted(args...)
}

// Restrict is an alias for DeriveRestricted.
func (t Token) Restrict(args ...any) (Token, error) {
	return t.DeriveRestricted(args...)
}

// DeriveRestricted is the package-level form of Token.DeriveRestricted.
func DeriveRestricted(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// DeriveToken is an alias for DeriveRestricted.
func DeriveToken(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// Derive is the package-level alias for DeriveRestricted.
func Derive(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// Restrict is the package-level alias for DeriveRestricted.
func Restrict(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// CreateRestrictedToken is the Windows-style name for DeriveRestricted.
func CreateRestrictedToken(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// RestrictToken is an alias for DeriveRestricted.
func RestrictToken(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// NewRestrictedToken is an alias for DeriveRestricted.
func NewRestrictedToken(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// DeriveRestrictedToken is an alias for DeriveRestricted.
func DeriveRestrictedToken(base Token, args ...any) (Token, error) {
	return base.DeriveRestricted(args...)
}

// EffectiveGroups returns the enabled groups that participate in Allow ACE
// matching. A SID that is also deny-only or disabled is excluded.
func (t Token) EffectiveGroups() []SID {
	t = normalizeToken(t)
	deny := makeSet(t.DenyOnlyGroups)
	disabled := makeSet(t.DisabledGroups)
	out := make([]SID, 0, len(t.EnabledGroups))
	for _, sid := range dedupeSIDs(t.EnabledGroups) {
		if _, ok := deny[sid]; ok {
			continue
		}
		if _, ok := disabled[sid]; ok {
			continue
		}
		out = append(out, sid)
	}
	return out
}

// HasPrivilege reports whether p is enabled in the token.
func (t Token) HasPrivilege(p Privilege) bool {
	return containsPrivilege(normalizeToken(t).EnabledPrivileges, p)
}

func deriveRestricted(base Token, opts RestrictOptions) (Token, error) {
	shape := aliasShapeOf(base)
	base = normalizeToken(base)
	out := cloneToken(base)

	out.EnabledGroups = dedupeSIDs(out.EnabledGroups)
	out.DenyOnlyGroups = dedupeSIDs(out.DenyOnlyGroups)
	out.DisabledGroups = dedupeSIDs(out.DisabledGroups)
	out.EnabledPrivileges = dedupePrivileges(out.EnabledPrivileges)
	out.RestrictedSIDs = dedupeSIDs(out.RestrictedSIDs)

	if out.UserSID == "" {
		return Token{}, fmt.Errorf("%w: base token has no user SID", ErrInvalidDerivation)
	}
	if uid := firstSID(opts.UserSID, opts.User); uid != nil && *uid != out.UserSID {
		return Token{}, fmt.Errorf("%w: cannot change user SID", ErrNewIdentity)
	}
	if len(opts.EnableGroups)+len(opts.AddGroups)+len(opts.NewGroups) != 0 {
		return Token{}, fmt.Errorf("%w: cannot enable or add groups", ErrNewIdentity)
	}

	enabled := makeSet(out.EnabledGroups)

	toDeny := dedupeSIDs(concatSIDs(opts.DenyOnlyGroups, opts.DenyOnly, opts.DenyOnlySIDs, opts.ToDenyOnly))
	toDisable := dedupeSIDs(concatSIDs(opts.DisabledGroups, opts.DisableGroups, opts.Disable, opts.DisabledSIDs, opts.ToDisabled))

	moveToDeny := makeSet(toDeny)
	for _, sid := range toDisable {
		if _, ok := moveToDeny[sid]; ok {
			return Token{}, fmt.Errorf("%w: group %q is both deny-only and disabled", ErrInvalidDerivation, sid)
		}
	}

	for _, sid := range toDeny {
		if _, ok := enabled[sid]; !ok {
			return Token{}, fmt.Errorf("%w: group %q is not an enabled group", ErrNewIdentity, sid)
		}
	}
	for _, sid := range toDisable {
		if _, ok := enabled[sid]; !ok {
			return Token{}, fmt.Errorf("%w: group %q is not an enabled group", ErrNewIdentity, sid)
		}
	}

	toDenySet := makeSet(toDeny)
	toDisableSet := makeSet(toDisable)

	out.EnabledGroups = filterSIDs(out.EnabledGroups, func(sid SID) bool {
		_, deny := toDenySet[sid]
		_, disable := toDisableSet[sid]
		return !deny && !disable
	})
	out.DenyOnlyGroups = filterSIDs(out.DenyOnlyGroups, func(sid SID) bool {
		_, disable := toDisableSet[sid]
		return !disable
	})
	for _, sid := range toDeny {
		if !containsSID(out.DenyOnlyGroups, sid) {
			out.DenyOnlyGroups = append(out.DenyOnlyGroups, sid)
		}
	}
	out.DisabledGroups = filterSIDs(out.DisabledGroups, func(sid SID) bool {
		_, deny := toDenySet[sid]
		return !deny
	})
	for _, sid := range toDisable {
		if !containsSID(out.DisabledGroups, sid) {
			out.DisabledGroups = append(out.DisabledGroups, sid)
		}
	}

	if len(opts.AddPrivileges) != 0 {
		return Token{}, fmt.Errorf("%w: cannot add privileges", ErrPrivilegeEscalation)
	}

	originalPrivileges := makePrivilegeSet(out.EnabledPrivileges)
	basePrivileges := originalPrivileges
	if opts.Privileges != nil || opts.KeepPrivileges != nil {
		desired := opts.Privileges
		if desired == nil {
			desired = opts.KeepPrivileges
		}
		desired = dedupePrivileges(desired)
		for _, p := range desired {
			if _, ok := basePrivileges[p]; !ok {
				return Token{}, fmt.Errorf("%w: privilege %q is not enabled in the base token", ErrPrivilegeEscalation, p)
			}
		}
		out.EnabledPrivileges = desired
		basePrivileges = makePrivilegeSet(desired)
	}

	toRemove := makePrivilegeSet(dedupePrivileges(concatPrivileges(
		opts.RemovePrivileges,
		opts.RemovePrivilege,
		opts.Remove,
		opts.DeletePrivileges,
	)))
	for p := range toRemove {
		if _, ok := originalPrivileges[p]; !ok {
			return Token{}, fmt.Errorf("%w: privilege %q is not enabled in the base token", ErrInvalidDerivation, p)
		}
	}
	out.EnabledPrivileges = filterPrivileges(out.EnabledPrivileges, func(p Privilege) bool {
		_, remove := toRemove[p]
		return !remove
	})

	newIntegrity, setIntegrity, err := requestedIntegrity(opts)
	if err != nil {
		return Token{}, err
	}
	if !setIntegrity {
		newIntegrity = out.IntegrityLevel
	}
	if newIntegrity < IntegrityUntrusted {
		return Token{}, fmt.Errorf("%w: negative integrity level %d", ErrInvalidDerivation, int(newIntegrity))
	}
	if newIntegrity > out.IntegrityLevel {
		return Token{}, fmt.Errorf("%w: %s -> %s", ErrIntegrityEscalation, out.IntegrityLevel, newIntegrity)
	}
	out.IntegrityLevel = newIntegrity

	out.RestrictedSIDs = dedupeSIDs(concatSIDs(
		out.RestrictedSIDs,
		opts.RestrictedSIDs,
		opts.AddRestrictedSIDs,
		opts.Restricted,
		opts.RestrictedSidSet,
		opts.NewRestrictedSIDs,
	))

	mirrorAliases(&out, shape)
	return out, nil
}

func parseRestrictOptions(base Token, args []any) (RestrictOptions, error) {
	var opts RestrictOptions
	var sidSlices [][]SID
	var privilegeSlices [][]Privilege
	var integrityValues []IntegrityLevel

	merge := func(v RestrictOptions) {
		opts = mergeRestrictOptions(opts, v)
	}

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case RestrictOptions:
			merge(v)
		case *RestrictOptions:
			if v == nil {
				continue
			}
			merge(*v)
		case []SID:
			sidSlices = append(sidSlices, v)
		case SID:
			sidSlices = append(sidSlices, []SID{v})
		case []Privilege:
			privilegeSlices = append(privilegeSlices, v)
		case Privilege:
			privilegeSlices = append(privilegeSlices, []Privilege{v})
		case IntegrityLevel:
			integrityValues = append(integrityValues, v)
		case *IntegrityLevel:
			if v != nil {
				integrityValues = append(integrityValues, *v)
			}
		case int:
			integrityValues = append(integrityValues, IntegrityLevel(v))
		case int32:
			integrityValues = append(integrityValues, IntegrityLevel(v))
		case int64:
			integrityValues = append(integrityValues, IntegrityLevel(v))
		case uint:
			integrityValues = append(integrityValues, IntegrityLevel(v))
		case uint32:
			integrityValues = append(integrityValues, IntegrityLevel(v))
		case uint64:
			integrityValues = append(integrityValues, IntegrityLevel(v))
		default:
			// Accept caller-defined option structs with the same field names.
			if reflected, ok := reflectRestrictOptions(arg); ok {
				merge(reflected)
				continue
			}
			return RestrictOptions{}, fmt.Errorf("%w: unsupported option type %T", ErrInvalidDerivation, arg)
		}
	}

	known := makeSet(concatSIDs(base.EnabledGroups, base.DenyOnlyGroups, base.DisabledGroups, base.RestrictedSIDs))
	usedDisable, usedDenyOnly := false, false
	for _, sids := range sidSlices {
		if len(sids) == 0 {
			continue
		}
		containsNew := false
		for _, sid := range sids {
			if _, ok := known[sid]; !ok {
				containsNew = true
				break
			}
		}
		switch {
		case containsNew:
			opts.RestrictedSIDs = append(opts.RestrictedSIDs, sids...)
		case !usedDisable:
			opts.DisabledGroups = append(opts.DisabledGroups, sids...)
			usedDisable = true
		case !usedDenyOnly:
			opts.DenyOnlyGroups = append(opts.DenyOnlyGroups, sids...)
			usedDenyOnly = true
		default:
			opts.RestrictedSIDs = append(opts.RestrictedSIDs, sids...)
		}
	}

	for i, privileges := range privilegeSlices {
		if i == 0 {
			opts.RemovePrivileges = append(opts.RemovePrivileges, privileges...)
		} else {
			opts.AddPrivileges = append(opts.AddPrivileges, privileges...)
		}
	}
	if len(integrityValues) != 0 {
		level := integrityValues[len(integrityValues)-1]
		opts.Integrity = &level
		opts.SetIntegrity = true
	}
	return opts, nil
}

func requestedIntegrity(opts RestrictOptions) (IntegrityLevel, bool, error) {
	switch {
	case opts.Integrity != nil:
		return *opts.Integrity, true, nil
	case opts.NewIntegrity != nil:
		return *opts.NewIntegrity, true, nil
	case opts.LowerIntegrity != nil:
		return *opts.LowerIntegrity, true, nil
	case opts.SetIntegrity:
		return opts.IntegrityLevel, true, nil
	case opts.IntegrityLevel != 0:
		return opts.IntegrityLevel, true, nil
	default:
		return 0, false, nil
	}
}

func mergeRestrictOptions(dst, src RestrictOptions) RestrictOptions {
	dst.DenyOnlyGroups = append(dst.DenyOnlyGroups, src.DenyOnlyGroups...)
	dst.DenyOnly = append(dst.DenyOnly, src.DenyOnly...)
	dst.DenyOnlySIDs = append(dst.DenyOnlySIDs, src.DenyOnlySIDs...)
	dst.ToDenyOnly = append(dst.ToDenyOnly, src.ToDenyOnly...)
	dst.DisabledGroups = append(dst.DisabledGroups, src.DisabledGroups...)
	dst.DisableGroups = append(dst.DisableGroups, src.DisableGroups...)
	dst.Disable = append(dst.Disable, src.Disable...)
	dst.DisabledSIDs = append(dst.DisabledSIDs, src.DisabledSIDs...)
	dst.ToDisabled = append(dst.ToDisabled, src.ToDisabled...)
	dst.RemovePrivileges = append(dst.RemovePrivileges, src.RemovePrivileges...)
	dst.RemovePrivilege = append(dst.RemovePrivilege, src.RemovePrivilege...)
	dst.Remove = append(dst.Remove, src.Remove...)
	dst.DeletePrivileges = append(dst.DeletePrivileges, src.DeletePrivileges...)
	if src.Privileges != nil {
		dst.Privileges = append([]Privilege(nil), src.Privileges...)
	}
	if src.KeepPrivileges != nil {
		dst.KeepPrivileges = append([]Privilege(nil), src.KeepPrivileges...)
	}
	dst.AddPrivileges = append(dst.AddPrivileges, src.AddPrivileges...)
	if src.Integrity != nil {
		v := *src.Integrity
		dst.Integrity = &v
	}
	if src.NewIntegrity != nil {
		v := *src.NewIntegrity
		dst.NewIntegrity = &v
	}
	if src.LowerIntegrity != nil {
		v := *src.LowerIntegrity
		dst.LowerIntegrity = &v
	}
	if src.SetIntegrity {
		dst.SetIntegrity = true
		dst.IntegrityLevel = src.IntegrityLevel
	} else if src.IntegrityLevel != 0 {
		dst.IntegrityLevel = src.IntegrityLevel
	}
	dst.RestrictedSIDs = append(dst.RestrictedSIDs, src.RestrictedSIDs...)
	dst.AddRestrictedSIDs = append(dst.AddRestrictedSIDs, src.AddRestrictedSIDs...)
	dst.Restricted = append(dst.Restricted, src.Restricted...)
	dst.RestrictedSidSet = append(dst.RestrictedSidSet, src.RestrictedSidSet...)
	dst.NewRestrictedSIDs = append(dst.NewRestrictedSIDs, src.NewRestrictedSIDs...)
	dst.EnableGroups = append(dst.EnableGroups, src.EnableGroups...)
	dst.AddGroups = append(dst.AddGroups, src.AddGroups...)
	dst.NewGroups = append(dst.NewGroups, src.NewGroups...)
	if src.UserSID != nil {
		v := *src.UserSID
		dst.UserSID = &v
	}
	if src.User != nil {
		v := *src.User
		dst.User = &v
	}
	return dst
}

func reflectRestrictOptions(v any) (RestrictOptions, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return RestrictOptions{}, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return RestrictOptions{}, false
	}
	var out RestrictOptions
	recognized := false
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		value := rv.Field(i)
		switch field.Name {
		case "Disable", "DisableGroups", "DisabledGroups", "DisableSIDs", "DisabledSIDs", "ToDisabled":
			out.DisabledGroups = append(out.DisabledGroups, reflectSIDs(value)...)
			recognized = true
		case "DenyOnly", "DenyOnlyGroups", "DenyOnlySIDs", "ToDenyOnly":
			out.DenyOnlyGroups = append(out.DenyOnlyGroups, reflectSIDs(value)...)
			recognized = true
		case "Remove", "RemovePrivilege", "RemovePrivileges", "DeletePrivileges":
			out.RemovePrivileges = append(out.RemovePrivileges, reflectPrivileges(value)...)
			recognized = true
		case "AddPrivileges":
			out.AddPrivileges = append(out.AddPrivileges, reflectPrivileges(value)...)
			recognized = true
		case "Privileges":
			out.Privileges = reflectPrivileges(value)
			recognized = true
		case "Integrity", "NewIntegrity", "NewIntegrityLevel", "LowerIntegrity", "IntegrityLevel":
			if level, ok := reflectIntegrity(value); ok {
				out.Integrity = &level
				recognized = true
			}
		case "Restricted", "Restrict", "RestrictedSIDs", "RestrictedSidSet", "AddRestrictedSIDs", "NewRestrictedSIDs":
			out.RestrictedSIDs = append(out.RestrictedSIDs, reflectSIDs(value)...)
			recognized = true
		case "EnableGroups", "AddGroups", "NewGroups":
			out.EnableGroups = append(out.EnableGroups, reflectSIDs(value)...)
			recognized = true
		case "User", "UserSID":
			if sid, ok := reflectSID(value); ok {
				out.UserSID = &sid
				recognized = true
			}
		}
	}
	return out, recognized
}

func reflectSIDs(v reflect.Value) []SID {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		out := make([]SID, 0, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			if sid, ok := reflectSID(iter.Key()); ok {
				out = append(out, sid)
			}
		}
		sortSIDs(out)
		return out
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		if sid, ok := reflectSID(v); ok {
			return []SID{sid}
		}
		return nil
	}
	out := make([]SID, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		if sid, ok := reflectSID(v.Index(i)); ok {
			out = append(out, sid)
		}
	}
	return out
}

func sortSIDs(sids []SID) {
	for i := 1; i < len(sids); i++ {
		for j := i; j > 0 && sids[j] < sids[j-1]; j-- {
			sids[j], sids[j-1] = sids[j-1], sids[j]
		}
	}
}

func reflectSID(v reflect.Value) (SID, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.String {
		return SID(v.String()), true
	}
	return "", false
}

func reflectPrivileges(v reflect.Value) []Privilege {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		if v.Kind() == reflect.String {
			return []Privilege{Privilege(v.String())}
		}
		return nil
	}
	out := make([]Privilege, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		if elem.Kind() == reflect.String {
			out = append(out, Privilege(elem.String()))
		}
	}
	return out
}

func reflectIntegrity(v reflect.Value) (IntegrityLevel, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return IntegrityLevel(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return IntegrityLevel(v.Uint()), true
	}
	return 0, false
}

func cloneToken(t Token) Token {
	return Token{
		UserSID:           t.UserSID,
		EnabledGroups:     append([]SID(nil), t.EnabledGroups...),
		DenyOnlyGroups:    append([]SID(nil), t.DenyOnlyGroups...),
		DisabledGroups:    append([]SID(nil), t.DisabledGroups...),
		EnabledPrivileges: append([]Privilege(nil), t.EnabledPrivileges...),
		IntegrityLevel:    t.IntegrityLevel,
		RestrictedSIDs:    append([]SID(nil), t.RestrictedSIDs...),
		User:              t.User,
		UserSid:           t.UserSid,
		Groups:            append([]SID(nil), t.Groups...),
		DenyOnly:          append([]SID(nil), t.DenyOnly...),
		Disabled:          append([]SID(nil), t.Disabled...),
		Privileges:        append([]Privilege(nil), t.Privileges...),
		Integrity:         t.Integrity,
		RestrictedSIDSet:  append([]SID(nil), t.RestrictedSIDSet...),
		RestrictedSids:    append([]SID(nil), t.RestrictedSids...),
	}
}

type tokenAliasShape struct {
	user           bool
	userSid        bool
	groups         bool
	denyOnly       bool
	disabled       bool
	privileges     bool
	integrity      bool
	restricted     bool
	restrictedSids bool
}

func aliasShapeOf(t Token) tokenAliasShape {
	return tokenAliasShape{
		user:           t.User != "",
		userSid:        t.UserSid != "",
		groups:         t.Groups != nil,
		denyOnly:       t.DenyOnly != nil,
		disabled:       t.Disabled != nil,
		privileges:     t.Privileges != nil,
		integrity:      t.Integrity != 0,
		restricted:     t.RestrictedSIDSet != nil,
		restrictedSids: t.RestrictedSids != nil,
	}
}

func mirrorAliases(t *Token, shape tokenAliasShape) {
	if shape.user {
		t.User = t.UserSID
	} else {
		t.User = ""
	}
	if shape.userSid {
		t.UserSid = t.UserSID
	} else {
		t.UserSid = ""
	}
	if shape.groups {
		t.Groups = append([]SID(nil), t.EnabledGroups...)
	} else {
		t.Groups = nil
	}
	if shape.denyOnly {
		t.DenyOnly = append([]SID(nil), t.DenyOnlyGroups...)
	} else {
		t.DenyOnly = nil
	}
	if shape.disabled {
		t.Disabled = append([]SID(nil), t.DisabledGroups...)
	} else {
		t.Disabled = nil
	}
	if shape.privileges {
		t.Privileges = append([]Privilege(nil), t.EnabledPrivileges...)
	} else {
		t.Privileges = nil
	}
	if shape.integrity {
		t.Integrity = t.IntegrityLevel
	} else {
		t.Integrity = 0
	}
	if shape.restricted {
		t.RestrictedSIDSet = append([]SID(nil), t.RestrictedSIDs...)
	} else {
		t.RestrictedSIDSet = nil
	}
	if shape.restrictedSids {
		t.RestrictedSids = append([]SID(nil), t.RestrictedSIDs...)
	} else {
		t.RestrictedSids = nil
	}
}

func makeSet(sids []SID) map[SID]struct{} {
	out := make(map[SID]struct{}, len(sids))
	for _, sid := range sids {
		if sid == "" {
			continue
		}
		out[sid] = struct{}{}
	}
	return out
}

func makePrivilegeSet(privileges []Privilege) map[Privilege]struct{} {
	out := make(map[Privilege]struct{}, len(privileges))
	for _, p := range privileges {
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func dedupeSIDs(in []SID) []SID {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[SID]struct{}, len(in))
	out := make([]SID, 0, len(in))
	for _, sid := range in {
		if sid == "" {
			continue
		}
		if _, ok := seen[sid]; ok {
			continue
		}
		seen[sid] = struct{}{}
		out = append(out, sid)
	}
	return out
}

func dedupePrivileges(in []Privilege) []Privilege {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[Privilege]struct{}, len(in))
	out := make([]Privilege, 0, len(in))
	for _, p := range in {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func concatSIDs(groups ...[]SID) []SID {
	var out []SID
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func concatPrivileges(groups ...[]Privilege) []Privilege {
	var out []Privilege
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func containsPrivilege(privileges []Privilege, wanted Privilege) bool {
	for _, p := range privileges {
		if p == wanted {
			return true
		}
	}
	return false
}

func containsSID(sids []SID, wanted SID) bool {
	for _, sid := range sids {
		if sid == wanted {
			return true
		}
	}
	return false
}

func filterSIDs(in []SID, keep func(SID) bool) []SID {
	out := make([]SID, 0, len(in))
	for _, sid := range dedupeSIDs(in) {
		if keep(sid) {
			out = append(out, sid)
		}
	}
	return out
}

func filterPrivileges(in []Privilege, keep func(Privilege) bool) []Privilege {
	out := make([]Privilege, 0, len(in))
	for _, p := range dedupePrivileges(in) {
		if keep(p) {
			out = append(out, p)
		}
	}
	return out
}

func firstSID(values ...*SID) *SID {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
