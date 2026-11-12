package netsec

import "fmt"

// CodeIdentity is the code-signing identity used for authorization matching.
// Both fields must match an authorization entry. An identity missing either
// field is treated as unsigned.
type CodeIdentity struct {
	SigningID string
	TeamID    string
}

func (id CodeIdentity) signed() bool {
	return id.SigningID != "" && id.TeamID != ""
}

// Library describes a dynamic library loaded by a process. A library is
// unsigned unless it has both a signing identifier and a team identifier.
type Library struct {
	Path              string
	SigningID         string
	SigningIdentifier string
	TeamID            string
	TeamIdentifier    string
	Signed            bool
	IsSigned          bool
	Unsigned          bool
	IsUnsigned        bool
}

func (lib Library) signed() bool {
	if lib.Unsigned || lib.IsUnsigned {
		return false
	}
	if lib.Signed || lib.IsSigned {
		return true
	}
	return lib.signingID() != "" && lib.teamID() != ""
}

func (lib Library) signingID() string {
	if lib.SigningID != "" {
		return lib.SigningID
	}
	return lib.SigningIdentifier
}

func (lib Library) teamID() string {
	if lib.TeamID != "" {
		return lib.TeamID
	}
	return lib.TeamIdentifier
}

// Process is one node in a process tree. ParentPID identifies the launching
// process. DisclaimsResponsibility makes the process responsible for itself
// instead of inheriting responsibility from an ancestor.
type Process struct {
	PID                      int
	ParentPID                int
	PPID                     int
	SigningID                string
	SigningIdentifier        string
	CodeSigningID            string
	ClientID                 string
	TeamID                   string
	TeamIdentifier           string
	DisclaimsResponsibility  bool
	WaivesResponsibility     bool
	SelfResponsible          bool
	AppBundleHelper          bool
	IsAppBundleHelper        bool
	Debugged                 bool
	IsDebugged               bool
	InjectedLibraries        []Library
	InjectedDylibs           []Library
	HasUnsignedInjectedDylib bool
	InjectedUnsignedDylib    bool
	UnsignedDylibInjected    bool
}

func (p Process) identity() CodeIdentity {
	return CodeIdentity{SigningID: p.signingID(), TeamID: p.teamID()}
}

func (p Process) signingID() string {
	switch {
	case p.SigningID != "":
		return p.SigningID
	case p.SigningIdentifier != "":
		return p.SigningIdentifier
	case p.CodeSigningID != "":
		return p.CodeSigningID
	default:
		return p.ClientID
	}
}

func (p Process) teamID() string {
	if p.TeamID != "" {
		return p.TeamID
	}
	return p.TeamIdentifier
}

func (p Process) parentPID() int {
	if p.ParentPID != 0 {
		return p.ParentPID
	}
	return p.PPID
}

func (p Process) disclaimsResponsibility() bool {
	return p.DisclaimsResponsibility || p.WaivesResponsibility || p.SelfResponsible
}

func (p Process) appBundleHelper() bool {
	return p.AppBundleHelper || p.IsAppBundleHelper
}

func (p Process) debugged() bool {
	return p.Debugged || p.IsDebugged
}

func (p Process) hasUnsignedInjectedLibrary() bool {
	if p.HasUnsignedInjectedDylib || p.InjectedUnsignedDylib || p.UnsignedDylibInjected {
		return true
	}
	libraries := make([]Library, 0, len(p.InjectedLibraries)+len(p.InjectedDylibs))
	libraries = append(libraries, p.InjectedLibraries...)
	libraries = append(libraries, p.InjectedDylibs...)
	for _, lib := range libraries {
		if !lib.signed() {
			return true
		}
	}
	return false
}

// Authorization is one row in the authorization table. Service, ClientID and
// TeamID must all match the responsible process for Allowed to take effect.
// A denial for an otherwise matching row takes precedence.
type Authorization struct {
	Service           string
	ClientID          string
	ClientIdentifier  string
	SigningID         string
	SigningIdentifier string
	TeamID            string
	TeamIdentifier    string
	Allowed           bool
	Allow             bool
	Granted           bool
}

