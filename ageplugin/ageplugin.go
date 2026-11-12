// Package ageplugin re-exports the host-side age plugin protocol v1 session
// implementation from the root netsec package under an import path dedicated
// to age plugin integrations.
//
// Use this package when integrating the session code into an age encryption
// host; the symbols behave identically to their netsec counterparts.
package ageplugin

import (
	"io"

	"example.org/netsec/q0569"
)

// Re-exported protocol limits and sizes.
const (
	MaxPluginResponse = netsec.MaxPluginResponse
	FileKeySize       = netsec.FileKeySize
)

// Re-exported sentinel errors.
var (
	ErrInvalidPluginName = netsec.ErrInvalidPluginName
	ErrResponseTooLarge  = netsec.ErrResponseTooLarge
	ErrProtocol          = netsec.ErrProtocol
	ErrFileKeySize       = netsec.ErrFileKeySize
	ErrRecipientType     = netsec.ErrRecipientType
)

// Stanza is an alias for netsec.Stanza.
type Stanza = netsec.Stanza

// Session is an alias for netsec.Session.
type Session = netsec.Session

// ParsePluginName re-exports netsec.ParsePluginName.
func ParsePluginName(recipientType string) (string, error) {
	return netsec.ParsePluginName(recipientType)
}

// BinaryName re-exports netsec.BinaryName.
func BinaryName(name string) (string, error) {
	return netsec.BinaryName(name)
}

// NewSession re-exports netsec.NewSession.
func NewSession(reader io.Reader, writer io.Writer, recipientType string) (*Session, error) {
	return netsec.NewSession(reader, writer, recipientType)
}
