// Package netsec implements ZFS "zfs allow" style delegation checks for a
// storage management service.
//
// A delegation record (Grant) hands a permission (create, mount, snapshot,
// send, an attribute name, ...) to a subject (a user, a group, or everyone)
// on a dataset, with one of three scopes:
//
//   - ScopeLocal: the record applies only to the dataset itself and never to
//     its descendants.
//   - ScopeDescendants: the record applies only to descendants, not to the
//     dataset carrying the record.
//   - ScopeAll: the record applies both to the dataset itself and to its
//     descendants.
//
// Grants stack along the ancestor chain: a subject's effective permissions on
// a dataset are the union of every matching grant carried by the dataset and
// its ancestors whose scope covers that dataset.
package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Delegation scopes.
const (
	// ScopeLocal limits a grant to the dataset that carries it.
	ScopeLocal Scope = "local"
	// ScopeDescendants limits a grant to strict descendants of the dataset
	// that carries it.
	ScopeDescendants Scope = "descendants"
	// ScopeAll applies a grant to the dataset that carries it and all of its
	// descendants.
	ScopeAll Scope = "all"
)

// Subject kinds.
const (
	SubjectUser     SubjectType = "user"
	SubjectGroup    SubjectType = "group"
	SubjectEveryone SubjectType = "everyone"
)

// Well-known delegated permission names.
const (
	PermCreate   = "create"
	PermDestroy  = "destroy"
	PermMount    = "mount"
	PermSnapshot = "snapshot"
	PermSend     = "send"
	PermReceive  = "receive"
	PermRename   = "rename"
	PermClone    = "clone"
	PermSetuid   = "setuid"
)

// Scope describes how far a grant reaches from the dataset carrying it.
type Scope string

// SubjectType identifies whether a Subject is a user, a group, or everyone.
type SubjectType string

// Operation is the management action that a Request asks to perform.
type Operation string

// Supported operations.
const (
	OpCreate   Operation = "create"
	OpDestroy  Operation = "destroy"
	OpMount    Operation = "mount"
	OpUnmount  Operation = "unmount"
	OpSnapshot Operation = "snapshot"
	OpSend     Operation = "send"
	OpReceive  Operation = "receive"
	OpClone    Operation = "clone"
	OpRename   Operation = "rename"
	OpSet      Operation = "set"
)

// Subject is the principal making a request, or the principal a grant was
// delegated to. Groups lists the groups the requesting user belongs to and is
// only meaningful for a SubjectUser request.
type Subject struct {
	Type   SubjectType
	Name   string
	Groups []string
}

// Grant is one delegation record, equivalent to a single "zfs allow" entry.
type Grant struct {
	Subject    SubjectType
	Name       string
	Permission string
	Dataset    string
	Scope      Scope
}

// Property is an attribute touched by a request, together with the value the
// caller wants to assign. Value is only interpreted for setuid=on at create
// time.
type Property struct {
	Name  string
	Value string
}

// Request is one authorization query.
//
// Dataset is the target dataset. For OpRename, Source and Destination hold
// the existing and the new full dataset names instead. For OpCreate the
// target dataset must not exist yet; Properties lists the attributes supplied
// at creation time.
type Request struct {
	Subject     Subject
	Operation   Operation
	Dataset     string
	Source      string
	Destination string
	Properties  []Property
}

// Decision is the result of an authorization check. When Allowed is false,
// Reasons explains every failed requirement.
type Decision struct {
	Allowed bool
	Reasons []string
}

// Dataset describes one node of the delegated dataset tree. Name is the full
// dataset name (for example "tank/home/alice"); parentage is derived from the
// "/" separators, so every parent must itself appear in the tree.
type Dataset struct {
	Name string
}

// Authorizer evaluates delegation requests against a fixed dataset tree and a
// fixed set of grants.
type Authorizer struct {
	datasets map[string]*Dataset
	grants   []Grant
	cache    map[string]map[string]bool
}

