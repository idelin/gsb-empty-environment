package netsec

import (
	"fmt"
	"reflect"
	"strings"
)

// AccessMask is the small permission domain used by this exercise.
type AccessMask uint32

// Access, AccessRights, and Permission are aliases for AccessMask.
type Access = AccessMask
type AccessRights = AccessMask
type Permission = AccessMask
type PermissionMask = AccessMask
type AceMask = AccessMask

const (
	AccessRead AccessMask = 1 << iota
	AccessWrite
	AccessExecute
	AccessChangeOwner
	AccessBackupRead
	AccessTakeOwnership
)

// Short and verbose aliases for the permission domain.
const (
	Read          = AccessRead
	Write         = AccessWrite
	Execute       = AccessExecute
	ChangeOwner   = AccessChangeOwner
	BackupRead    = AccessBackupRead
	TakeOwnership = AccessTakeOwnership

	PermissionRead          = AccessRead
	PermissionWrite         = AccessWrite
	PermissionExecute       = AccessExecute
	PermissionChangeOwner   = AccessChangeOwner
	PermissionBackupRead    = AccessBackupRead
	PermissionTakeOwnership = AccessTakeOwnership

	PermRead          = AccessRead
	PermWrite         = AccessWrite
	PermExecute       = AccessExecute
	PermChangeOwner   = AccessChangeOwner
	PermBackupRead    = AccessBackupRead
	PermTakeOwnership = AccessTakeOwnership

	AccessOwner      = AccessChangeOwner
	AccessWriteOwner = AccessChangeOwner
	AccessBackup     = AccessBackupRead
	AccessTakeOwner  = AccessTakeOwnership
	AccessOwnership  = AccessTakeOwnership
)

const (
	AccessAll = AccessRead | AccessWrite | AccessExecute | AccessChangeOwner | AccessBackupRead | AccessTakeOwnership
	AllAccess = AccessAll
	NoAccess  = AccessMask(0)
)

func (m AccessMask) String() string {
	if m == 0 {
		return "none"
	}
	names := []struct {
		bit  AccessMask
		name string
	}{
		{AccessRead, "Read"},
		{AccessWrite, "Write"},
		{AccessExecute, "Execute"},
		{AccessChangeOwner, "ChangeOwner"},
		{AccessBackupRead, "BackupRead"},
		{AccessTakeOwnership, "TakeOwnership"},
	}
	var parts []string
	remaining := m
	for _, item := range names {
		if m&item.bit != 0 {
			parts = append(parts, item.name)
			remaining &^= item.bit
		}
	}
	if remaining != 0 {
		parts = append(parts, fmt.Sprintf("0x%x", uint32(remaining)))
	}
	return strings.Join(parts, "|")
}

// Effect distinguishes Allow and Deny ACEs. EffectAllow is the zero value so
// that a plain ACE literal with a SID and mask behaves as an Allow ACE.
type Effect uint8

const (
	EffectAllow Effect = iota
	EffectDeny

	Allow       = EffectAllow
	Deny        = EffectDeny
	AceAllow    = EffectAllow
	AceDeny     = EffectDeny
	AllowEffect = EffectAllow
	DenyEffect  = EffectDeny
)

type AceType = Effect
type ACEType = Effect
type ACEKind = Effect

func (e Effect) String() string {
	if e == EffectDeny {
		return "Deny"
	}
	return "Allow"
}

// ACE is one ordered access-control entry.
//
// The canonical form is {Type: AceAllow|AceDeny, SID: sid, Mask: mask}. The
// additional fields make the common Allow/Deny and Access/Permissions spellings
// interchangeable without changing the check:
//
//	ACE{Allow: true, SID: s, Mask: Read}
//	ACE{Deny: true, SID: s, Access: Write}
type ACE struct {
	Type    Effect
	Effect  Effect
	Kind    Effect
	AceType Effect

	Allow   bool
	Allowed bool
	Deny    bool
	Denied  bool
	IsAllow bool
	IsDeny  bool

	SID SID
	Sid SID

	Mask        AccessMask
	Access      AccessMask
	Permissions AccessMask
	Rights      AccessMask
}

