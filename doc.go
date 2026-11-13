// Package netsec validates WebAuthn authenticator-data flags for a relying
// party. It records backup eligibility at registration and checks every later
// assertion for reserved bits, backup-state transitions, credential-data
// presence, user presence/verification, and high-risk requirements.
package netsec