func (a Authorization) clientID() string {
	switch {
	case a.ClientID != "":
		return a.ClientID
	case a.ClientIdentifier != "":
		return a.ClientIdentifier
	case a.SigningID != "":
		return a.SigningID
	default:
		return a.SigningIdentifier
	}
}

func (a Authorization) teamID() string {
	if a.TeamID != "" {
		return a.TeamID
	}
	return a.TeamIdentifier
}

func (a Authorization) allowed() bool {
	return a.Allowed || a.Allow || a.Granted
}

// Reason explains why a request was allowed or denied.
type Reason string

const (
	ReasonAllowed                    Reason = "allowed"
	ReasonUnknownProcess             Reason = "unknown_process"
	ReasonInvalidProcessTree         Reason = "invalid_process_tree"
	ReasonUnsignedProcess            Reason = "unsigned_process"
	ReasonUnsignedResponsibleProcess Reason = "unsigned_responsible_process"
	ReasonBundleHelperWithoutApp     Reason = "bundle_helper_without_app"
	ReasonDebugged                   Reason = "debugged"
	ReasonUnsignedInjectedLibrary    Reason = "unsigned_injected_library"
	ReasonNoMatchingAuthorization    Reason = "no_matching_authorization"
	ReasonAuthorizationDenied        Reason = "authorization_denied"
)

// Decision is the result of a simulated authorization request.
type Decision struct {
	Allowed             bool
	Reason              Reason
	ResponsiblePID      int
	Responsible         Process
	ResponsibleIdentity CodeIdentity
}

// Common integration aliases. The canonical names are Process,
// Authorization and Decision.
type (
	ProcessNode        = Process
	ProcessTree        = []Process
	AuthEntry          = Authorization
	AuthorizationEntry = Authorization
	AuthTable          = []Authorization
	AuthorizationTable = []Authorization
	Dylib              = Library
	SignedLibrary      = Library
	Result             = Decision
)

// Policy evaluates authorization requests against an immutable snapshot of a
// process tree and authorization table.
type Policy struct {
	processes      map[int]Process
	authorizations []Authorization
}

// NewPolicy creates a policy from a process snapshot and authorization table.
// Duplicate process IDs use the last entry, matching the usual behavior of a
// snapshot being overlaid with newer information.
func NewPolicy(processes []Process, authorizations []Authorization) *Policy {
	index := make(map[int]Process, len(processes))
	for _, process := range processes {
		index[process.PID] = process
	}

	return &Policy{
		processes:      index,
		authorizations: append([]Authorization(nil), authorizations...),
	}
}

// New is a short alias for NewPolicy.
func New(processes []Process, authorizations []Authorization) *Policy {
	return NewPolicy(processes, authorizations)
}

// Check evaluates whether the process identified by pid may use service.
func (p *Policy) Check(pid int, service string) Decision {
	if p == nil {
		return Decision{Reason: ReasonInvalidProcessTree}
	}

	requestor, ok := p.processes[pid]
	if !ok {
		return Decision{Reason: ReasonUnknownProcess}
	}

	responsible, chain, failure := p.responsibilityChain(pid)
	decision := Decision{
		ResponsiblePID:      responsible.PID,
		Responsible:         responsible,
		ResponsibleIdentity: responsible.identity(),
	}

	// An unsigned binary cannot borrow the identity or authorization of an
	// ancestor. This check applies even to app-bundle helpers.
	if !requestor.identity().signed() {
		decision.Reason = ReasonUnsignedProcess
		return decision
	}

	if failure != "" {
		decision.Reason = failure
		return decision
	}

	// A compromised process in the attribution chain cannot pass an inherited
	// authorization to the requestor.
	for _, process := range chain {
		if process.debugged() {
			decision.Reason = ReasonDebugged
			return decision
		}
		if process.hasUnsignedInjectedLibrary() {
			decision.Reason = ReasonUnsignedInjectedLibrary
			return decision
		}
	}

	if !responsible.identity().signed() {
		decision.Reason = ReasonUnsignedResponsibleProcess
		return decision
	}

	decision.Reason = ReasonNoMatchingAuthorization
	found := false
	for _, entry := range p.authorizations {
		if entry.Service != service ||
			entry.clientID() != responsible.signingID() ||
			entry.teamID() != responsible.teamID() {
			continue
		}
		found = true
		if !entry.allowed() {
			decision.Reason = ReasonAuthorizationDenied
			return decision
		}
	}

	if found {
		decision.Allowed = true
		decision.Reason = ReasonAllowed
	}
	return decision
}

