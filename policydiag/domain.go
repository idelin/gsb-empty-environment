package policydiag

import (
	"fmt"
	"sort"
)

// RuleKind enumerates the three rule types.
type RuleKind string

const (
	// KindAllow grants a request when the rule matches.
	KindAllow RuleKind = "allow"
	// KindDeny forbids a request when the rule matches. Deny wins over allow.
	KindDeny RuleKind = "deny"
	// KindDual marks a matched allow as requiring two distinct-person approvers.
	KindDual RuleKind = "require_dual"
)

// RequirementKind enumerates the business assertions the policy must satisfy.
type RequirementKind string

const (
	// ReqAvailability asks that the named subject can perform an operation on
	// the named file, including any mandated dual approval.
	ReqAvailability RequirementKind = "availability"
	// ReqNoUnauthorized asks that no subject lacking the needed clearance can
	// ever be granted the named operation (all operations when empty).
	ReqNoUnauthorized RequirementKind = "no_unauthorized"
)

// Subject is one public-key identity. Two subjects with the same PersonID are
// aliases of the same human and therefore never count as two distinct people.
type Subject struct {
	// KeyID is the unique public-key identity, e.g. "key-alice-1".
	KeyID string
	// PersonID identifies the human controlling the key.
	PersonID string
	// Roles are the subject's role attributes (finite domain).
	Roles []string
	// Clearance is the subject's clearance level.
	Clearance string
}

// File is a classified file.
type File struct {
	// FileID is the unique file identifier.
	FileID string
	// Classification is the file's classification level.
	Classification string
}

// Rule is a single policy rule. Every slice field is a conjunction of
// "equals one of" filters; an empty slice is a wildcard that matches every
// value of that dimension.
type Rule struct {
	// ID is the unique rule identifier used in conflict reports.
	ID string
	// Kind is allow, deny or require_dual.
	Kind RuleKind
	// Operations restricts the rule to these operations. Empty matches all.
	Operations []string
	// SubjectKeys restricts the rule to these key identities. Empty matches all.
	SubjectKeys []string
	// SubjectRoles restricts the rule to subjects holding one of these roles.
	// Empty matches all.
	SubjectRoles []string
	// MinClearance restricts the rule to subjects whose clearance is at least
	// this level on the policy's clearance order. Empty matches all.
	MinClearance string
	// FileMinLevel restricts the rule to files classified at least at this
	// level. It is a separate dimension from subject MinClearance.
	FileMinLevel string
	// FileIDs restricts the rule to these files. Empty matches all.
	FileIDs []string
	// FileLevels restricts the rule to files with one of these classifications.
	// Empty matches all.
	FileLevels []string
}

// Requirement is a business assertion that the published policy must meet.
type Requirement struct {
	// ID is the unique requirement identifier.
	ID string
	// Kind is availability or no_unauthorized.
	Kind RequirementKind
	// Operations narrows the assertion. For availability it must contain the
	// single operation that must work. For no_unauthorized an empty list means
	// every declared operation.
	Operations []string
	// SubjectKey is the subject of an availability requirement.
	SubjectKey string
	// FileID is the file of an availability requirement.
	FileID string
}

// Policy is the finite universe plus the rules and business requirements.
type Policy struct {
	// Operations is the finite operation domain, e.g. ["decrypt", "export"].
	Operations []string
	// ClearanceOrder lists clearance levels from lowest to highest.
	ClearanceOrder []string
	// Subjects is the finite subject (key identity) domain.
	Subjects []Subject
	// Files is the finite file domain.
	Files []File
	// Rules holds at most 18 rules.
	Rules []Rule
	// Requirements are the availability and no-unauthorized assertions.
	Requirements []Requirement
}

// indexed lookups built once and reused by every enumeration.
type universe struct {
	opIndex    map[string]int
	levelRank  map[string]int
	keyIndex   map[string]int
	fileIndex  map[string]int
	personOf   map[string]string
	clearance  map[string]string
	roles      map[string][]string
	subjectIDs []string
	fileIDs    []string
}

