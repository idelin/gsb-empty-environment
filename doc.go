// Package netsec builds and validates the session transcript that a mobile
// wallet's device signs when presenting credentials to a reader.
//
// The session transcript is distinct from the handoff data. The raw handoff
// bytes never go into the transcript; instead the transcript binds the session
// to them through their SHA-256 digest. A valid transcript must contain all
// three of the following, and is rejected if any one is missing:
//
//   - HandoffHash: SHA-256 over the handoff bytes,
//   - ReaderPublicKey: the e-reader session public key,
//   - DevicePublicKey: the device session public key.
//
// Because the device signature covers all three fields, an incomplete
// transcript can never be signed or accepted, which preserves the anti-relay
// and session-binding guarantees of the presentation.
package netsec