// Authorize is a boolean convenience wrapper around Check.
func (p *Policy) Authorize(pid int, service string) bool {
	return p.Check(pid, service).Allowed
}

// Evaluate is an alias for Check.
func (p *Policy) Evaluate(pid int, service string) Decision {
	return p.Check(pid, service)
}

// FindResponsible returns the process that bears responsibility for pid. It
// walks the launch chain until it reaches a process that disclaims
// responsibility, an app-bundle helper with no app parent, or a tree root.
func (p *Policy) FindResponsible(pid int) (Process, bool) {
	responsible, _, failure := p.responsibilityChain(pid)
	return responsible, failure == ""
}

func (p *Policy) responsibilityChain(pid int) (Process, []Process, Reason) {
	if p == nil {
		return Process{}, nil, ReasonInvalidProcessTree
	}

	current, ok := p.processes[pid]
	if !ok {
		return Process{}, nil, ReasonUnknownProcess
	}

	chain := []Process{current}
	visited := map[int]struct{}{current.PID: {}}
	for {
		if current.disclaimsResponsibility() {
			return current, chain, ""
		}

		if current.parentPID() == 0 {
			if current.appBundleHelper() {
				return current, chain, ReasonBundleHelperWithoutApp
			}
			return current, chain, ""
		}

		parent, ok := p.processes[current.parentPID()]
		if !ok {
			// A snapshot can start below launchd. Treat a missing parent as the
			// top of the known tree unless the node claims to be an app helper,
			// which must be attributed to its containing app.
			if current.appBundleHelper() {
				return current, chain, ReasonBundleHelperWithoutApp
			}
			return current, chain, ""
		}

		if _, seen := visited[parent.PID]; seen {
			return current, chain, ReasonInvalidProcessTree
		}
		visited[parent.PID] = struct{}{}
		chain = append(chain, parent)
		current = parent
	}
}

// Check evaluates one authorization request against a process tree and
// authorization table. It is a convenience wrapper around Policy.Check.
func Check(processes []Process, authorizations []Authorization, pid int, service string) Decision {
	return NewPolicy(processes, authorizations).Check(pid, service)
}

// Evaluate is an alias for Check.
func Evaluate(processes []Process, authorizations []Authorization, pid int, service string) Decision {
	return Check(processes, authorizations, pid, service)
}

// Decide is a short alias for Check.
func Decide(processes []Process, authorizations []Authorization, pid int, service string) Decision {
	return Check(processes, authorizations, pid, service)
}

// Authorize reports whether pid may use service.
func Authorize(processes []Process, authorizations []Authorization, pid int, service string) bool {
	return Check(processes, authorizations, pid, service).Allowed
}

// IsAuthorized reports whether pid may use service.
func IsAuthorized(processes []Process, authorizations []Authorization, pid int, service string) bool {
	return Authorize(processes, authorizations, pid, service)
}

// FindResponsible returns the responsible process for pid from a process
// snapshot.
func FindResponsible(processes []Process, pid int) (Process, bool) {
	return NewPolicy(processes, nil).FindResponsible(pid)
}

// ResolveResponsible is an alias for FindResponsible.
func ResolveResponsible(processes []Process, pid int) (Process, bool) {
	return FindResponsible(processes, pid)
}

func (d Decision) String() string {
	if d.Allowed {
		return fmt.Sprintf("allowed: responsible pid %d", d.ResponsiblePID)
	}
	return fmt.Sprintf("denied (%s): responsible pid %d", d.Reason, d.ResponsiblePID)
}
