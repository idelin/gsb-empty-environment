package pqpin

import (
	"errors"
	"fmt"
)

var errEmptyServerID = errors.New("pqpin: observation has empty ServerID")

// Reason identifies why a pinned server was treated as downgraded.
type Reason string

const (
	// ReasonServerHello means the (final) ServerHello selected a non-hybrid
	// key exchange group while an unexpired pin exists.
	ReasonServerHello Reason = "non-hybrid group selected in ServerHello"
	// ReasonHRR means a HelloRetryRequest selected a non-hybrid key exchange
	// group while an unexpired pin exists.
	ReasonHRR Reason = "non-hybrid group selected in HelloRetryRequest"
	// ReasonResume means the resumed session was originally established
	// without a hybrid key exchange while an unexpired pin exists.
	ReasonResume Reason = "resumed session originated from a non-hybrid handshake"
)

// DowngradeError reports that an online downgrade from a hybrid key
// exchange was detected for a pinned server.
type DowngradeError struct {
	// ServerID is the pinned endpoint, normally host:port.
	ServerID string
	// Reason is the specific check that failed.
	Reason Reason
	// SelectedGroup is the key exchange group offered by the server when
	// Reason is ReasonServerHello or ReasonHRR. It is zero for the
	// resumption and expiry cases.
	SelectedGroup uint16
	// ExpiresAt is the end of the pin lifetime, zero when not applicable.
	ExpiresAt int64
}

func (e *DowngradeError) Error() string {
	switch e.Reason {
	case ReasonServerHello, ReasonHRR:
		return fmt.Sprintf("pqpin: downgrade detected for %q: %s (selected group %d)",
			e.ServerID, e.Reason, e.SelectedGroup)
	default:
		return fmt.Sprintf("pqpin: downgrade detected for %q: %s", e.ServerID, e.Reason)
	}
}

// AsDowngrade returns the *DowngradeError inside err, if any.
func AsDowngrade(err error) (*DowngradeError, bool) {
	var de *DowngradeError
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}
