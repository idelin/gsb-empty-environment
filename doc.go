// Package netsec provides fail-closed HTTP bearer-token middleware.
//
// Tokens without a cnf claim follow ordinary bearer-token rules. Tokens with
// cnf are proof-of-possession constrained: cnf.jkt requires a matching DPoP
// proof and cnf["x5t#S256"] (or cnf.x5t) requires a matching TLS client
// certificate. A constrained token with no matching proof or certificate is
// rejected and is never downgraded to a plain bearer token.
package netsec
