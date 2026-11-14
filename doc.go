// Package netsec provides authenticated, encrypted storage for infrastructure
// service state.
//
// State payloads, the version record, the exclusive lock, and backup copies
// are all sealed with AES-256-GCM. Envelopes are bound to a workspace, an
// account, a key generation, the artifact role, and the artifact sequence.
// Reads are refused unless the authenticated caller belongs to the workspace
// and account that own the store. Plaintext export is denied unless an
// ExportAuthorizer explicitly approves the request.
//
// The package deliberately has no dependency on a particular cloud provider.
// Services supply a KeyProvider backed by their KMS, secret manager, or an
// in-memory keyring for tests. Key material must be kept outside the
// team-readable workspace directory.
package netsec
