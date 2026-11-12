// Package netsec evaluates ZooKeeper-style access control lists for a
// coordination service.
//
// An ACL is fixed on a node when the node is created. The package models the
// schemes that matter for authorization decisions:
//
//   - "world" with the single identity "anyone" matches every connection.
//   - "auth" is only meaningful at node-creation time: it is expanded into
//     every identity the creating connection has authenticated as. A
//     connection without any authenticated identity yields InvalidACL.
//   - "digest" identities have the form "user:Base64(SHA-1(user:password))"
//     and are matched exactly against authenticated digest identities.
//   - "ip" holds an IPv4 (or IPv6) address or CIDR block and matches the
//     client address of the connection.
//
// A configured super digest, once authenticated, bypasses every check.
package netsec

import (
	"crypto/subtle"
	"errors"
	"net"
)

// Permission bits that may be combined on an ACL entry.
const (
	PermRead   uint32 = 1 << 0
	PermWrite  uint32 = 1 << 1
	PermCreate uint32 = 1 << 2
	PermDelete uint32 = 1 << 3
	PermAdmin  uint32 = 1 << 4
	PermAll    uint32 = PermRead | PermWrite | PermCreate | PermDelete | PermAdmin
)

// Supported ACL identity schemes.
const (
	SchemeWorld  = "world"
	SchemeAuth   = "auth"
	SchemeDigest = "digest"
	SchemeIP     = "ip"
)

// WorldAnyone is the only identity defined for the "world" scheme.
const WorldAnyone = "anyone"

var (
	// ErrInvalidACL is returned when an ACL is empty, references the "auth"
	// scheme from a connection with no authenticated identity, or is otherwise
	// malformed.
	ErrInvalidACL = errors.New("netsec: invalid ACL")
	// ErrAccessDenied is returned by Check when no ACL entry grants the
	// requested permission.
	ErrAccessDenied = errors.New("netsec: access denied")
	// ErrUnsupportedScheme is returned when a connection authenticates with a
	// scheme the evaluator does not understand.
	ErrUnsupportedScheme = errors.New("netsec: unsupported authentication scheme")
	// ErrMalformedIdentity is returned when a digest credential is not of the
	// form "user:password".
	ErrMalformedIdentity = errors.New("netsec: malformed identity")
)

// ID identifies an ACL principal as a scheme/identity pair.
type ID struct {
	Scheme string
	ID     string
}

// ACL is a single access control entry binding a permission bitmask to an ID.
type ACL struct {
	Perms uint32
	ID    ID
}

// Authorizer evaluates ACLs for a configured deployment. The zero value is
// usable and simply disables the super digest.
type Authorizer struct {
	// superDigest is the full "user:Base64(SHA-1(user:password))" identity
	// whose holder bypasses every authorization check. Empty means no super
	// user is configured.
	superDigest string
}

// NewAuthorizer builds an Authorizer. superDigest is the result of DigestID
// for the chosen super principal; pass an empty string to disable it.
func NewAuthorizer(superDigest string) *Authorizer {
	return &Authorizer{superDigest: superDigest}
}

// Connection models the authenticated state of a single client connection.
type Connection struct {
	authorizer *Authorizer

	// clientIP is the remote address the connection originates from. It may be
	// nil, in which case "ip" entries never match.
	clientIP net.IP

	// identities are the fully-formed principal IDs (e.g. digest IDs) the
	// connection has authenticated as.
	identities []ID

	// super reports whether the connection authenticated as the configured
	// super principal.
	super bool
}

// NewConnection opens an evaluator for a client. clientIP may be nil when the
// transport does not expose a remote address.
func (a *Authorizer) NewConnection(clientIP net.IP) *Connection {
	return &Connection{authorizer: a, clientIP: clientIP}
}

