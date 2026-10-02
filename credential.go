package tracevault

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Credential is a judicial audit authorization. It is bound to exactly one
// entity and one epoch range, carries a validity window, and is HMAC-signed
// with the regulator key.
type Credential struct {
	EntityID  string
	FromEpoch int64
	ToEpoch   int64
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	Nonce     string
	Signature string
}

func (c *Credential) canonical() string {
	return fmt.Sprintf("%s|%d|%d|%s|%d|%d|%s",
		c.EntityID, c.FromEpoch, c.ToEpoch, c.Issuer,
		c.NotBefore.Unix(), c.NotAfter.Unix(), c.Nonce)
}

func signCredential(regulatorKey []byte, canonical string) string {
	mac := hmac.New(sha256.New, regulatorKey)
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueCredential creates a signed audit credential (regulator side).
func IssueCredential(regulatorKey []byte, entityID string, fromEpoch, toEpoch int64, issuer string, notBefore, notAfter time.Time) (Credential, error) {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return Credential{}, err
	}
	cred := Credential{
		EntityID:  entityID,
		FromEpoch: fromEpoch,
		ToEpoch:   toEpoch,
		Issuer:    issuer,
		NotBefore: notBefore,
		NotAfter:  notAfter,
		Nonce:     hex.EncodeToString(nonceBytes),
	}
	cred.Signature = signCredential(regulatorKey, cred.canonical())
	return cred, nil
}

// Verify checks the signature and validity window of the credential.
func (c *Credential) Verify(regulatorKey []byte, now time.Time) error {
	expected := signCredential(regulatorKey, c.canonical())
	if !hmac.Equal([]byte(expected), []byte(c.Signature)) {
		return errors.New("tracevault: invalid credential signature")
	}
	if now.Before(c.NotBefore) || now.After(c.NotAfter) {
		return errors.New("tracevault: credential outside validity window")
	}
	if c.ToEpoch < c.FromEpoch {
		return errors.New("tracevault: credential epoch range is inverted")
	}
	return nil
}
