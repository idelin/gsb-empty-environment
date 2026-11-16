package netsec

import (
	"crypto/hmac"
	"encoding/binary"
	"fmt"
	"sync"
)

type sessionState uint8

const (
	stateOpened sessionState = iota
	stateRAKP1
	stateRAKP2
	stateAuthenticated
	stateFailed
)

// RAKP1Request is the client's first RAKP message. The client random must be
// 16 bytes; an empty value is generated for convenience in simulations.
type RAKP1Request struct {
	Username     string
	ClientRandom []byte
	Role         uint8
	Privilege    uint8
}

// RAKP2Response is the server's RAKP2 response. Its wire form has the same
// fields and fixed algorithm-dependent HMAC length whether or not the username
// exists.
type RAKP2Response struct {
	Username     string
	ServerRandom []byte
	BMCGUID      []byte
	AuthCode     []byte
}

// Bytes returns a deterministic length-prefixed wire representation. Callers
// can compare lengths to verify that unknown-user and bad-password responses
// are identical in shape.
func (r *RAKP2Response) Bytes() []byte {
	size := 2 + len(r.Username) + 2 + len(r.ServerRandom) + 2 + len(r.BMCGUID) + 2 + len(r.AuthCode)
	out := make([]byte, 0, size)
	out = appendField(out, []byte(r.Username))
	out = appendField(out, r.ServerRandom)
	out = appendField(out, r.BMCGUID)
	out = appendField(out, r.AuthCode)
	return out
}

// Len returns the length of Bytes.
func (r *RAKP2Response) Len() int { return len(r.Bytes()) }

func appendField(dst, field []byte) []byte {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(field)))
	dst = append(dst, length[:]...)
	return append(dst, field...)
}

// Session is one RMCP+ session admission state machine.
type Session struct {
	admission *Admission
	source    string
	suite     Suite

	mu    sync.Mutex
	state sessionState

	username     string
	clientRandom []byte
	role         uint8
	privilege    uint8

	serverRandom []byte
	serverCode   []byte

	userKnown          bool
	expectedClientCode []byte
}

// OpenSession applies source throttling and cipher suite admission before
// returning a session that can run RAKP.
func (a *Admission) OpenSession(source string, suiteNumber uint8) (*Session, error) {
	source = normalizeSource(source)
	if a.throttle.Blocked(source, a.clock.Now()) {
		return nil, fmt.Errorf("%w: %s", ErrThrottled, source)
	}
	suite, err := a.AdmitSuite(suiteNumber)
	if err != nil {
		return nil, err
	}
	return &Session{
		admission: a,
		source:    source,
		suite:     suite,
		state:     stateOpened,
	}, nil
}

// NewSession is an alias for OpenSession.
func (a *Admission) NewSession(source string, suiteNumber uint8) (*Session, error) {
	return a.OpenSession(source, suiteNumber)
}

// RAKP1 starts the exchange. Empty usernames are treated as anonymous and
// rejected before a challenge is built.
func (s *Session) RAKP1(request RAKP1Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateOpened {
		return fmt.Errorf("%w: RAKP1 in state %d", ErrInvalidRequest, s.state)
	}
	if request.Username == "" {
		s.state = stateFailed
		return fmt.Errorf("%w: empty username", ErrAnonymous)
	}

	clientRandom := request.ClientRandom
	if len(clientRandom) == 0 {
		generated, err := s.admission.randomBytes(16)
		if err != nil {
			return fmt.Errorf("netsec: generate client random: %w", err)
		}
		clientRandom = generated
	} else if len(clientRandom) != 16 {
		return fmt.Errorf("%w: client random must be 16 bytes", ErrInvalidRequest)
	}

	s.username = request.Username
	s.clientRandom = append([]byte(nil), clientRandom...)
	s.role = request.Role
	s.privilege = request.Privilege
	s.state = stateRAKP1
	return nil
}

// RAKP2 builds the server challenge. An unknown username uses a per-process
// dummy secret and therefore receives the same response length and timing
// bucket as a known username with a bad password. No user-existence error is
// returned here.
func (s *Session) RAKP2() (*RAKP2Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateRAKP1 {
		return nil, fmt.Errorf("%w: RAKP2 in state %d", ErrInvalidRequest, s.state)
	}

	start := s.admission.clock.Now()
	secret, known := s.admission.lookupUser(s.username)
	if !known {
		secret = s.admission.dummySecret
	}

	serverRandom, err := s.admission.randomBytes(16)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate server random: %w", err)
	}
	s.serverRandom = serverRandom
	s.userKnown = known

	serverCode, err := authCode(s.suite.Auth, secret, s.rakp2PayloadLocked())
	if err != nil {
		return nil, err
	}
	s.serverCode = serverCode
	s.expectedClientCode, err = authCode(s.suite.Auth, secret, s.rakp3PayloadLocked())
	if err != nil {
		return nil, err
	}

	response := &RAKP2Response{
		Username:     s.username,
		ServerRandom: append([]byte(nil), s.serverRandom...),
		BMCGUID:      append([]byte(nil), s.admission.bmcGUID...),
		AuthCode:     append([]byte(nil), serverCode...),
	}
	s.state = stateRAKP2
	s.admission.padAuthTiming(start)
	return response, nil
}

