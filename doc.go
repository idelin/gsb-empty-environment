// Package netsec provides an importable server-side validator for
// Kubernetes service account tokens. It verifies the signature, issuer,
// audience, expiry and revocation status of each presented token, and
// accepts only tokens whose audience equals the identity the calling
// service declares for itself. Namespace, service account and permission
// generation claims are surfaced in the returned Identity so callers can
// feed them into their authorization decisions.
//
// Projected tokens have their audience and expiry bound by the server
// (Config.Audience and Config.MaxTokenLifetime). Legacy audience-less
// tokens are handled by an explicit migration policy (Config.LegacyPolicy)
// and are rejected by default. Tokens from other clusters and mirrored
// replica tokens are never accepted.
package netsec