// NewAuthorizer validates the dataset tree and the grants and builds an
// Authorizer. A grant whose dataset is missing, an unknown scope, or a broken
// ancestor chain makes the input unusable and is reported as an error.
func NewAuthorizer(datasets []Dataset, grants []Grant) (*Authorizer, error) {
	a := &Authorizer{
		datasets: make(map[string]*Dataset, len(datasets)),
		grants:   grants,
		cache:    make(map[string]map[string]bool),
	}
	for i := range datasets {
		name := datasets[i].Name
		if err := validateDatasetName(name); err != nil {
			return nil, fmt.Errorf("dataset %d: %w", i, err)
		}
		if _, exists := a.datasets[name]; exists {
			return nil, fmt.Errorf("dataset %q listed more than once", name)
		}
		a.datasets[name] = &datasets[i]
	}
	for name := range a.datasets {
		if parent := parentName(name); parent != "" {
			if _, ok := a.datasets[parent]; !ok {
				return nil, fmt.Errorf("dataset %q references missing parent %q", name, parent)
			}
		}
	}
	for i, g := range grants {
		if err := validateGrant(g); err != nil {
			return nil, fmt.Errorf("grant %d: %w", i, err)
		}
		if _, ok := a.datasets[g.Dataset]; !ok {
			return nil, fmt.Errorf("grant %d: dataset %q is not in the tree", i, g.Dataset)
		}
	}
	return a, nil
}

func validateDatasetName(name string) error {
	if name == "" {
		return errors.New("empty dataset name")
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return fmt.Errorf("malformed dataset name %q", name)
	}
	return nil
}

func validateGrant(g Grant) error {
	switch g.Subject {
	case SubjectUser, SubjectGroup, SubjectEveryone:
	default:
		return fmt.Errorf("unknown subject type %q", g.Subject)
	}
	if g.Name == "" {
		return errors.New("empty subject name")
	}
	if g.Permission == "" {
		return errors.New("empty permission")
	}
	switch g.Scope {
	case ScopeLocal, ScopeDescendants, ScopeAll:
	default:
		return fmt.Errorf("unknown scope %q", g.Scope)
	}
	return nil
}

func validateSubject(s Subject) error {
	switch s.Type {
	case SubjectUser, SubjectGroup, SubjectEveryone:
	default:
		return fmt.Errorf("unknown subject type %q", s.Type)
	}
	if s.Name == "" {
		return errors.New("empty subject name")
	}
	return nil
}

// parentName returns the full name of the parent dataset, or "" for a
// top-level (pool) dataset.
func parentName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i]
	}
	return ""
}

// poolName returns the top-level dataset (pool) segment of a full name.
func poolName(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[:i]
	}
	return name
}

// EffectivePermissions returns the union of permissions the subject holds on
// dataset after every matching grant on the dataset and its ancestors has
// been stacked. Local-only grants on ancestors are intentionally excluded.
func (a *Authorizer) EffectivePermissions(subject Subject, dataset string) (map[string]bool, error) {
	if _, ok := a.datasets[dataset]; !ok {
		return nil, fmt.Errorf("dataset %q is not in the tree", dataset)
	}
	return a.effective(subject, dataset), nil
}

// Authorize evaluates one request and always returns a non-nil Decision.
func (a *Authorizer) Authorize(req Request) *Decision {
	reasons := []string{}
	if err := validateSubject(req.Subject); err != nil {
		return &Decision{Allowed: false, Reasons: []string{"invalid subject: " + err.Error()}}
	}
	switch req.Operation {
	case OpCreate:
		a.checkCreate(req, &reasons)
	case OpRename:
		a.checkRename(req, &reasons)
	case OpSet:
		a.checkSet(req, &reasons)
	default:
		a.checkSimple(req, &reasons)
	}
	return &Decision{Allowed: len(reasons) == 0, Reasons: reasons}
}