func buildUniverse(p *Policy) (*universe, error) {
	u := &universe{
		opIndex:   map[string]int{},
		levelRank: map[string]int{},
		keyIndex:  map[string]int{},
		fileIndex: map[string]int{},
		personOf:  map[string]string{},
		clearance: map[string]string{},
		roles:     map[string][]string{},
	}
	if len(p.Operations) == 0 {
		return nil, fmt.Errorf("policydiag: policy must declare at least one operation")
	}
	for i, op := range p.Operations {
		if op == "" {
			return nil, fmt.Errorf("policydiag: operation %d has empty id", i)
		}
		if _, dup := u.opIndex[op]; dup {
			return nil, fmt.Errorf("policydiag: duplicate operation %q", op)
		}
		u.opIndex[op] = i
	}
	if len(p.ClearanceOrder) == 0 {
		return nil, fmt.Errorf("policydiag: policy must declare a clearance order")
	}
	for i, level := range p.ClearanceOrder {
		if level == "" {
			return nil, fmt.Errorf("policydiag: clearance level %d has empty id", i)
		}
		if _, dup := u.levelRank[level]; dup {
			return nil, fmt.Errorf("policydiag: duplicate clearance level %q", level)
		}
		u.levelRank[level] = i
	}
	for i, s := range p.Subjects {
		if s.KeyID == "" {
			return nil, fmt.Errorf("policydiag: subject %d has empty key id", i)
		}
		if _, dup := u.keyIndex[s.KeyID]; dup {
			return nil, fmt.Errorf("policydiag: duplicate subject key %q", s.KeyID)
		}
		if s.PersonID == "" {
			return nil, fmt.Errorf("policydiag: subject %q has empty person id", s.KeyID)
		}
		if _, ok := u.levelRank[s.Clearance]; !ok {
			return nil, fmt.Errorf("policydiag: subject %q has unknown clearance %q", s.KeyID, s.Clearance)
		}
		u.keyIndex[s.KeyID] = i
		u.personOf[s.KeyID] = s.PersonID
		u.clearance[s.KeyID] = s.Clearance
		u.roles[s.KeyID] = append([]string(nil), s.Roles...)
		u.subjectIDs = append(u.subjectIDs, s.KeyID)
	}
	for i, f := range p.Files {
		if f.FileID == "" {
			return nil, fmt.Errorf("policydiag: file %d has empty file id", i)
		}
		if _, dup := u.fileIndex[f.FileID]; dup {
			return nil, fmt.Errorf("policydiag: duplicate file id %q", f.FileID)
		}
		if _, ok := u.levelRank[f.Classification]; !ok {
			return nil, fmt.Errorf("policydiag: file %q has unknown classification %q", f.FileID, f.Classification)
		}
		u.fileIndex[f.FileID] = i
		u.fileIDs = append(u.fileIDs, f.FileID)
	}
	sort.Strings(u.subjectIDs)
	sort.Strings(u.fileIDs)
	return u, nil
}

// validate checks identifier references, rule kinds and the 18-rule cap.
func (p *Policy) validate(u *universe) error {
	if len(p.Rules) > 18 {
		return fmt.Errorf("policydiag: %d rules exceed the 18-rule limit", len(p.Rules))
	}
	seenRule := map[string]bool{}
	for i, r := range p.Rules {
		if r.ID == "" {
			return fmt.Errorf("policydiag: rule %d has empty id", i)
		}
		if seenRule[r.ID] {
			return fmt.Errorf("policydiag: duplicate rule id %q", r.ID)
		}
		seenRule[r.ID] = true
		switch r.Kind {
		case KindAllow, KindDeny, KindDual:
		default:
			return fmt.Errorf("policydiag: rule %q has unknown kind %q", r.ID, r.Kind)
		}
		for _, op := range r.Operations {
			if _, ok := u.opIndex[op]; !ok {
				return fmt.Errorf("policydiag: rule %q references unknown operation %q", r.ID, op)
			}
		}
		for _, key := range r.SubjectKeys {
			if _, ok := u.keyIndex[key]; !ok {
				return fmt.Errorf("policydiag: rule %q references unknown subject %q", r.ID, key)
			}
		}
		for _, fID := range r.FileIDs {
			if _, ok := u.fileIndex[fID]; !ok {
				return fmt.Errorf("policydiag: rule %q references unknown file %q", r.ID, fID)
			}
		}
		if r.MinClearance != "" {
			if _, ok := u.levelRank[r.MinClearance]; !ok {
				return fmt.Errorf("policydiag: rule %q references unknown clearance %q", r.ID, r.MinClearance)
			}
		}
		if r.FileMinLevel != "" {
			if _, ok := u.levelRank[r.FileMinLevel]; !ok {
				return fmt.Errorf("policydiag: rule %q references unknown minimum file level %q", r.ID, r.FileMinLevel)
			}
		}
		for _, level := range r.FileLevels {
			if _, ok := u.levelRank[level]; !ok {
				return fmt.Errorf("policydiag: rule %q references unknown file level %q", r.ID, level)
			}
		}
	}
	seenReq := map[string]bool{}
	for i, req := range p.Requirements {
		if req.ID == "" {
			return fmt.Errorf("policydiag: requirement %d has empty id", i)
		}
		if seenReq[req.ID] {
			return fmt.Errorf("policydiag: duplicate requirement id %q", req.ID)
		}
		seenReq[req.ID] = true
		switch req.Kind {
		case ReqAvailability:
			if len(req.Operations) != 1 {
				return fmt.Errorf("policydiag: availability requirement %q must name exactly one operation", req.ID)
			}
			if _, ok := u.opIndex[req.Operations[0]]; !ok {
				return fmt.Errorf("policydiag: requirement %q references unknown operation %q", req.ID, req.Operations[0])
			}
			if _, ok := u.keyIndex[req.SubjectKey]; !ok {
				return fmt.Errorf("policydiag: requirement %q references unknown subject %q", req.ID, req.SubjectKey)
			}
			if _, ok := u.fileIndex[req.FileID]; !ok {
				return fmt.Errorf("policydiag: requirement %q references unknown file %q", req.ID, req.FileID)
			}
		case ReqNoUnauthorized:
			for _, op := range req.Operations {
				if _, ok := u.opIndex[op]; !ok {
					return fmt.Errorf("policydiag: requirement %q references unknown operation %q", req.ID, op)
				}
			}
		default:
			return fmt.Errorf("policydiag: requirement %q has unknown kind %q", req.ID, req.Kind)
		}
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
