package netsec

import (
	"crypto/rand"
	"fmt"
	"io"
	"sync"
)

type session struct {
	hlsChallenge []byte
	hlsComplete  bool
	llsComplete  bool
}

// AccessController makes COSEM access decisions for associated clients.
// Association and authentication state is scoped to one SAP and is replaced
// on re-association.
type AccessController struct {
	mu sync.RWMutex

	publicSAP     uint16
	managementSAP uint16

	hlsSecret        []byte
	hlsVerifier      func(challenge, response []byte) bool
	hlsChallengeSize int
	random           io.Reader
	llsPassword      []byte

	objects  map[ObjectID]struct{}
	sessions map[uint16]*session
}

// NewController creates a controller using the standard SAP assignments and
// an HMAC-SHA256 HLS verifier.
func NewController(hlsSecret []byte) *AccessController {
	controller, err := NewWithConfig(Config{HLSSecret: hlsSecret})
	if err != nil {
		panic(err)
	}
	return controller
}

// New is an alias for NewController.
func New(hlsSecret []byte) *AccessController {
	return NewController(hlsSecret)
}

// NewWithConfig creates an AccessController from cfg.
func NewWithConfig(cfg Config) (*AccessController, error) {
	if cfg.PublicSAP == 0 {
		cfg.PublicSAP = SAPPublic
	}
	if cfg.ManagementSAP == 0 {
		cfg.ManagementSAP = SAPManagement
	}
	if cfg.PublicSAP == cfg.ManagementSAP {
		return nil, fmt.Errorf("%w: public and management SAPs must differ", ErrInvalidAccess)
	}
	if cfg.HLSChallengeSize == 0 {
		cfg.HLSChallengeSize = 16
	}
	if cfg.HLSChallengeSize < 0 {
		return nil, fmt.Errorf("%w: challenge size must be positive", ErrInvalidChallengeConfig)
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}

	controller := newController(cfg)
	return controller, nil
}

// NewAccessController is an alias for NewWithConfig.
func NewAccessController(cfg Config) (*AccessController, error) {
	return NewWithConfig(cfg)
}

func newController(cfg Config) *AccessController {
	objects := make(map[ObjectID]struct{}, len(cfg.RegisteredObjects))
	for _, object := range cfg.RegisteredObjects {
		objects[object] = struct{}{}
	}

	return &AccessController{
		publicSAP:        cfg.PublicSAP,
		managementSAP:    cfg.ManagementSAP,
		hlsSecret:        append([]byte(nil), cfg.HLSSecret...),
		hlsVerifier:      cfg.HLSVerifier,
		hlsChallengeSize: cfg.HLSChallengeSize,
		random:           cfg.Random,
		llsPassword:      append([]byte(nil), cfg.LLSPassword...),
		objects:          objects,
		sessions:         make(map[uint16]*session),
	}
}

// RegisterReadable adds objects that clients are allowed to read. Registering
// the same object more than once is harmless.
func (c *AccessController) RegisterReadable(objects ...ObjectID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, object := range objects {
		c.objects[object] = struct{}{}
	}
}

// RegisterObject is an alias for RegisterReadable.
func (c *AccessController) RegisterObject(object ObjectID) {
	c.RegisterReadable(object)
}

// IsRegistered reports whether an object is in the readable registry.
func (c *AccessController) IsRegistered(object ObjectID) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.objects[object]
	return ok
}

// Associate starts a new association for sap. Any previous association state
// for that SAP, including completed HLS authentication and a pending
// challenge, is discarded.
func (c *AccessController) Associate(sap uint16) error {
	if !c.knownSAP(sap) {
		return ErrUnknownSAP
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[sap] = &session{}
	return nil
}

// Release terminates sap's association and clears all authentication state.
// Releasing a SAP that is not associated is a no-op.
func (c *AccessController) Release(sap uint16) error {
	if !c.knownSAP(sap) {
		return ErrUnknownSAP
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sap)
	return nil
}

// IsAssociated reports whether sap currently has an association.
func (c *AccessController) IsAssociated(sap uint16) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.sessions[sap]
	return ok
}

// State returns a snapshot of the authentication state for sap.
func (c *AccessController) State(sap uint16) AssociationState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	current, ok := c.sessions[sap]
	if !ok {
		return AssociationState{}
	}
	return AssociationState{
		Associated:          true,
		HLSChallengePending: len(current.hlsChallenge) != 0,
		HLSComplete:         current.hlsComplete,
		LLSComplete:         current.llsComplete,
	}
}

// IsHLSComplete reports whether HLS challenge-response has completed for the
// current association.
func (c *AccessController) IsHLSComplete(sap uint16) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	current, ok := c.sessions[sap]
	return ok && current.hlsComplete
}

// IsLLSComplete reports whether a non-empty LLS password has been accepted for
// the current association. LLS does not authorize disconnect-control actions.
func (c *AccessController) IsLLSComplete(sap uint16) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	current, ok := c.sessions[sap]
	return ok && current.llsComplete
}

// Decide returns an access decision, preserving the precise denial reason.
func (c *AccessController) Decide(request AccessRequest) Decision {
	err := c.Authorize(request)
	return Decision{Allowed: err == nil, Reason: err}
}

// Check reports whether request is allowed.
func (c *AccessController) Check(request AccessRequest) bool {
	return c.Authorize(request) == nil
}

// Authorize checks request against the active association, object registry,
// client role and authentication state.
func (c *AccessController) Authorize(request AccessRequest) error {
	if request.Access < AccessRead || request.Access > AccessAction {
		return ErrInvalidAccess
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	current, associated := c.sessions[request.SAP]
	if !associated {
		return ErrNotAssociated
	}

	if request.SAP == c.publicSAP {
		return c.authorizePublic(request, current)
	}
	return c.authorizeManagement(request, current)
}

func (c *AccessController) authorizePublic(request AccessRequest, _ *session) error {
	if request.Access != AccessRead {
		return ErrAccessDenied
	}
	if _, registered := c.objects[request.Object]; !registered {
		return ErrObjectNotRegistered
	}
	return nil
}

func (c *AccessController) authorizeManagement(request AccessRequest, current *session) error {
	switch request.Access {
	case AccessRead:
		if _, registered := c.objects[request.Object]; !registered {
			return ErrObjectNotRegistered
		}
		return nil
	case AccessWrite:
		return ErrAccessDenied
	case AccessAction:
		if request.Object.ClassID != ClassDisconnectControl {
			return ErrActionNotAllowed
		}
		if request.Method != MethodRemoteDisconnect && request.Method != MethodRemoteReconnect {
			return ErrMethodNotAllowed
		}
		if !current.hlsComplete {
			return ErrHLSRequired
		}
		return nil
	default:
		return ErrInvalidAccess
	}
}

func (c *AccessController) knownSAP(sap uint16) bool {
	return sap == c.publicSAP || sap == c.managementSAP
}
