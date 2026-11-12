// Package eaptls implements the client-side state machine for EAP-TLS 1.3
// inside a Go 802.1X supplicant.
//
// EAP-TLS 1.3 (RFC 8996, section 2.5) requires the peer to send an empty
// EAP-TLS response (the commitment message) after receiving the server's
// final TLS handshake message, and forbids accepting an EAP-Success before
// that point. An EAP method implementation drives this package through its
// callbacks:
//
//   - [State.Begin] loads the original user identity and checks authorization.
//   - [State.HandleRequest] parses EAP-TLS requests and reassembles fragments.
//   - [State.MarkFinalHandshake] (or plaintext Finished auto-detection)
//     records the server's final handshake message.
//   - [State.Commitment] emits the empty commitment response.
//   - [State.HandleSuccess] accepts EAP-Success only in the committed state.
//   - [State.Resume] re-fetches the original user identity and re-runs
//     authorization before a handshake may be resumed.
package eaptls