// Authorize is a package-level convenience wrapper that builds a throwaway
// Authorizer for a single decision.
func Authorize(datasets []Dataset, grants []Grant, req Request) (*Decision, error) {
	a, err := NewAuthorizer(datasets, grants)
	if err != nil {
		return nil, err
	}
	return a.Authorize(req), nil
}

// EffectivePermissions is the package-level convenience wrapper.
func EffectivePermissions(datasets []Dataset, grants []Grant, subject Subject, dataset string) (map[string]bool, error) {
	a, err := NewAuthorizer(datasets, grants)
	if err != nil {
		return nil, err
	}
	return a.EffectivePermissions(subject, dataset)
}

// effective walks the dataset and each of its ancestors, stacking every grant
// whose scope reaches the dataset and whose subject matches the requester.
func (a *Authorizer) effective(subject Subject, dataset string) map[string]bool {
	key := subjectKey(subject) + "\x00" + dataset
	if cached, ok := a.cache[key]; ok {
		return cached
	}
	perms := map[string]bool{}
	for current, isSelf := dataset, true; current != ""; current, isSelf = parentName(current), false {
		for _, g := range a.grants {
			if g.Dataset != current || !grantReaches(g.Scope, isSelf) || !subjectMatches(g, subject) {
				continue
			}
			perms[g.Permission] = true
		}
	}
	a.cache[key] = perms
	return perms
}

// grantReaches reports whether a grant with the given scope applies to a
// dataset that is either the grant's own dataset (isSelf) or a strict
// descendant of it.
func grantReaches(scope Scope, isSelf bool) bool {
	switch scope {
	case ScopeLocal:
		return isSelf
	case ScopeDescendants:
		return !isSelf
	case ScopeAll:
		return true
	}
	return false
}

func subjectMatches(g Grant, subject Subject) bool {
	switch g.Subject {
	case SubjectEveryone:
		return true
	case SubjectUser:
		return subject.Type == SubjectUser && g.Name == subject.Name
	case SubjectGroup:
		if subject.Type == SubjectGroup && g.Name == subject.Name {
			return true
		}
		for _, group := range subject.Groups {
			if group == g.Name {
				return true
			}
		}
	}
	return false
}

func subjectKey(s Subject) string {
	return string(s.Type) + ":" + s.Name + "|" + strings.Join(s.Groups, ",")
}

func appendReason(reasons *[]string, reason string) {
	*reasons = append(*reasons, reason)
}

// mountGuardedProperties lists attributes that may only be changed while
// holding both the mount permission and the permission named after the
// attribute.
var mountGuardedProperties = map[string]bool{
	"mountpoint": true,
	"sharenfs":   true,
	"canmount":   true,
	PermSetuid:   true,
}

// checkCreate enforces creation on the parent dataset. The target must not
// exist; create is required on the parent and every creation-time attribute is
// checked against the permissions that will be effective on the new dataset,
// i.e. only ancestor grants reach it.
func (a *Authorizer) checkCreate(req Request, reasons *[]string) {
	target := req.Dataset
	if target == "" {
		appendReason(reasons, "create request is missing a target dataset")
		return
	}
	if _, ok := a.datasets[target]; ok {
		appendReason(reasons, fmt.Sprintf("target dataset %q already exists", target))
	}
	parent := parentName(target)
	if parent == "" {
		appendReason(reasons, fmt.Sprintf("cannot create top-level dataset %q", target))
		return
	}
	if _, ok := a.datasets[parent]; !ok {
		appendReason(reasons, fmt.Sprintf("parent dataset %q does not exist", parent))
		return
	}
	a.require(req.Subject, parent, PermCreate, reasons)
	a.checkProperties(req.Subject, target, req.Properties, true, reasons)
}