// RAKP3 verifies the client's authentication code. Unknown users and bad
// passwords produce the same error and the same timing bucket, and a code that
// happens to match the dummy secret still cannot authenticate an unknown
// username.
func (s *Session) RAKP3(clientAuthCode []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateRAKP2 {
		return fmt.Errorf("%w: RAKP3 in state %d", ErrInvalidRequest, s.state)
	}

	start := s.admission.clock.Now()
	matched := hmac.Equal(clientAuthCode, s.expectedClientCode)
	ok := s.userKnown && matched
	s.admission.padAuthTiming(start)

	if !ok {
		s.state = stateFailed
		s.admission.throttle.RecordFailure(s.source, s.admission.clock.Now())
		return ErrAuthenticationFailed
	}
	s.state = stateAuthenticated
	return nil
}

// ClientAuthCode computes the RAKP3 code a client with the supplied password
// would send. It is a simulation helper and is never sent by the BMC.
func (s *Session) ClientAuthCode(password []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateRAKP2 {
		return nil
	}
	code, err := authCode(s.suite.Auth, password, s.rakp3PayloadLocked())
	if err != nil {
		return nil
	}
	return code
}

// RAKP3Transcript returns a copy of the data covered by the RAKP3 code.
func (s *Session) RAKP3Transcript() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateRAKP2 {
		return nil
	}
	return append([]byte(nil), s.rakp3PayloadLocked()...)
}

// Suite returns the admitted suite.
func (s *Session) Suite() Suite { return s.suite }

// Source returns the normalised source key used for throttling.
func (s *Session) Source() string { return s.source }

// Username returns the username supplied in RAKP1.
func (s *Session) Username() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.username
}

func (s *Session) rakp2PayloadLocked() []byte {
	var out []byte
	out = appendField(out, []byte(s.username))
	out = append(out, s.role, s.privilege)
	out = appendField(out, s.clientRandom)
	out = appendField(out, s.serverRandom)
	return out
}

func (s *Session) rakp3PayloadLocked() []byte {
	out := s.rakp2PayloadLocked()
	out = appendField(out, s.serverCode)
	return out
}

func authCode(algorithm Algorithm, password, payload []byte) ([]byte, error) {
	newHash, err := authHash(algorithm)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(newHash, password)
	_, _ = mac.Write(payload)
	return mac.Sum(nil), nil
}

// AuthRequest is a complete simulated login.
type AuthRequest struct {
	Source   string
	Suite    uint8
	Username string
	Password []byte
}

// AuthResult is returned by Authenticate and Login.
type AuthResult struct {
	Session   *Session
	Challenge *RAKP2Response
}

// Authenticate runs a complete simulated RAKP login. It rejects anonymous
// logins, applies suite admission and throttling, and returns a nil result for
// admission failures. On authentication failure the result still contains the
// RAKP2 challenge so callers can inspect its shape; the error is always
// ErrAuthenticationFailed for both an unknown user and a bad password.
func (a *Admission) Authenticate(request AuthRequest) (*AuthResult, error) {
	session, err := a.OpenSession(request.Source, request.Suite)
	if err != nil {
		return nil, err
	}
	if request.Username == "" && len(request.Password) == 0 {
		return nil, fmt.Errorf("%w: empty username and password", ErrAnonymous)
	}
	if err := session.RAKP1(RAKP1Request{Username: request.Username}); err != nil {
		return nil, err
	}
	challenge, err := session.RAKP2()
	if err != nil {
		return nil, err
	}
	result := &AuthResult{Session: session, Challenge: challenge}
	clientCode := session.ClientAuthCode(request.Password)
	if err := session.RAKP3(clientCode); err != nil {
		return result, err
	}
	return result, nil
}

// Login is an alias for Authenticate.
func (a *Admission) Login(source string, suite uint8, username string, password []byte) (*AuthResult, error) {
	return a.Authenticate(AuthRequest{
		Source:   source,
		Suite:    suite,
		Username: username,
		Password: password,
	})
}
