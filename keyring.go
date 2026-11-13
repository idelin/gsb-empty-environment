package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Permission is a Linux key permission mask. Each byte describes the
// permissions of one access class: other, group, owner, and possessor.
type Permission uint32

const (
	PermView    Permission = 1 << 0
	PermRead    Permission = 1 << 1
	PermWrite   Permission = 1 << 2
	PermSearch  Permission = 1 << 3
	PermLink    Permission = 1 << 4
	PermSetattr Permission = 1 << 5

	PermAll Permission = PermView | PermRead | PermWrite | PermSearch | PermLink | PermSetattr
)

const (
	PermOtherShift     = 0
	PermGroupShift     = 8
	PermOwnerShift     = 16
	PermPossessorShift = 24
)

// Operation is an operation that may be performed through a keyring.
type Operation string

const (
	OperationView    Operation = "view"
	OperationRead    Operation = "read"
	OperationSearch  Operation = "search"
	OperationLink    Operation = "link"
	OperationSetattr Operation = "setattr"
)

const (
	View    = OperationView
	Read    = OperationRead
	Search  = OperationSearch
	Link    = OperationLink
	Setattr = OperationSetattr
)

// MaxSearchDepth is the largest number of keyring links followed while
// looking for a key from a thread, process, or session keyring.
const MaxSearchDepth = 6

// KeyID identifies a linked key. Zero is reserved as an unspecified ID.
type KeyID uint32

// KeyringID identifies a keyring. Zero is reserved as an unspecified ID.
type KeyringID uint32

// Key contains the ownership and permission metadata used for a decision.
type Key struct {
	OwnerUID    uint32
	OwnerGID    uint32
	Permissions Permission

	ID   KeyID
	Perm Permission
}

// Credentials are the UID and GID against which a key is checked.
type Credentials struct {
	UID uint32
	GID uint32
}

// Cred is a short alias for Credentials.
type Cred = Credentials

// Caller describes the credentials and keyrings inherited by a process.
type Caller struct {
	UID uint32
	GID uint32

	Credentials Credentials

	ThreadKeyring  KeyringID
	ProcessKeyring KeyringID
	SessionKeyring KeyringID

	Thread  KeyringID
	Process KeyringID
	Session KeyringID
}

// Keyring is a node in the link graph. Links point to keyrings and Keys
// identify keys directly linked by this keyring.
type Keyring struct {
	ID           KeyringID
	Description  string
	Anonymous    bool
	NewlyCreated bool
	New          bool

	Links          []KeyringID
	LinkedKeyrings []KeyringID

	Keys       []KeyID
	LinkedKeys []KeyID
}

// KeyringGraph maps keyring IDs to their link relationships. A slice of
// Keyring values may be supplied instead of a graph.
type KeyringGraph map[KeyringID]Keyring

// Ring and Graph are aliases for Keyring and KeyringGraph.
type Ring = Keyring
type Graph = KeyringGraph

// SandboxPolicy describes the session isolation required for a sandbox.
// A zero policy disables sandbox checks.
type SandboxPolicy struct {
	Enabled bool

	SessionKeyring     KeyringID
	HostSessionKeyring KeyringID
	HostKeyring        KeyringID
	HostKeyrings       []KeyringID

	NewAnonymousSession bool
	NewAnonymous        bool
	AnonymousSession    bool
	NewSession          bool
	NewlyCreatedSession bool
}

// Policy is an alias for SandboxPolicy.
type Policy = SandboxPolicy

// Request is the complete input to an access decision.
type Request struct {
	Key         Key
	KeyID       KeyID
	TargetKeyID KeyID

	Caller      Caller
	Credentials Credentials

	ThreadKeyring  KeyringID
	ProcessKeyring KeyringID
	SessionKeyring KeyringID

	Keyrings  []Keyring
	Rings     []Keyring
	Graph     KeyringGraph
	Adjacency map[KeyringID][]KeyringID

	Operation Operation
	Op        Operation

	Sandbox SandboxPolicy
	Policy  *SandboxPolicy
}

