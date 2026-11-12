package netsec

import (
	"strings"
	"time"
)

// Key identifies a pin by the hostname and port used for the connection.
type Key struct {
	Host string
	Port int
}

// NewKey returns a canonical key for host and port.
func NewKey(host string, port int) Key {
	return Key{Host: canonicalHost(host), Port: port}
}

// Pin records the first successful sighting of a certificate for a host and
// port, along with the certificate's expiry time.
type Pin struct {
	Host        string
	Port        int
	Fingerprint Fingerprint
	FirstSeen   time.Time
	NotAfter    time.Time
}

// Key returns the host/port key for the pin.
func (p Pin) Key() Key {
	return NewKey(p.Host, p.Port)
}

// ExpiresAt returns the recorded certificate expiry time.
func (p Pin) ExpiresAt() time.Time {
	return p.NotAfter
}

// ExpiredAt reports whether the recorded certificate has expired at now.
// The NotAfter instant itself counts as expired.
func (p Pin) ExpiredAt(now time.Time) bool {
	return !now.Before(p.NotAfter)
}

func canonicalHost(host string) string {
	host = strings.TrimSpace(host)
	for strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	return strings.ToLower(host)
}

func validPort(port int) bool {
	return port >= 0 && port <= 65535
}