// Ace is an alias for ACE.
type Ace = ACE

func (a ACE) effect() Effect {
	// A Deny marker wins over an Allow marker. Otherwise the first non-zero
	// enum spelling is used; the zero value means Allow.
	if a.Deny || a.Denied || a.IsDeny {
		return EffectDeny
	}
	if a.Allow || a.Allowed || a.IsAllow {
		return EffectAllow
	}
	if a.Effect != EffectAllow {
		return a.Effect
	}
	if a.Kind != EffectAllow {
		return a.Kind
	}
	if a.Type != EffectAllow {
		return a.Type
	}
	if a.AceType != EffectAllow {
		return a.AceType
	}
	return EffectAllow
}

func (a ACE) mask() AccessMask {
	return a.Mask | a.Access | a.Permissions | a.Rights
}

func (a ACE) sidValue() SID {
	if a.SID != "" {
		return a.SID
	}
	return a.Sid
}

// SecurityDescriptor is an object's ordered discretionary ACL plus its
// integrity label.
type SecurityDescriptor struct {
	ACL     []ACE
	DACL    []ACE
	Aces    []ACE
	Entries []ACE

	IntegrityLevel       IntegrityLevel
	ObjectIntegrity      IntegrityLevel
	ObjectIntegrityLevel IntegrityLevel
	Integrity            IntegrityLevel
	IntegrityLabel       IntegrityLevel
	Label                IntegrityLevel

	IntegrityPtr *IntegrityLevel
}

func (sd SecurityDescriptor) effectiveACL() []ACE {
	for _, acl := range [][]ACE{sd.ACL, sd.DACL, sd.Aces, sd.Entries} {
		if len(acl) != 0 {
			return acl
		}
	}
	return nil
}

func (sd SecurityDescriptor) effectiveIntegrity() IntegrityLevel {
	level := sd.IntegrityLevel
	for _, candidate := range []IntegrityLevel{
		sd.ObjectIntegrity,
		sd.ObjectIntegrityLevel,
		sd.Integrity,
		sd.IntegrityLabel,
		sd.Label,
	} {
		if candidate > level {
			level = candidate
		}
	}
	if sd.IntegrityPtr != nil && *sd.IntegrityPtr > level {
		level = *sd.IntegrityPtr
	}
	return level
}

// CheckPhase identifies which of the two access checks produced evidence.
type CheckPhase string

const (
	PhasePrimary    CheckPhase = "primary"
	PhaseRestricted CheckPhase = "restricted"
)

// MatchKind identifies why an ACE's SID matched the token.
type MatchKind string

const (
	MatchNone          MatchKind = ""
	MatchUser          MatchKind = "user"
	MatchEnabledGroup  MatchKind = "enabled-group"
	MatchDenyOnlyGroup MatchKind = "deny-only-group"
	MatchRestrictedSID MatchKind = "restricted-sid"

	MatchedUser          = MatchUser
	MatchedEnabledGroup  = MatchEnabledGroup
	MatchedDenyOnlyGroup = MatchDenyOnlyGroup
	MatchedRestrictedSID = MatchRestrictedSID
)

// DenyReason is a machine-readable explanation for a failed check.
type DenyReason string

const (
	ReasonNone           DenyReason = ""
	ReasonExplicitDeny   DenyReason = "explicit-deny"
	ReasonIncomplete     DenyReason = "incomplete-grant"
	ReasonRestrictedDeny DenyReason = "restricted-sid-check"
	ReasonIntegrity      DenyReason = "integrity-label"
	ReasonInvalid        DenyReason = "invalid-request"
)

// ACEEvidence records the result of considering one ACE. Evidence is emitted
// for every ACE reached, including ACEs that did not match.
type ACEEvidence struct {
	Index int
	Phase CheckPhase

	ACE ACE
	Ace ACE

	SID     SID
	Mask    AccessMask
	Effect  Effect
	IsDeny  bool
	IsAllow bool

	Matched    bool
	Match      bool
	MatchKind  MatchKind
	MatchedSID SID

	Applied     bool
	AppliedMask AccessMask
	Granted     AccessMask
	GrantedMask AccessMask
	Cumulative  AccessMask
	Remaining   AccessMask
	Requested   AccessMask
	Reason      DenyReason
	Note        string
}