// Reason explains why a request was allowed or denied.
type Reason string

const (
	ReasonNone                      Reason = ""
	ReasonUnknownOperation          Reason = "unknown operation"
	ReasonPermissionDenied          Reason = "permission denied"
	ReasonSandboxSharedSession      Reason = "sandbox session keyring is shared with host"
	ReasonSandboxSessionNotIsolated Reason = "sandbox session keyring is not newly created and anonymous"
	ReasonSandboxLinkedToHost       Reason = "sandbox keyring is linked into host keyring"
)

// Result is the complete result of an access decision.
type Result struct {
	Allowed              bool
	Possessor            bool
	EffectivePermissions Permission
	Depth                int
	Reason               Reason
}

// Decision is a boolean access decision.
type Decision bool

// Allowed reports whether access is permitted.
func (d Decision) Allowed() bool {
	return bool(d)
}

// IsAllowed reports whether access is permitted.
func (d Decision) IsAllowed() bool {
	return bool(d)
}

// AccessDeniedError is returned by Check when access is denied.
type AccessDeniedError struct {
	Result Result
}

func (e *AccessDeniedError) Error() string {
	if e == nil {
		return "access denied"
	}
	if e.Result.Reason == ReasonNone {
		return "access denied"
	}
	return "access denied: " + string(e.Result.Reason)
}

// Evaluate performs the access decision described by the request.
func (r Request) Evaluate() Result {
	return evaluateRequest(r)
}

// Authorize reports whether the request is permitted.
func (r Request) Authorize() Decision {
	return Decision(r.Evaluate().Allowed)
}

// Check returns an error when the request is denied.
func (r Request) Check() error {
	result := r.Evaluate()
	if result.Allowed {
		return nil
	}
	return &AccessDeniedError{Result: result}
}

// ParseOperation converts a textual key operation into an Operation.
func ParseOperation(value string) (Operation, error) {
	switch Operation(strings.ToLower(strings.TrimSpace(value))) {
	case OperationView:
		return OperationView, nil
	case OperationRead:
		return OperationRead, nil
	case OperationSearch:
		return OperationSearch, nil
	case OperationLink:
		return OperationLink, nil
	case OperationSetattr:
		return OperationSetattr, nil
	default:
		return "", fmt.Errorf("unknown key operation %q", value)
	}
}

// Permission returns the permission bit required by the operation.
func (op Operation) Permission() (Permission, bool) {
	switch op {
	case OperationView:
		return PermView, true
	case OperationRead:
		return PermRead, true
	case OperationSearch:
		return PermSearch, true
	case OperationLink:
		return PermLink, true
	case OperationSetattr:
		return PermSetattr, true
	default:
		return 0, false
	}
}

// Has reports whether the permission mask contains the operation's bit.
func (p Permission) Has(op Operation) bool {
	required, ok := op.Permission()
	return ok && p&required != 0
}

// ClassPermissions returns the applicable owner, group, or other byte.
// Ownership takes precedence over group membership, which takes precedence
// over the other class.
func (k Key) ClassPermissions(credentials Credentials) Permission {
	mask := k.Permissions | k.Perm
	switch {
	case credentials.UID == k.OwnerUID:
		return (mask >> PermOwnerShift) & 0xff
	case credentials.GID == k.OwnerGID:
		return (mask >> PermGroupShift) & 0xff
	default:
		return (mask >> PermOtherShift) & 0xff
	}
}

// EffectivePermissions returns the union of the possessor byte and the
// applicable ownership class. Without possession, only the ownership class
// applies.
func (k Key) EffectivePermissions(credentials Credentials, possessor bool) Permission {
	permissions := k.ClassPermissions(credentials)
	if possessor {
		permissions |= ((k.Permissions | k.Perm) >> PermPossessorShift) & 0xff
	}
	return permissions
}

