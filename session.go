package wireguard

import (
	"crypto/subtle"
	"time"
)

const (
	// RekeyAfterMessages triggers a proactive handshake while the old key
	// remains usable for sending until RejectAfterMessages.
	RekeyAfterMessages uint64 = 1 << 60
	// RejectAfterMessages is the hard send/receive limit for a session.
	RejectAfterMessages uint64 = (1 << 64) - (1 << 13) - 1
	// RekeyAfterTime triggers a proactive handshake after two minutes.
	RekeyAfterTime = 120 * time.Second
	// RejectAfterTime destroys a session after three minutes.
	RejectAfterTime = 180 * time.Second
	// CookieRefreshTime is the lifetime of a local cookie secret.
	CookieRefreshTime = 120 * time.Second
)

// Limits contains the four hard session bounds plus the cookie rotation
// interval. Zero fields are replaced with protocol defaults.
type Limits struct {
	RekeyAfterMessages  uint64
	RejectAfterMessages uint64
	RekeyAfterTime      time.Duration
	RejectAfterTime     time.Duration
	CookieRefreshTime   time.Duration
}

// DefaultLimits returns the WireGuard protocol defaults.
func DefaultLimits() Limits {
	return Limits{
		RekeyAfterMessages:  RekeyAfterMessages,
		RejectAfterMessages: RejectAfterMessages,
		RekeyAfterTime:      RekeyAfterTime,
		RejectAfterTime:     RejectAfterTime,
		CookieRefreshTime:   CookieRefreshTime,
	}
}

func (l Limits) normalized() Limits {
	if l.RekeyAfterMessages == 0 {
		l.RekeyAfterMessages = RekeyAfterMessages
	}
	if l.RejectAfterMessages == 0 {
		l.RejectAfterMessages = RejectAfterMessages
	}
	if l.RejectAfterTime <= 0 {
		l.RejectAfterTime = RejectAfterTime
	}
	if l.RekeyAfterTime <= 0 {
		l.RekeyAfterTime = RekeyAfterTime
	}
	if l.CookieRefreshTime <= 0 {
		l.CookieRefreshTime = CookieRefreshTime
	}
	return l
}

type session struct {
	localIndex  uint32
	remoteIndex uint32
	sendKey     [32]byte
	recvKey     [32]byte
	sendCounter uint64
	replay      ReplayWindow
	createdAt   time.Time
	initiator   bool
}

func (s *session) rejectExpired(now time.Time, limits Limits) bool {
	if s.sendCounter >= limits.RejectAfterMessages {
		return true
	}
	if !now.Before(s.createdAt) && now.Sub(s.createdAt) >= limits.RejectAfterTime {
		return true
	}
	return false
}

func (s *session) rekeyNeeded(now time.Time, limits Limits) bool {
	if s.sendCounter >= limits.RekeyAfterMessages {
		return true
	}
	if !now.Before(s.createdAt) && now.Sub(s.createdAt) >= limits.RekeyAfterTime {
		return true
	}
	return false
}

func (s *session) destroy() {
	subtle.XORBytes(s.sendKey[:], s.sendKey[:], zero32[:])
	subtle.XORBytes(s.recvKey[:], s.recvKey[:], zero32[:])
	s.sendCounter = 0
	s.replay.Reset()
	s.localIndex = 0
	s.remoteIndex = 0
	s.createdAt = time.Time{}
}

var zero32 [32]byte

// SessionInfo is an immutable snapshot of one key slot.
type SessionInfo struct {
	LocalIndex  uint32
	RemoteIndex uint32
	CreatedAt   time.Time
	SendCounter uint64
	Initiator   bool
}

// SessionSnapshot reports which key rotation slots are populated.
type SessionSnapshot struct {
	Current  *SessionInfo
	Previous *SessionInfo
	Next     *SessionInfo
}

func infoOf(s *session) *SessionInfo {
	if s == nil {
		return nil
	}
	return &SessionInfo{
		LocalIndex:  s.localIndex,
		RemoteIndex: s.remoteIndex,
		CreatedAt:   s.createdAt,
		SendCounter: s.sendCounter,
		Initiator:   s.initiator,
	}
}