// PrivilegeEvidence records whether an enabled privilege supplied one of the
// two explicitly privileged bits.
type PrivilegeEvidence struct {
	Phase     CheckPhase
	Privilege Privilege
	Access    AccessMask
	Applied   bool
}

// CheckResult is the result of one DACL pass.
type CheckResult struct {
	Phase CheckPhase

	Allowed bool
	Passed  bool

	Requested   AccessMask
	Granted     AccessMask
	GrantedMask AccessMask
	Remaining   AccessMask
	Evidence    []ACEEvidence

	DeniedBy  int
	DenyIndex int
	DenySID   SID
	Reason    DenyReason

	PrivilegeGrant    AccessMask
	PrivilegeEvidence []PrivilegeEvidence
}

// Decision is the combined result of the primary and (when applicable)
// restricted-SID access checks plus the object's integrity constraint.
type Decision struct {
	Allowed bool
	Passed  bool

	Requested AccessMask
	Granted   AccessMask
	Denied    AccessMask

	RequestedAccess AccessMask
	GrantedAccess   AccessMask
	RequestedMask   AccessMask
	GrantedMask     AccessMask
	DeniedMask      AccessMask

	PrimaryGranted    AccessMask
	RestrictedGranted AccessMask

	Reason  DenyReason
	Message string
	Error   error

	// Evidence is the flat, ordered evidence stream. PrimaryEvidence and
	// RestrictedEvidence expose the same records grouped by check.
	Evidence           []ACEEvidence
	PrimaryEvidence    []ACEEvidence
	RestrictedEvidence []ACEEvidence

	Primary    CheckResult
	Restricted *CheckResult

	Integrity           IntegrityLevel
	TokenIntegrity      IntegrityLevel
	IntegrityBlocked    AccessMask
	IntegrityRestricted AccessMask
}

// IsAllowed reports whether the request was granted.
func (d Decision) IsAllowed() bool { return d.Allowed }

// OK is an alias for IsAllowed.
func (d Decision) OK() bool { return d.Allowed }

// Result and AccessResult are aliases for Decision.
type Result = Decision
type AccessResult = Decision
type AccessDecision = Decision
type Evidence = ACEEvidence
type ACEInfo = ACEEvidence
type AccessEvidence = ACEEvidence

// CheckAccess performs the Windows-style access check. It accepts a token, a
// security descriptor (or an ACE slice), and the requested mask in any order.
// Additional IntegrityLevel arguments override the descriptor's label.
func CheckAccess(args ...any) Decision {
	token, sd, requested, err := parseAccessArgs(args)
	if err != nil {
		return Decision{
			Allowed: false,
			Passed:  false,
			Reason:  ReasonInvalid,
			Message: err.Error(),
			Error:   err,
		}
	}
	return checkAccess(token, sd, requested)
}

// AccessCheck is an alias for CheckAccess.
func AccessCheck(args ...any) Decision {
	return CheckAccess(args...)
}

// EvaluateAccess is an alias for CheckAccess.
func EvaluateAccess(args ...any) Decision {
	return CheckAccess(args...)
}

// Check is an alias for CheckAccess.
func Check(args ...any) Decision {
	return CheckAccess(args...)
}

// CheckAllowed reports only whether the request is allowed.
func CheckAllowed(args ...any) bool {
	return CheckAccess(args...).Allowed
}

// Allowed reports only whether the request is allowed.
func Allowed(args ...any) bool {
	return CheckAccess(args...).Allowed
}

// CheckAccess performs the same check as the package-level function.
func (t Token) CheckAccess(args ...any) Decision {
	return CheckAccess(append([]any{t}, args...)...)
}

// AccessCheck performs the same check as the package-level function.
func (t Token) AccessCheck(args ...any) Decision {
	return t.CheckAccess(args...)
}

// CheckPermissions is an alias for Token.CheckAccess.
func (t Token) CheckPermissions(args ...any) Decision {
	return t.CheckAccess(args...)
}

