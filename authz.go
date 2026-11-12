package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// Sentinel errors returned by Authorizer. Callers should use errors.Is rather
// than compare error strings.
var (
	// ErrNotFound is deliberately used for every failed version lookup in an
	// UPDATE request, including a missing version, a version in another
	// history, and a version the caller cannot read. This prevents an
	// unauthorized caller from learning whether another history exists.
	ErrNotFound = errors.New("deltav: resource not found")

	ErrForbidden         = errors.New("deltav: access forbidden")
	ErrLabelExists       = errors.New("deltav: label already exists in history")
	ErrLabelNotFound     = errors.New("deltav: label does not exist in history")
	ErrCheckoutRequired  = errors.New("deltav: working resource is not checked out by this caller")
	ErrAlreadyCheckedOut = errors.New("deltav: working resource is already checked out")
	ErrAlreadyExists     = errors.New("deltav: resource is already registered")
	ErrInvalidRequest    = errors.New("deltav: invalid request")
)

// Principal identifies an authenticated caller. Tenant is a security
// boundary; Name identifies the caller within that tenant.
type Principal struct {
	Tenant string
	Name   string
}

// History describes one version history. Href is the URL used by DAV
// requests to address the history and must be unique across all tenants.
type History struct {
	Href   string
	Tenant string
}

type principalKey struct {
	tenant string
	name   string
}

func (p Principal) key() principalKey {
	return principalKey{tenant: p.Tenant, name: p.Name}
}

type historyState struct {
	href   string
	tenant string
	labels map[string]string
}

// Authorizer enforces DeltaV version-history authorization. It is safe for
// concurrent handler use. The zero value is usable and is initialized lazily.
type Authorizer struct {
	mu sync.RWMutex

	histories map[string]*historyState
	resources map[string]string
	versions  map[string]string
	working   map[string]string

	versionReads  map[principalKey]map[string]struct{}
	historyWrites map[principalKey]map[string]struct{}

	checkouts map[string]principalKey
}

// NewAuthorizer returns an empty authorizer.
func NewAuthorizer() *Authorizer {
	return &Authorizer{
		histories:     make(map[string]*historyState),
		resources:     make(map[string]string),
		versions:      make(map[string]string),
		working:       make(map[string]string),
		versionReads:  make(map[principalKey]map[string]struct{}),
		historyWrites: make(map[principalKey]map[string]struct{}),
		checkouts:     make(map[string]principalKey),
	}
}

func (a *Authorizer) initLocked() {
	if a.histories == nil {
		a.histories = make(map[string]*historyState)
	}
	if a.resources == nil {
		a.resources = make(map[string]string)
	}
	if a.versions == nil {
		a.versions = make(map[string]string)
	}
	if a.working == nil {
		a.working = make(map[string]string)
	}
	if a.versionReads == nil {
		a.versionReads = make(map[principalKey]map[string]struct{})
	}
	if a.historyWrites == nil {
		a.historyWrites = make(map[principalKey]map[string]struct{})
	}
	if a.checkouts == nil {
		a.checkouts = make(map[string]principalKey)
	}
}

