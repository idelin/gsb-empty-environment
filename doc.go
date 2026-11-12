// Package taskpkg provides admission control for packet acknowledgments in a
// reliable transport.
//
// The host records every packet number it sends. An acknowledgment is first
// authenticated by the injected Authenticator against the host's current
// connection generation and direction. Every closed interval in the
// acknowledgment must then be covered by packet numbers that were actually
// sent. Validation is atomic: if any interval is invalid, the entire
// acknowledgment is ignored and no frame reference is decremented.
//
// Sent packet numbers are strictly increasing and never wrap within a
// generation. When a configured packet number space is exhausted the host
// starts a new connection with a greater Generation.
package taskpkg