// CheckAccess performs the same check as the package-level function.
func (sd SecurityDescriptor) CheckAccess(args ...any) Decision {
	return CheckAccess(append([]any{sd}, args...)...)
}

// AccessCheck performs the same check as the package-level function.
func (sd SecurityDescriptor) AccessCheck(args ...any) Decision {
	return sd.CheckAccess(args...)
}

func checkAccess(token Token, sd SecurityDescriptor, requested AccessMask) Decision {
	token = normalizeToken(token)
	acl := append([]ACE(nil), sd.effectiveACL()...)
	objectIntegrity := sd.effectiveIntegrity()

	user := token.UserSID
	enabled := makeSet(token.EffectiveGroups())
	denyOnly := makeSet(token.DenyOnlyGroups)
	for sid := range enabled {
		delete(denyOnly, sid)
	}
	for _, sid := range token.DisabledGroups {
		delete(denyOnly, sid)
	}

	allowPrimary := makeSet(nil)
	denyPrimary := makeSet(nil)
	if user != "" {
		allowPrimary[user] = struct{}{}
		denyPrimary[user] = struct{}{}
	}
	for sid := range enabled {
		allowPrimary[sid] = struct{}{}
		denyPrimary[sid] = struct{}{}
	}
	for sid := range denyOnly {
		denyPrimary[sid] = struct{}{}
	}

	primary := runDACLCheck(acl, requested, PhasePrimary, allowPrimary, denyPrimary, denyOnly, user, token.EnabledPrivileges)

	var restricted *CheckResult
	restrictedIDs := dedupeSIDs(token.RestrictedSIDs)
	if len(restrictedIDs) != 0 {
		allowRestricted := makeSet(restrictedIDs)
		denyRestricted := makeSet(restrictedIDs)
		for _, sid := range token.DisabledGroups {
			delete(allowRestricted, sid)
			delete(denyRestricted, sid)
		}
		// Deny-only groups remain deny-only in the second check. They are
		// never added to the Allow set.
		for sid := range denyOnly {
			delete(allowRestricted, sid)
			denyRestricted[sid] = struct{}{}
		}
		result := runDACLCheck(acl, requested, PhaseRestricted, allowRestricted, denyRestricted, denyOnly, "", token.EnabledPrivileges)
		restricted = &result
	}

	granted := primary.Granted
	if restricted != nil {
		granted &= restricted.Granted
	}

	var integrityBlocked AccessMask
	if objectIntegrity > token.IntegrityLevel {
		integrityBlocked = AccessWrite | AccessChangeOwner
	}
	granted &^= integrityBlocked

	allowed := primary.Allowed
	reason := primary.Reason
	message := primaryMessage(primary)
	if restricted != nil {
		allowed = allowed && restricted.Allowed
		if allowed {
			reason = ReasonNone
			message = "requested access granted by both checks"
		} else if reason != ReasonExplicitDeny {
			if restricted.Reason == ReasonExplicitDeny {
				reason = ReasonExplicitDeny
			} else if reason == ReasonNone {
				reason = ReasonRestrictedDeny
			}
			message = restrictedMessage(*restricted)
		}
	}
	if integrityBlocked&requested != 0 {
		allowed = false
		reason = ReasonIntegrity
		message = fmt.Sprintf("object integrity %s exceeds token integrity %s; Write and ChangeOwner are blocked",
			objectIntegrity, token.IntegrityLevel)
	}

	decision := Decision{
		Allowed:             allowed,
		Passed:              allowed,
		Requested:           requested,
		Granted:             granted & requested,
		Denied:              requested &^ granted,
		RequestedAccess:     requested,
		GrantedAccess:       granted & requested,
		RequestedMask:       requested,
		GrantedMask:         granted & requested,
		DeniedMask:          requested &^ granted,
		Reason:              reason,
		Message:             message,
		Primary:             primary,
		Restricted:          restricted,
		PrimaryEvidence:     primary.Evidence,
		Integrity:           objectIntegrity,
		TokenIntegrity:      token.IntegrityLevel,
		IntegrityBlocked:    integrityBlocked & requested,
		IntegrityRestricted: integrityBlocked & requested,
	}
	decision.PrimaryGranted = primary.Granted
	if restricted != nil {
		decision.RestrictedGranted = restricted.Granted
	}
	decision.Evidence = append(decision.Evidence, primary.Evidence...)
	if restricted != nil {
		decision.RestrictedEvidence = restricted.Evidence
		decision.Evidence = append(decision.Evidence, restricted.Evidence...)
	}
	if allowed {
		decision.Reason = ReasonNone
	}
	return decision
}

