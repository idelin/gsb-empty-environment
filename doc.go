// Package netsec provides the resource-server side of DPoP validation
// (RFC 9449). It verifies a proof JWT, checks htm/htu and freshness, and
// requires the proof JWK thumbprint to equal the access token's cnf.jkt.
package netsec