// Allows reports whether the key permits the operation for the credentials.
func (k Key) Allows(credentials Credentials, possessor bool, operation Operation) bool {
	return k.EffectivePermissions(credentials, possessor).Has(operation)
}

// FindKey searches all three caller keyring roots for keyID. It returns the
// shortest link depth and whether the key was found within maxDepth.
func FindKey(keyID KeyID, roots []KeyringID, graph KeyringGraph, maxDepth int) (int, bool) {
	if keyID == 0 || maxDepth < 0 {
		return 0, false
	}

	type node struct {
		id    KeyringID
		depth int
	}

	queue := make([]node, 0, len(roots))
	visited := make(map[KeyringID]int, len(graph))
	for _, root := range roots {
		if root == 0 {
			continue
		}
		if previous, ok := visited[root]; ok && previous == 0 {
			continue
		}
		visited[root] = 0
		queue = append(queue, node{id: root})
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if KeyID(current.id) == keyID {
			return current.depth, true
		}
		if current.depth >= maxDepth {
			continue
		}

		ring, ok := graph[current.id]
		if !ok {
			continue
		}
		for _, key := range allLinkedKeys(ring) {
			if key == keyID {
				return current.depth + 1, true
			}
		}
		for _, child := range allLinkedKeyrings(ring) {
			if child == 0 {
				continue
			}
			nextDepth := current.depth + 1
			if oldDepth, seen := visited[child]; seen && oldDepth <= nextDepth {
				continue
			}
			visited[child] = nextDepth
			queue = append(queue, node{id: child, depth: nextDepth})
		}
	}

	return 0, false
}

// Evaluate performs an access decision for a request.
func Evaluate(args ...any) Result {
	request, err := buildRequest(args)
	if err != nil {
		return Result{Possessor: false, Reason: Reason(err.Error())}
	}
	return request.Evaluate()
}

// Decide returns a boolean access decision and is an alias for Authorize.
func Decide(args ...any) Decision {
	return Authorize(args...)
}

// Authorize returns a boolean access decision for the supplied request.
func Authorize(args ...any) Decision {
	return Decision(Evaluate(args...).Allowed)
}

// Allowed reports whether the supplied request is permitted.
func Allowed(args ...any) bool {
	return bool(Authorize(args...))
}

// CanAccess reports whether the supplied request is permitted.
func CanAccess(args ...any) bool {
	return bool(Authorize(args...))
}

// CheckAccess reports whether the supplied request is permitted.
func CheckAccess(args ...any) bool {
	return bool(Authorize(args...))
}

// Check returns an error when the supplied request is denied.
func Check(args ...any) error {
	result := Evaluate(args...)
	if result.Allowed {
		return nil
	}
	return &AccessDeniedError{Result: result}
}

func evaluateRequest(request Request) Result {
	operation := request.Operation
	if operation == "" {
		operation = request.Op
	}
	if _, ok := operation.Permission(); !ok {
		return Result{Reason: ReasonUnknownOperation}
	}

	credentials, caller, graph := normalizeRequest(request)
	session := request.SessionKeyring
	if session == 0 {
		session = caller.SessionKeyring
	}
	if session == 0 {
		session = caller.Session
	}

	policy := request.Sandbox
	if request.Policy != nil {
		policy = *request.Policy
	}
	if policy.SessionKeyring != 0 {
		session = policy.SessionKeyring
	}
	if reason := validateSandbox(policy, session, graph); reason != ReasonNone {
		return Result{Reason: reason}
	}

	roots := []KeyringID{
		caller.ThreadKeyring,
		caller.ProcessKeyring,
		session,
	}
	if caller.ThreadKeyring == 0 {
		roots[0] = caller.Thread
	}
	if caller.ProcessKeyring == 0 {
		roots[1] = caller.Process
	}

	keyID := request.TargetKeyID
	if keyID == 0 {
		keyID = request.KeyID
	}
	if keyID == 0 {
		keyID = request.Key.ID
	}
	depth, possessor := FindKey(keyID, roots, graph, MaxSearchDepth)
	effective := request.Key.EffectivePermissions(credentials, possessor)

	result := Result{
		Possessor:            possessor,
		EffectivePermissions: effective,
		Depth:                depth,
	}
	if !effective.Has(operation) {
		result.Reason = ReasonPermissionDenied
		return result
	}

	result.Allowed = true
	return result
}