// AddAuth authenticates the connection with the given scheme. Only "digest"
// credentials of the form "user:password" are accepted; the stored identity is
// the derived "user:Base64(SHA-1(user:password))" digest. Authenticating as the
// configured super principal marks the connection as super.
func (c *Connection) AddAuth(scheme, credential string) error {
	if scheme != SchemeDigest {
		return ErrUnsupportedScheme
	}

	sep := -1
	for i := 0; i < len(credential); i++ {
		if credential[i] == ':' {
			sep = i
			break
		}
	}
	if sep <= 0 || sep == len(credential)-1 {
		return ErrMalformedIdentity
	}

	digestID := DigestID(credential[:sep], credential[sep+1:])
	c.identities = append(c.identities, ID{Scheme: SchemeDigest, ID: digestID})

	if c.authorizer.superDigest != "" &&
		subtle.ConstantTimeCompare([]byte(digestID), []byte(c.authorizer.superDigest)) == 1 {
		c.super = true
	}
	return nil
}

// IsSuper reports whether the connection holds the configured super identity.
func (c *Connection) IsSuper() bool { return c.super }

// ExpandCreateACL resolves the ACL attached to a create request into the ACL
// that must be stored on the new node.
//
// An empty list is InvalidACL. Every "auth" entry is expanded into one entry
// per authenticated identity (carrying the original permission bits); a
// connection with no authenticated identity cannot use "auth" and produces
// InvalidACL. Other entries are passed through unchanged.
func (c *Connection) ExpandCreateACL(acls []ACL) ([]ACL, error) {
	if len(acls) == 0 {
		return nil, ErrInvalidACL
	}

	expanded := make([]ACL, 0, len(acls))
	for _, entry := range acls {
		if entry.ID.Scheme != SchemeAuth {
			expanded = append(expanded, entry)
			continue
		}
		if len(c.identities) == 0 {
			return nil, ErrInvalidACL
		}
		for _, identity := range c.identities {
			expanded = append(expanded, ACL{Perms: entry.Perms, ID: identity})
		}
	}
	return expanded, nil
}

// Authorize reports whether the connection holds perm against a node carrying
// acls. The super principal always succeeds; otherwise an empty ACL denies by
// default and every matching entry contributes its permission bits.
func (c *Connection) Authorize(acls []ACL, perm uint32) bool {
	if c.super {
		return true
	}
	if len(acls) == 0 {
		return false
	}

	for _, entry := range acls {
		if entry.Perms&perm != perm {
			continue
		}
		if c.matches(entry.ID) {
			return true
		}
	}
	return false
}

// Check is the error-returning form of Authorize.
func (c *Connection) Check(acls []ACL, perm uint32) error {
	if c.Authorize(acls, perm) {
		return nil
	}
	return ErrAccessDenied
}

// CanRead reports whether the connection may read data or list children.
func (c *Connection) CanRead(acls []ACL) bool { return c.Authorize(acls, PermRead) }

// CanWrite reports whether the connection may set node data.
func (c *Connection) CanWrite(acls []ACL) bool { return c.Authorize(acls, PermWrite) }

// CanAdmin reports whether the connection may change the node's ACL.
func (c *Connection) CanAdmin(acls []ACL) bool { return c.Authorize(acls, PermAdmin) }

// CanCreate reports whether a child may be created under parentACL, i.e. the
// connection holds CREATE on the parent node.
func (c *Connection) CanCreate(parentACL []ACL) bool {
	return c.Authorize(parentACL, PermCreate)
}

// CanDelete reports whether a child may be deleted under parentACL. Deletion
// depends only on the DELETE permission of the parent node; the child's own ACL
// is never consulted.
func (c *Connection) CanDelete(parentACL []ACL) bool {
	return c.Authorize(parentACL, PermDelete)
}

// CanSetACL reports whether the connection may overwrite nodeACL, which
// requires ADMIN on that node.
func (c *Connection) CanSetACL(nodeACL []ACL) bool {
	return c.Authorize(nodeACL, PermAdmin)
}

// matches reports whether id applies to this connection.
func (c *Connection) matches(id ID) bool {
	switch id.Scheme {
	case SchemeWorld:
		// "anyone" is the only defined world identity and matches everyone.
		return id.ID == WorldAnyone
	case SchemeIP:
		return matchCIDR(id.ID, c.clientIP)
	case SchemeAuth:
		// A residual "auth" entry matches any authenticated connection, but
		// never an anonymous one.
		return len(c.identities) > 0
	default:
		for _, identity := range c.identities {
			if identity == id {
				return true
			}
		}
		return false
	}
}