// AddHistory registers a version history. Histories in different tenants may
// not share an href.
func (a *Authorizer) AddHistory(h History) error {
	key, ok := hrefKey(h.Href)
	if !ok || strings.TrimSpace(h.Tenant) == "" {
		return ErrInvalidRequest
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	// A history href must not alias any other kind of resource either.
	if !a.hrefFreeLocked(key) {
		return ErrAlreadyExists
	}
	a.histories[key] = &historyState{
		href:   key,
		tenant: h.Tenant,
		labels: make(map[string]string),
	}
	return nil
}

// AddControlledResource registers a version-controlled resource and associates
// it with its own version history.
func (a *Authorizer) AddControlledResource(href, historyHref string) error {
	key, ok := hrefKey(href)
	if !ok {
		return ErrInvalidRequest
	}
	history, ok := hrefKey(historyHref)
	if !ok {
		return ErrInvalidRequest
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if !a.hrefFreeLocked(key) {
		return ErrAlreadyExists
	}
	if _, exists := a.histories[history]; !exists {
		return ErrNotFound
	}
	a.resources[key] = history
	return nil
}

// AddVersion registers a version in a history. Registration is metadata;
// access still requires a read grant for the specific version.
func (a *Authorizer) AddVersion(href, historyHref string) error {
	key, ok := hrefKey(href)
	if !ok {
		return ErrInvalidRequest
	}
	history, ok := hrefKey(historyHref)
	if !ok {
		return ErrInvalidRequest
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if !a.hrefFreeLocked(key) {
		return ErrAlreadyExists
	}
	if _, exists := a.histories[history]; !exists {
		return ErrNotFound
	}
	a.versions[key] = history
	return nil
}

// AddWorkingResource registers a working resource that can later be checked
// out. In a full server this would normally be created by CHECKOUT.
func (a *Authorizer) AddWorkingResource(href, historyHref string) error {
	key, ok := hrefKey(href)
	if !ok {
		return ErrInvalidRequest
	}
	history, ok := hrefKey(historyHref)
	if !ok {
		return ErrInvalidRequest
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if !a.hrefFreeLocked(key) {
		return ErrAlreadyExists
	}
	if _, exists := a.histories[history]; !exists {
		return ErrNotFound
	}
	a.working[key] = history
	return nil
}

// GrantVersionRead grants a caller read access to one specific version.
// Grants never override the tenant boundary.
func (a *Authorizer) GrantVersionRead(principal Principal, versionHref string) error {
	if !validPrincipal(principal) {
		return ErrInvalidRequest
	}
	key, ok := hrefKey(versionHref)
	if !ok {
		return ErrInvalidRequest
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if _, exists := a.versions[key]; !exists {
		return ErrNotFound
	}
	grants := a.versionReads[principal.key()]
	if grants == nil {
		grants = make(map[string]struct{})
		a.versionReads[principal.key()] = grants
	}
	grants[key] = struct{}{}
	return nil
}

// GrantHistoryWrite grants a caller write access to a version history. Grants
// never override the tenant boundary.
func (a *Authorizer) GrantHistoryWrite(principal Principal, historyHref string) error {
	if !validPrincipal(principal) {
		return ErrInvalidRequest
	}
	key, ok := hrefKey(historyHref)
	if !ok {
		return ErrInvalidRequest
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if _, exists := a.histories[key]; !exists {
		return ErrNotFound
	}
	grants := a.historyWrites[principal.key()]
	if grants == nil {
		grants = make(map[string]struct{})
		a.historyWrites[principal.key()] = grants
	}
	grants[key] = struct{}{}
	return nil
}

// AuthorizeUpdate validates a DAV:version href in an UPDATE request. The
// version must be in the controlled resource's own history, the caller must be
// in that history's tenant, and the caller must hold read access to that exact
// version. Every failure in resolving or authorizing the version returns
// ErrNotFound, intentionally indistinguishable from a nonexistent version.
func (a *Authorizer) AuthorizeUpdate(principal Principal, controlledResourceHref, versionHref string) error {
	if !validPrincipal(principal) {
		return ErrNotFound
	}
	resourceKey, ok := hrefKey(controlledResourceHref)
	if !ok {
		return ErrNotFound
	}
	versionKey, ok := hrefKey(versionHref)
	if !ok {
		return ErrNotFound
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	resourceHistory, exists := a.resources[resourceKey]
	if !exists {
		return ErrNotFound
	}
	versionHistory, exists := a.versions[versionKey]
	if !exists || versionHistory != resourceHistory {
		return ErrNotFound
	}
	history, exists := a.histories[resourceHistory]
	if !exists || history.tenant != principal.Tenant {
		return ErrNotFound
	}
	if !hasGrant(a.versionReads[principal.key()], versionKey) {
		return ErrNotFound
	}
	return nil
}

// LabelAction identifies one of the RFC 3253 LABEL operations.
type LabelAction uint8

const (
	LabelAdd LabelAction = iota + 1
	LabelSet
	LabelRemove
)

// String implements fmt.Stringer.
func (action LabelAction) String() string {
	switch action {
	case LabelAdd:
		return "add"
	case LabelSet:
		return "set"
	case LabelRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// LabelChange is a parsed LABEL request. Name is the DAV:label-name value.
// VersionHref is required for add and set and is ignored for remove.
type LabelChange struct {
	Action      LabelAction
	Name        string
	VersionHref string
}

// AuthorizeLabel atomically authorizes and applies a LABEL request to a
// version history.
//
// The caller must belong to the history's tenant and hold write access to the
// history. Labels are scoped to that history, so equal names in different
// histories are independent. Add fails with ErrLabelExists if the name is
// already used in the same history. Set creates or moves the label to the
// target version. Remove fails with ErrLabelNotFound if the name is absent.
// The target version of add/set must belong to the addressed history.
func (a *Authorizer) AuthorizeLabel(principal Principal, historyHref string, change LabelChange) error {
	if !validPrincipal(principal) {
		return ErrNotFound
	}
	historyKey, ok := hrefKey(historyHref)
	if !ok {
		return ErrNotFound
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	history, exists := a.histories[historyKey]
	if !exists || history.tenant != principal.Tenant {
		return ErrNotFound
	}
	if !hasGrant(a.historyWrites[principal.key()], historyKey) {
		return ErrForbidden
	}
	if strings.TrimSpace(change.Name) == "" || strings.ContainsRune(change.Name, '\x00') {
		return ErrInvalidRequest
	}

	switch change.Action {
	case LabelAdd, LabelSet:
		versionKey, ok := hrefKey(change.VersionHref)
		if !ok {
			return ErrNotFound
		}
		versionHistory, exists := a.versions[versionKey]
		if !exists || versionHistory != historyKey {
			return ErrNotFound
		}
		if change.Action == LabelAdd {
			if _, exists := history.labels[change.Name]; exists {
				return ErrLabelExists
			}
		}
		history.labels[change.Name] = versionKey
		return nil

	case LabelRemove:
		if _, exists := history.labels[change.Name]; !exists {
			return ErrLabelNotFound
		}
		delete(history.labels, change.Name)
		return nil

	default:
		return ErrInvalidRequest
	}
}

// LabelVersion returns the version href currently associated with a label in
// a history. It only answers callers that belong to the history's tenant and
// hold write access to it; all other callers receive false.
func (a *Authorizer) LabelVersion(principal Principal, historyHref, name string) (string, bool) {
	if !validPrincipal(principal) {
		return "", false
	}
	historyKey, ok := hrefKey(historyHref)
	if !ok {
		return "", false
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	history, exists := a.histories[historyKey]
	if !exists || history.tenant != principal.Tenant {
		return "", false
	}
	if !hasGrant(a.historyWrites[principal.key()], historyKey) {
		return "", false
	}
	version, exists := history.labels[name]
	return version, exists
}

// Checkout records that principal owns the checkout of a working resource.
// The caller must belong to the working resource's tenant history and hold
// write access to it. A second checkout by anyone fails without disclosing the
// current owner.
func (a *Authorizer) Checkout(principal Principal, workingResourceHref string) error {
	if !validPrincipal(principal) {
		return ErrNotFound
	}
	workingKey, ok := hrefKey(workingResourceHref)
	if !ok {
		return ErrNotFound
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	historyKey, exists := a.working[workingKey]
	if !exists {
		return ErrNotFound
	}
	history, exists := a.histories[historyKey]
	if !exists || history.tenant != principal.Tenant {
		return ErrNotFound
	}
	if !hasGrant(a.historyWrites[principal.key()], historyKey) {
		return ErrForbidden
	}
	if _, exists := a.checkouts[workingKey]; exists {
		return ErrAlreadyCheckedOut
	}
	a.checkouts[workingKey] = principal.key()
	return nil
}

// AuthorizeCheckin atomically verifies and consumes the caller's checkout of a
// working resource. A working resource that is unknown, belongs to another
// tenant, is not checked out, or is checked out by a different caller is
// denied. The last two cases deliberately return the same error so that a
// caller cannot probe another caller's checkout status.
func (a *Authorizer) AuthorizeCheckin(principal Principal, workingResourceHref string) error {
	if !validPrincipal(principal) {
		return ErrNotFound
	}
	workingKey, ok := hrefKey(workingResourceHref)
	if !ok {
		return ErrNotFound
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	historyKey, exists := a.working[workingKey]
	if !exists {
		return ErrNotFound
	}
	history, exists := a.histories[historyKey]
	if !exists || history.tenant != principal.Tenant {
		return ErrNotFound
	}
	owner, exists := a.checkouts[workingKey]
	if !exists || owner != principal.key() {
		return ErrCheckoutRequired
	}
	delete(a.checkouts, workingKey)
	return nil
}

func (a *Authorizer) hrefFreeLocked(href string) bool {
	if _, exists := a.resources[href]; exists {
		return false
	}
	if _, exists := a.versions[href]; exists {
		return false
	}
	if _, exists := a.working[href]; exists {
		return false
	}
	if _, exists := a.histories[href]; exists {
		return false
	}
	return true
}

func validPrincipal(principal Principal) bool {
	return strings.TrimSpace(principal.Tenant) != "" && strings.TrimSpace(principal.Name) != ""
}

func hasGrant(grants map[string]struct{}, href string) bool {
	_, ok := grants[href]
	return ok
}

// hrefKey canonicalizes a DAV href without allowing URL forms that could
// address something other than a resource path. Unsupported or ambiguous
// forms fail closed and are treated as nonexistent by authorization methods.
func hrefKey(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	if u.Scheme != "" || u.Host != "" {
		if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "", false
		}
	}
	if u.Path == "" && u.EscapedPath() == "" {
		return "", false
	}
	return u.String(), true
}

// String makes errors from this package easier to include in diagnostics
// without accidentally embedding an href.
func (p Principal) String() string {
	return fmt.Sprintf("%s/%s", p.Tenant, p.Name)
}