func normalizeRequest(request Request) (Credentials, Caller, KeyringGraph) {
	caller := request.Caller
	credentials := request.Credentials
	if credentials.UID == 0 && credentials.GID == 0 {
		credentials = caller.Credentials
	}
	if credentials.UID == 0 && credentials.GID == 0 {
		credentials = Credentials{UID: caller.UID, GID: caller.GID}
	}
	if caller.UID == 0 && caller.GID == 0 && (credentials.UID != 0 || credentials.GID != 0) {
		caller.UID = credentials.UID
		caller.GID = credentials.GID
	}
	if request.ThreadKeyring != 0 {
		caller.ThreadKeyring = request.ThreadKeyring
	}
	if request.ProcessKeyring != 0 {
		caller.ProcessKeyring = request.ProcessKeyring
	}
	if request.SessionKeyring != 0 {
		caller.SessionKeyring = request.SessionKeyring
	}

	graph := make(KeyringGraph, len(request.Graph)+len(request.Keyrings)+len(request.Rings))
	for id, ring := range request.Graph {
		if ring.ID == 0 {
			ring.ID = id
		}
		addKeyring(graph, ring)
	}
	for _, ring := range request.Keyrings {
		addKeyring(graph, ring)
	}
	for _, ring := range request.Rings {
		addKeyring(graph, ring)
	}
	for id, links := range request.Adjacency {
		addKeyring(graph, Keyring{ID: id, Links: links})
	}
	return credentials, caller, graph
}

func addKeyring(graph KeyringGraph, ring Keyring) {
	if ring.ID == 0 {
		return
	}
	existing, ok := graph[ring.ID]
	if !ok {
		ring.Links = allLinkedKeyrings(ring)
		ring.LinkedKeyrings = nil
		ring.Keys = allLinkedKeys(ring)
		ring.LinkedKeys = nil
		graph[ring.ID] = ring
		return
	}
	existing.Links = append(existing.Links, allLinkedKeyrings(ring)...)
	existing.Keys = append(existing.Keys, allLinkedKeys(ring)...)
	if existing.Description == "" {
		existing.Description = ring.Description
	}
	existing.Anonymous = existing.Anonymous || ring.Anonymous
	existing.NewlyCreated = existing.NewlyCreated || ring.NewlyCreated
	existing.NewlyCreated = existing.NewlyCreated || ring.New
	graph[ring.ID] = existing
}

func allLinkedKeyrings(ring Keyring) []KeyringID {
	links := make([]KeyringID, 0, len(ring.Links)+len(ring.LinkedKeyrings))
	links = append(links, ring.Links...)
	links = append(links, ring.LinkedKeyrings...)
	return links
}

func allLinkedKeys(ring Keyring) []KeyID {
	keys := make([]KeyID, 0, len(ring.Keys)+len(ring.LinkedKeys))
	keys = append(keys, ring.Keys...)
	keys = append(keys, ring.LinkedKeys...)
	return keys
}

