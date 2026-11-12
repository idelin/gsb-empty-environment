// Package netsec implements RADIUS packet parsing and Message-Authenticator
// validation for UDP servers.
//
// Access-Requests must carry a 16-byte Message-Authenticator (attribute type
// 80) as their first attribute. Responses are authenticated with the
// Access-Request authenticator, as required by RFC 3579. Both checks use
// HMAC-MD5 with the RADIUS shared secret.
package netsec