func runDACLCheck(
	acl []ACE,
	requested AccessMask,
	phase CheckPhase,
	allowIDs map[SID]struct{},
	denyIDs map[SID]struct{},
	denyOnlyIDs map[SID]struct{},
	user SID,
	privileges []Privilege,
) CheckResult {
	result := CheckResult{
		Phase:     phase,
		Requested: requested,
		DeniedBy:  -1,
		DenyIndex: -1,
	}
	var granted AccessMask

	for index, ace := range acl {
		effect := ace.effect()
		aceMask := ace.mask()
		relevant := aceMask & requested

		sid := ace.sidValue()
		matchKind, matchedSID := matchACE(sid, effect, allowIDs, denyIDs, denyOnlyIDs, user, phase)
		evidence := ACEEvidence{
			Index:      index,
			Phase:      phase,
			ACE:        ace,
			Ace:        ace,
			SID:        sid,
			Mask:       aceMask,
			Effect:     effect,
			IsDeny:     effect == EffectDeny,
			IsAllow:    effect == EffectAllow,
			Matched:    matchKind != MatchNone,
			Match:      matchKind != MatchNone,
			MatchKind:  matchKind,
			MatchedSID: matchedSID,
			Requested:  requested,
		}

		if evidence.Matched && relevant != 0 {
			evidence.AppliedMask = relevant
			evidence.Applied = true
			if effect == EffectAllow {
				granted |= relevant
				evidence.Note = "allow ACE added requested bits"
			} else {
				evidence.Note = "matching deny ACE rejects the request immediately"
				evidence.Reason = ReasonExplicitDeny
				evidence.Granted = granted
				evidence.GrantedMask = granted
				evidence.Cumulative = granted
				evidence.Remaining = requested &^ granted
				result.Evidence = append(result.Evidence, evidence)
				result.Granted = granted
				result.GrantedMask = granted
				result.Remaining = requested &^ granted
				result.Allowed = false
				result.Passed = false
				result.DeniedBy = index
				result.DenyIndex = index
				result.DenySID = sid
				result.Reason = ReasonExplicitDeny
				return result
			}
		} else if evidence.Matched {
			evidence.Note = "SID matched but the ACE has no requested permission bits"
		} else {
			evidence.Note = "SID did not match this check"
		}

		evidence.Granted = granted
		evidence.GrantedMask = granted
		evidence.Cumulative = granted
		evidence.Remaining = requested &^ granted
		result.Evidence = append(result.Evidence, evidence)
	}

	// Privileges supplement the DACL only for the two bits named by the
	// exercise, and only when they are enabled in the token.
	for _, privilege := range dedupePrivileges(privileges) {
		bit := privilegeAccess(privilege)
		if bit == 0 || requested&bit == 0 || granted&bit != 0 {
			continue
		}
		granted |= bit
		result.PrivilegeGrant |= bit
		result.PrivilegeEvidence = append(result.PrivilegeEvidence, PrivilegeEvidence{
			Phase:     phase,
			Privilege: privilege,
			Access:    bit,
			Applied:   true,
		})
	}

	result.Granted = granted
	result.GrantedMask = granted
	result.Remaining = requested &^ granted
	result.Allowed = result.Remaining == 0
	result.Passed = result.Allowed
	if !result.Allowed {
		result.Reason = ReasonIncomplete
	}
	return result
}