func validateSandbox(policy SandboxPolicy, session KeyringID, graph KeyringGraph) Reason {
	if !sandboxEnabled(policy) {
		return ReasonNone
	}
	if session == 0 {
		return ReasonSandboxSessionNotIsolated
	}
	if policy.HostSessionKeyring != 0 && session == policy.HostSessionKeyring {
		return ReasonSandboxSharedSession
	}

	ring, hasRing := graph[session]
	isolated := policy.NewAnonymousSession ||
		policy.NewAnonymous ||
		policy.AnonymousSession ||
		policy.NewSession ||
		policy.NewlyCreatedSession
	if hasRing {
		newlyCreated := ring.NewlyCreated || ring.New
		isolated = isolated || (ring.Anonymous && newlyCreated)
		if ring.Description == "" && newlyCreated {
			isolated = true
		}
	}
	if !isolated {
		return ReasonSandboxSessionNotIsolated
	}

	hostRoots := append([]KeyringID(nil), policy.HostKeyrings...)
	if policy.HostKeyring != 0 {
		hostRoots = append(hostRoots, policy.HostKeyring)
	}
	if policy.HostSessionKeyring != 0 {
		hostRoots = append(hostRoots, policy.HostSessionKeyring)
	}

	for _, hostRoot := range hostRoots {
		if hostRoot == 0 {
			continue
		}
		if hostRoot == session {
			return ReasonSandboxSharedSession
		}
		if reachableAnyDepth(hostRoot, session, graph) {
			return ReasonSandboxLinkedToHost
		}
		if reachableAnyDepth(session, hostRoot, graph) {
			return ReasonSandboxLinkedToHost
		}
	}

	return ReasonNone
}

func sandboxEnabled(policy SandboxPolicy) bool {
	return policy.Enabled ||
		policy.SessionKeyring != 0 ||
		policy.HostSessionKeyring != 0 ||
		policy.HostKeyring != 0 ||
		len(policy.HostKeyrings) > 0 ||
		policy.NewAnonymousSession ||
		policy.NewAnonymous ||
		policy.AnonymousSession ||
		policy.NewSession ||
		policy.NewlyCreatedSession
}

func reachableAnyDepth(from, to KeyringID, graph KeyringGraph) bool {
	if from == 0 || to == 0 {
		return false
	}
	queue := []KeyringID{from}
	visited := map[KeyringID]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == to {
			return true
		}
		ring, ok := graph[current]
		if !ok {
			continue
		}
		for _, child := range allLinkedKeyrings(ring) {
			if child == 0 || visited[child] {
				continue
			}
			visited[child] = true
			queue = append(queue, child)
		}
	}
	return false
}

