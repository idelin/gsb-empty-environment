package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
)

// HLSResponse computes the default HMAC-SHA256 client response for a challenge.
func HLSResponse(secret, challenge []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("COSEM-HLS"))
	mac.Write(challenge)
	return mac.Sum(nil)
}

// ComputeHLSResponse is an alias for HLSResponse.
func ComputeHLSResponse(secret, challenge []byte) []byte {
	return HLSResponse(secret, challenge)
}

// StartHLS creates a fresh one-use challenge for the management SAP's current
// association. Starting a challenge clears any earlier HLS completion.
func (c *AccessController) StartHLS(sap uint16) ([]byte, error) {
	if sap != c.managementSAP {
		return nil, ErrHLSNotAllowed
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current, associated := c.sessions[sap]
	if !associated {
		return nil, ErrNotAssociated
	}

	challenge := make([]byte, c.hlsChallengeSize)
	if _, err := io.ReadFull(c.random, challenge); err != nil {
		return nil, fmt.Errorf("generate HLS challenge: %w", err)
	}

	current.hlsChallenge = append([]byte(nil), challenge...)
	current.hlsComplete = false
	current.llsComplete = false

	return append([]byte(nil), challenge...), nil
}

// CompleteHLS verifies a response against the challenge most recently issued
// for the management SAP. A challenge can be used at most once.
func (c *AccessController) CompleteHLS(sap uint16, response []byte) error {
	if sap != c.managementSAP {
		return ErrHLSNotAllowed
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current, associated := c.sessions[sap]
	if !associated {
		return ErrNotAssociated
	}
	if len(current.hlsChallenge) == 0 {
		return ErrHLSChallengeRequired
	}

	challenge := current.hlsChallenge
	current.hlsChallenge = nil

	if !c.verifyHLS(challenge, response) {
		current.hlsComplete = false
		return ErrHLSResponseInvalid
	}

	current.hlsComplete = true
	return nil
}

// AuthenticateHLS is an alias for CompleteHLS.
func (c *AccessController) AuthenticateHLS(sap uint16, response []byte) error {
	return c.CompleteHLS(sap, response)
}

// AuthenticateLLS validates a low-level security password for the current
// management association. Empty passwords are always rejected. A successful
// LLS authentication does not satisfy the HLS requirement for class 70 actions.
func (c *AccessController) AuthenticateLLS(sap uint16, password []byte) error {
	if sap != c.managementSAP {
		return ErrLLSNotAllowed
	}
	if len(password) == 0 {
		c.mu.Lock()
		defer c.mu.Unlock()
		if current, associated := c.sessions[sap]; associated {
			current.llsComplete = false
		}
		return ErrEmptyPassword
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current, associated := c.sessions[sap]
	if !associated {
		return ErrNotAssociated
	}
	if len(c.llsPassword) == 0 ||
		subtle.ConstantTimeCompare(password, c.llsPassword) != 1 {
		current.llsComplete = false
		return ErrInvalidPassword
	}

	current.llsComplete = true
	return nil
}

// AssociateLLS starts a new management association and immediately performs
// LLS authentication.
func (c *AccessController) AssociateLLS(sap uint16, password []byte) error {
	if sap != c.managementSAP {
		return ErrLLSNotAllowed
	}
	if err := c.Associate(sap); err != nil {
		return err
	}
	return c.AuthenticateLLS(sap, password)
}

func (c *AccessController) verifyHLS(challenge, response []byte) bool {
	if c.hlsVerifier != nil {
		return c.hlsVerifier(challenge, response)
	}
	if len(c.hlsSecret) == 0 {
		return false
	}

	expected := HLSResponse(c.hlsSecret, challenge)
	return subtle.ConstantTimeCompare(expected, response) == 1
}