func matchACE(
	sid SID,
	effect Effect,
	allowIDs map[SID]struct{},
	denyIDs map[SID]struct{},
	denyOnlyIDs map[SID]struct{},
	user SID,
	phase CheckPhase,
) (MatchKind, SID) {
	if sid == "" {
		return MatchNone, ""
	}
	if effect == EffectDeny {
		if _, ok := denyIDs[sid]; ok {
			if _, denyOnly := denyOnlyIDs[sid]; denyOnly {
				return MatchDenyOnlyGroup, sid
			}
			return matchKindForSID(sid, user, phase), sid
		}
		return MatchNone, ""
	}
	if _, ok := allowIDs[sid]; ok {
		return matchKindForSID(sid, user, phase), sid
	}
	return MatchNone, ""
}

func matchKindForSID(sid SID, user SID, phase CheckPhase) MatchKind {
	if phase == PhaseRestricted {
		return MatchRestrictedSID
	}
	if sid == user {
		return MatchUser
	}
	if sid != "" {
		return MatchEnabledGroup
	}
	return MatchNone
}

func privilegeAccess(p Privilege) AccessMask {
	switch p {
	case SeBackupPrivilege:
		return AccessBackupRead
	case SeTakeOwnershipPrivilege:
		return AccessTakeOwnership
	default:
		return 0
	}
}

func normalizeToken(t Token) Token {
	out := cloneToken(t)
	if out.UserSID == "" {
		if out.User != "" {
			out.UserSID = out.User
		} else {
			out.UserSID = out.UserSid
		}
	}
	if len(out.EnabledGroups) == 0 && len(out.Groups) != 0 {
		out.EnabledGroups = out.Groups
	}
	if len(out.DenyOnlyGroups) == 0 && len(out.DenyOnly) != 0 {
		out.DenyOnlyGroups = out.DenyOnly
	}
	if len(out.DisabledGroups) == 0 && len(out.Disabled) != 0 {
		out.DisabledGroups = out.Disabled
	}
	if len(out.EnabledPrivileges) == 0 && len(out.Privileges) != 0 {
		out.EnabledPrivileges = out.Privileges
	}
	if out.IntegrityLevel == 0 && out.Integrity != 0 {
		out.IntegrityLevel = out.Integrity
	}
	if len(out.RestrictedSIDs) == 0 && len(out.RestrictedSIDSet) != 0 {
		out.RestrictedSIDs = out.RestrictedSIDSet
	}
	if len(out.RestrictedSIDs) == 0 && len(out.RestrictedSids) != 0 {
		out.RestrictedSIDs = out.RestrictedSids
	}
	out.UserSID = SID(strings.TrimSpace(string(out.UserSID)))
	out.EnabledGroups = dedupeSIDs(out.EnabledGroups)
	out.DenyOnlyGroups = dedupeSIDs(out.DenyOnlyGroups)
	out.DisabledGroups = dedupeSIDs(out.DisabledGroups)
	out.EnabledPrivileges = dedupePrivileges(out.EnabledPrivileges)
	out.RestrictedSIDs = dedupeSIDs(out.RestrictedSIDs)
	return out
}