func buildRequest(args []any) (Request, error) {
	var request Request
	for _, arg := range args {
		switch value := arg.(type) {
		case Request:
			request = mergeRequest(request, value)
		case *Request:
			if value == nil {
				return Request{}, errors.New("nil request")
			}
			request = mergeRequest(request, *value)
		case Key:
			request.Key = value
		case *Key:
			if value == nil {
				return Request{}, errors.New("nil key")
			}
			request.Key = *value
		case KeyID:
			request.TargetKeyID = value
		case KeyringID:
			switch {
			case request.Caller.ThreadKeyring == 0:
				request.Caller.ThreadKeyring = value
			case request.Caller.ProcessKeyring == 0:
				request.Caller.ProcessKeyring = value
			case request.Caller.SessionKeyring == 0:
				request.Caller.SessionKeyring = value
			default:
				return Request{}, errors.New("too many keyring roots")
			}
		case []KeyringID:
			for _, id := range value {
				if id == 0 {
					continue
				}
				switch {
				case request.Caller.ThreadKeyring == 0:
					request.Caller.ThreadKeyring = id
				case request.Caller.ProcessKeyring == 0:
					request.Caller.ProcessKeyring = id
				case request.Caller.SessionKeyring == 0:
					request.Caller.SessionKeyring = id
				default:
					return Request{}, errors.New("too many keyring roots")
				}
			}
		case Caller:
			request.Caller = value
		case *Caller:
			if value == nil {
				return Request{}, errors.New("nil caller")
			}
			request.Caller = *value
		case Credentials:
			request.Credentials = value
		case Keyring:
			request.Keyrings = append(request.Keyrings, value)
		case []Keyring:
			request.Keyrings = append(request.Keyrings, value...)
		case KeyringGraph:
			for id, ring := range value {
				if request.Graph == nil {
					request.Graph = make(KeyringGraph)
				}
				if ring.ID == 0 {
					ring.ID = id
				}
				request.Graph[id] = ring
			}
		case map[KeyringID]Keyring:
			if request.Graph == nil {
				request.Graph = make(KeyringGraph, len(value))
			}
			for id, ring := range value {
				if ring.ID == 0 {
					ring.ID = id
				}
				request.Graph[id] = ring
			}
		case map[uint32]Keyring:
			if request.Graph == nil {
				request.Graph = make(KeyringGraph, len(value))
			}
			for id, ring := range value {
				if ring.ID == 0 {
					ring.ID = KeyringID(id)
				}
				request.Graph[KeyringID(id)] = ring
			}
		case map[KeyringID][]KeyringID:
			if request.Adjacency == nil {
				request.Adjacency = make(map[KeyringID][]KeyringID, len(value))
			}
			for id, links := range value {
				request.Adjacency[id] = append(request.Adjacency[id], links...)
			}
		case map[uint32][]KeyringID:
			if request.Adjacency == nil {
				request.Adjacency = make(map[KeyringID][]KeyringID, len(value))
			}
			for id, links := range value {
				request.Adjacency[KeyringID(id)] = append(request.Adjacency[KeyringID(id)], links...)
			}
		case SandboxPolicy:
			request.Sandbox = value
		case *SandboxPolicy:
			if value == nil {
				return Request{}, errors.New("nil sandbox policy")
			}
			request.Sandbox = *value
		case Operation:
			request.Operation = value
		case string:
			operation, err := ParseOperation(value)
			if err != nil {
				return Request{}, err
			}
			request.Operation = operation
		default:
			return Request{}, fmt.Errorf("unsupported access decision argument %T", arg)
		}
	}
	return request, nil
}

func mergeRequest(dst, src Request) Request {
	if src.Key != (Key{}) {
		dst.Key = src.Key
	}
	if src.KeyID != 0 {
		dst.KeyID = src.KeyID
	}
	if src.TargetKeyID != 0 {
		dst.TargetKeyID = src.TargetKeyID
	}
	if src.Caller != (Caller{}) {
		dst.Caller = src.Caller
	}
	if src.Credentials != (Credentials{}) {
		dst.Credentials = src.Credentials
	}
	if src.ThreadKeyring != 0 {
		dst.ThreadKeyring = src.ThreadKeyring
	}
	if src.ProcessKeyring != 0 {
		dst.ProcessKeyring = src.ProcessKeyring
	}
	if src.SessionKeyring != 0 {
		dst.SessionKeyring = src.SessionKeyring
	}
	dst.Keyrings = append(dst.Keyrings, src.Keyrings...)
	dst.Rings = append(dst.Rings, src.Rings...)
	if len(src.Graph) > 0 {
		if dst.Graph == nil {
			dst.Graph = make(KeyringGraph, len(src.Graph))
		}
		for id, ring := range src.Graph {
			dst.Graph[id] = ring
		}
	}
	if len(src.Adjacency) > 0 {
		if dst.Adjacency == nil {
			dst.Adjacency = make(map[KeyringID][]KeyringID, len(src.Adjacency))
		}
		for id, links := range src.Adjacency {
			dst.Adjacency[id] = append(dst.Adjacency[id], links...)
		}
	}
	if src.Operation != "" {
		dst.Operation = src.Operation
	}
	if src.Op != "" {
		dst.Op = src.Op
	}
	if sandboxEnabled(src.Sandbox) {
		dst.Sandbox = src.Sandbox
	}
	if src.Policy != nil {
		dst.Policy = src.Policy
	}
	return dst
}