// checkRename implements the task-specific rule: both the source parent and
// the destination parent must delegate rename and mount to the caller. A
// local-only grant on either parent still counts because the check runs on the
// parent datasets themselves.
func (a *Authorizer) checkRename(req Request, reasons *[]string) {
	if req.Source == "" || req.Destination == "" {
		appendReason(reasons, "rename request requires both source and destination")
		return
	}
	if _, ok := a.datasets[req.Source]; !ok {
		appendReason(reasons, fmt.Sprintf("source dataset %q does not exist", req.Source))
	}
	if _, ok := a.datasets[req.Destination]; ok {
		appendReason(reasons, fmt.Sprintf("destination dataset %q already exists", req.Destination))
	}
	if poolName(req.Source) != poolName(req.Destination) {
		appendReason(reasons, "rename cannot cross pool boundaries")
		return
	}
	sourceParent := parentName(req.Source)
	destParent := parentName(req.Destination)
	if sourceParent == "" || destParent == "" {
		appendReason(reasons, "top-level datasets cannot be renamed")
		return
	}
	if _, ok := a.datasets[sourceParent]; !ok {
		appendReason(reasons, fmt.Sprintf("source parent %q does not exist", sourceParent))
	}
	if _, ok := a.datasets[destParent]; !ok {
		appendReason(reasons, fmt.Sprintf("destination parent %q does not exist", destParent))
	}
	for _, parent := range []string{sourceParent, destParent} {
		if _, ok := a.datasets[parent]; !ok {
			continue
		}
		a.require(req.Subject, parent, PermRename, reasons)
		a.require(req.Subject, parent, PermMount, reasons)
	}
}

// checkSet enforces attribute changes on an existing target dataset.
func (a *Authorizer) checkSet(req Request, reasons *[]string) {
	if _, ok := a.datasets[req.Dataset]; !ok {
		appendReason(reasons, fmt.Sprintf("target dataset %q does not exist", req.Dataset))
		return
	}
	a.checkProperties(req.Subject, req.Dataset, req.Properties, false, reasons)
}

// checkSimple handles operations whose permission name mirrors the operation
// and whose check runs on the target dataset.
func (a *Authorizer) checkSimple(req Request, reasons *[]string) {
	perm, ok := map[Operation]string{
		OpDestroy:  PermDestroy,
		OpMount:    PermMount,
		OpUnmount:  PermMount,
		OpSnapshot: PermSnapshot,
		OpSend:     PermSend,
		OpReceive:  PermReceive,
		OpClone:    PermClone,
	}[req.Operation]
	if !ok {
		appendReason(reasons, fmt.Sprintf("unknown operation %q", req.Operation))
		return
	}
	if _, ok := a.datasets[req.Dataset]; !ok {
		appendReason(reasons, fmt.Sprintf("target dataset %q does not exist", req.Dataset))
		return
	}
	a.require(req.Subject, req.Dataset, perm, reasons)
}

// checkProperties verifies each involved attribute. Every attribute needs the
// permission named after it; the mount-guarded attributes additionally need
// mount. setuid=on at create time is explicitly refused without a setuid
// delegation, closing the create+mount escalation to setuid binaries.
func (a *Authorizer) checkProperties(subject Subject, dataset string, props []Property, create bool, reasons *[]string) {
	seen := map[string]bool{}
	for _, prop := range props {
		name := strings.ToLower(prop.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		eff := a.effective(subject, dataset)
		switch {
		case name == PermSetuid && create && strings.EqualFold(prop.Value, "on") && !eff[PermSetuid]:
			appendReason(reasons, fmt.Sprintf("creating %q with setuid=on requires a setuid delegation", dataset))
		case !eff[name]:
			appendReason(reasons, fmt.Sprintf("missing %s permission on dataset %q", name, dataset))
		}
		if mountGuardedProperties[name] && !eff[PermMount] {
			appendReason(reasons, fmt.Sprintf("changing %s on %q additionally requires the mount permission", name, dataset))
		}
	}
}

// require appends a reason when the subject lacks permission on dataset.
func (a *Authorizer) require(subject Subject, dataset, permission string, reasons *[]string) {
	if !a.effective(subject, dataset)[permission] {
		appendReason(reasons, fmt.Sprintf("missing %s permission on dataset %q", permission, dataset))
	}
}