func parseAccessArgs(args []any) (Token, SecurityDescriptor, AccessMask, error) {
	var (
		token     Token
		haveToken bool
		sd        SecurityDescriptor
		haveSD    bool
		acl       []ACE
		haveACL   bool
		requested AccessMask
		haveReq   bool
		integrity *IntegrityLevel
	)

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case Token:
			token, haveToken = v, true
		case *Token:
			if v != nil {
				token, haveToken = *v, true
			}
		case SecurityDescriptor:
			sd, haveSD = v, true
		case *SecurityDescriptor:
			if v != nil {
				sd, haveSD = *v, true
			}
		case []ACE:
			acl, haveACL = v, true
		case ACE:
			acl, haveACL = []ACE{v}, true
		case AccessMask:
			if !haveReq {
				requested, haveReq = v, true
			} else {
				requested |= v
			}
		case IntegrityLevel:
			level := v
			integrity = &level
		case *IntegrityLevel:
			if v != nil {
				level := *v
				integrity = &level
			}
		case uint8:
			requested, haveReq = AccessMask(v), true
		case uint16:
			requested, haveReq = AccessMask(v), true
		case uint32:
			requested, haveReq = AccessMask(v), true
		case uint64:
			requested, haveReq = AccessMask(v), true
		case int:
			requested, haveReq = AccessMask(v), true
		case int32:
			requested, haveReq = AccessMask(v), true
		case int64:
			requested, haveReq = AccessMask(v), true
		default:
			if ref, ok := reflectAccessArg(arg); ok {
				if ref.token != nil && !haveToken {
					token, haveToken = *ref.token, true
				}
				if ref.sd != nil && !haveSD {
					sd, haveSD = *ref.sd, true
				}
				if ref.acl != nil && !haveACL {
					acl, haveACL = ref.acl, true
				}
				if ref.requested != nil && !haveReq {
					requested, haveReq = *ref.requested, true
				}
				if ref.integrity != nil {
					integrity = ref.integrity
				}
				continue
			}
			return Token{}, SecurityDescriptor{}, 0, fmt.Errorf("netsec: unsupported access-check argument %T", arg)
		}
	}

	if !haveToken {
		return Token{}, SecurityDescriptor{}, 0, fmt.Errorf("netsec: access check requires a Token")
	}
	if haveACL {
		if haveSD && sd.effectiveACL() != nil {
			return Token{}, SecurityDescriptor{}, 0, fmt.Errorf("netsec: provide either an ACL or a SecurityDescriptor, not both")
		}
		sd.ACL = acl
		haveSD = true
	}
	if !haveSD {
		return Token{}, SecurityDescriptor{}, 0, fmt.Errorf("netsec: access check requires a SecurityDescriptor or ACL")
	}
	if integrity != nil {
		sd.IntegrityPtr = integrity
	}
	return token, sd, requested, nil
}

type reflectedAccessArg struct {
	token     *Token
	sd        *SecurityDescriptor
	acl       []ACE
	requested *AccessMask
	integrity *IntegrityLevel
}

func reflectAccessArg(v any) (reflectedAccessArg, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return reflectedAccessArg{}, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflectedAccessArg{}, false
	}
	var out reflectedAccessArg
	recognized := false
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		value := rv.Field(i)
		switch field.Name {
		case "Token":
			if value.CanConvert(reflect.TypeOf(Token{})) {
				t := value.Convert(reflect.TypeOf(Token{})).Interface().(Token)
				out.token = &t
				recognized = true
			}
		case "SecurityDescriptor", "Descriptor":
			if value.CanConvert(reflect.TypeOf(SecurityDescriptor{})) {
				s := value.Convert(reflect.TypeOf(SecurityDescriptor{})).Interface().(SecurityDescriptor)
				out.sd = &s
				recognized = true
			}
		case "ACL", "DACL", "Aces":
			if value.CanConvert(reflect.TypeOf([]ACE{})) {
				out.acl = value.Convert(reflect.TypeOf([]ACE{})).Interface().([]ACE)
				recognized = true
			}
		case "Requested", "RequestedAccess", "RequestedMask", "DesiredAccess", "Mask", "Access":
			if value.CanConvert(reflect.TypeOf(AccessMask(0))) {
				m := value.Convert(reflect.TypeOf(AccessMask(0))).Interface().(AccessMask)
				out.requested = &m
				recognized = true
			}
		case "Integrity", "IntegrityLevel":
			if value.CanConvert(reflect.TypeOf(IntegrityLevel(0))) {
				l := value.Convert(reflect.TypeOf(IntegrityLevel(0))).Interface().(IntegrityLevel)
				out.integrity = &l
				recognized = true
			}
		}
	}
	return out, recognized
}

func primaryMessage(result CheckResult) string {
	if result.Allowed {
		return "primary access check granted the request"
	}
	if result.Reason == ReasonExplicitDeny {
		return fmt.Sprintf("primary access check denied by ACE %d for SID %q", result.DeniedBy, result.DenySID)
	}
	return fmt.Sprintf("primary access check did not grant %s", (result.Requested &^ result.Granted))
}

func restrictedMessage(result CheckResult) string {
	if result.Reason == ReasonExplicitDeny {
		return fmt.Sprintf("restricted-SID check denied by ACE %d for SID %q", result.DeniedBy, result.DenySID)
	}
	return fmt.Sprintf("restricted-SID check did not grant %s", (result.Requested &^ result.Granted))
}
