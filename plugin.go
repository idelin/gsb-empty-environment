package netsec

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Plugin protocol constants fixed by the age plugin protocol v1.
const (
	pluginProtocolVersion = "v1"

	// MaxPluginResponse is the maximum number of wire bytes a single plugin
	// response (the stanzas returned for one command) may occupy.
	MaxPluginResponse = 64 * 1024

	// FileKeySize is the only accepted length of an age file key.
	FileKeySize = 16
)

// pluginNameRE mirrors the age reference implementation: a plugin name may
// contain only lowercase ASCII letters, digits and hyphens and must not be
// empty. This is the security boundary that prevents plugin names such as
// "../../bin/sh" or "/usr/bin/evil" from being turned into executable paths.
var pluginNameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// Sentinel errors returned by the session package. Callers can use errors.Is
// to distinguish validation failures from protocol violations without parsing
// error strings.
var (
	// ErrInvalidPluginName is returned when a recipient stanza type cannot be
	// parsed into an allowed plugin name.
	ErrInvalidPluginName = errors.New("netsec: invalid age plugin name")

	// ErrResponseTooLarge is returned when a single plugin response exceeds
	// MaxPluginResponse wire bytes.
	ErrResponseTooLarge = errors.New("netsec: plugin response exceeds 64KiB")

	// ErrProtocol is returned when the plugin violates the wire protocol.
	ErrProtocol = errors.New("netsec: age plugin protocol error")

	// ErrFileKeySize is returned when WrapFileKey is handed a file key that is
	// not FileKeySize bytes long.
	ErrFileKeySize = errors.New("netsec: age file key must be 16 bytes")

	// ErrRecipientType is returned when a wrap-file-key result stanza does not
	// declare the recipient type the host asked the plugin to wrap for.
	ErrRecipientType = errors.New("netsec: plugin returned stanza with unexpected recipient type")
)

// Stanza is an age protocol stanza. Type and Args are the header fields; Body
// is the raw, already base64-decoded payload.
type Stanza struct {
	Type string
	Args []string
	Body []byte
}

// ParsePluginName extracts the plugin name from an age recipient stanza type
// of the form "AGE-PLUGIN-<name>-1". The middle component must be non-empty
// and consist solely of lowercase ASCII letters, digits and hyphens. The
// returned name is the short plugin name; combine it with [BinaryName] to
// obtain the executable name.
func ParsePluginName(recipientType string) (string, error) {
	const prefix, suffix = "AGE-PLUGIN-", "-1"
	if !strings.HasPrefix(recipientType, prefix) || !strings.HasSuffix(recipientType, suffix) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPluginName, recipientType)
	}
	name := recipientType[len(prefix) : len(recipientType)-len(suffix)]
	// The stanza type is uppercase on the wire; the plugin identity itself is
	// the lowercased middle component.
	name = strings.ToLower(name)
	if !pluginNameRE.MatchString(name) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPluginName, recipientType)
	}
	return name, nil
}

// BinaryName maps a validated short plugin name to the age plugin executable
// name "age-plugin-<name>". It returns an error for names that would not pass
// [ParsePluginName], so the result can never contain path separators.
func BinaryName(name string) (string, error) {
	if !pluginNameRE.MatchString(name) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPluginName, name)
	}
	return "age-plugin-" + name, nil
}

// Session is one host-side age plugin protocol v1 session. A Session is bound
// to a single plugin and recipient type and communicates only through the
// io.Reader and io.Writer supplied to [NewSession]; it never launches
// processes or touches the file system.
type Session struct {
	reader     *stanzaReader
	writer     *stanzaWriter
	rawWriter  io.Writer
	pluginName string
	binaryName string
	recipient  string
	recipients [][]string
	sentAdds   bool
}

// NewSession performs the protocol handshake. recipientType is the full
// AGE-PLUGIN-<name>-1 stanza type taken from an age recipient; the plugin name
// embedded in it is validated and mapped to its executable name. The stream
// pair typically wraps a plugin process's stdout and stdin.
func NewSession(in io.Reader, out io.Writer, recipientType string) (*Session, error) {
	name, err := ParsePluginName(recipientType)
	if err != nil {
		return nil, err
	}
	binary, err := BinaryName(name)
	if err != nil {
		return nil, err
	}

	s := &Session{
		reader:     newStanzaReader(in),
		writer:     &stanzaWriter{w: out},
		rawWriter:  out,
		pluginName: name,
		binaryName: binary,
		recipient:  recipientType,
	}

	if err := s.handshake(); err != nil {
		return nil, err
	}
	return s, nil
}

// PluginName returns the validated short plugin name.
func (s *Session) PluginName() string { return s.pluginName }

// BinaryName returns the validated "age-plugin-<name>" executable name. It
// contains only lowercase letters, digits and hyphens after the fixed prefix,
// so it can be looked up on PATH without path traversal risk.
func (s *Session) BinaryName() string { return s.binaryName }

// RecipientType returns the full AGE-PLUGIN-<name>-1 recipient stanza type the
// session was created for.
func (s *Session) RecipientType() string { return s.recipient }

// AddRecipient registers a recipient, given as the full arg list of its
// AGE-PLUGIN-<name>-1 header stanza. Recipients are sent to the plugin with
// the first [Session.WrapFileKey] call.
func (s *Session) AddRecipient(recipient []string) {
	args := append([]string(nil), recipient...)
	s.recipients = append(s.recipients, args)
}

// handshake consumes the plugin's initial greet stanza and answers with the
// host greet selecting protocol v1. Stanzas of unknown type before the greet
// are ignored per the protocol's extension rule.
func (s *Session) handshake() error {
	s.reader.resetLimit(MaxPluginResponse)
	for {
		stanza, err := s.reader.ReadStanza()
		if err != nil {
			return err
		}
		if stanza.Type == "error" {
			return pluginError(stanza)
		}
		if stanza.Type == "greet" {
			if len(stanza.Args) != 1 || stanza.Args[0] != pluginProtocolVersion {
				return fmt.Errorf("%w: unsupported plugin protocol version %q", ErrProtocol, stanza.Args)
			}
			return s.writer.WriteStanza(&Stanza{Type: "greet", Args: []string{pluginProtocolVersion}})
		}
		// Unknown stanzas are ignored so hostile plugins cannot drive host
		// behavior through unsupported commands.
	}
}

// sendAddRecipients flushes every registered recipient to the plugin.
func (s *Session) sendAddRecipients() error {
	for _, recipient := range s.recipients {
		if err := s.writer.WriteStanza(&Stanza{Type: "add-recipient", Args: recipient}); err != nil {
			return err
		}
	}
	s.sentAdds = true
	return s.flush()
}

// WrapFileKey runs the recipient-side wrap-file-key command. fileKey must be
// exactly FileKeySize bytes. The plugin's recipient-stanza results are
// validated: every result's first argument must equal the recipient type
// declared for this session, and the complete response must fit into
// MaxPluginResponse wire bytes. Unknown stanzas within the response are
// ignored.
func (s *Session) WrapFileKey(fileKey []byte) ([]*Stanza, error) {
	if len(fileKey) != FileKeySize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrFileKeySize, len(fileKey))
	}

	if !s.sentAdds {
		if err := s.sendAddRecipients(); err != nil {
			return nil, err
		}
	}

	command := &Stanza{
		Type: "wrap-file-key",
		Body: append([]byte(nil), fileKey...),
	}
	if err := s.writer.WriteStanza(command); err != nil {
		return nil, err
	}
	if err := s.writer.WriteStanza(&Stanza{Type: "done"}); err != nil {
		return nil, err
	}
	if err := s.flush(); err != nil {
		return nil, err
	}

	s.reader.resetLimit(MaxPluginResponse)
	var results []*Stanza
	for {
		stanza, err := s.reader.ReadStanza()
		if err != nil {
			return nil, err
		}
		switch stanza.Type {
		case "recipient-stanza":
			if len(stanza.Args) == 0 || stanza.Args[0] != s.recipient {
				return nil, fmt.Errorf("%w: got %q want %q", ErrRecipientType, firstArg(stanza), s.recipient)
			}
			results = append(results, stanza)
		case "done":
			if len(results) == 0 {
				return nil, fmt.Errorf("%w: plugin returned no recipient stanza", ErrProtocol)
			}
			return results, nil
		case "error":
			return nil, pluginError(stanza)
		default:
			// Ignore unknown commands/stanzas as required by the protocol.
		}
	}
}

func (s *Session) flush() error {
	if buffered, ok := s.rawWriter.(interface{ Flush() error }); ok {
		return buffered.Flush()
	}
	return nil
}

func firstArg(stanza *Stanza) string {
	if len(stanza.Args) == 0 {
		return "<none>"
	}
	return stanza.Args[0]
}

// pluginError converts a plugin error stanza into a Go error, annotating it
// with the protocol sentinel so callers can detect plugin-side failures.
func pluginError(stanza *Stanza) error {
	message := strings.TrimSpace(string(stanza.Body))
	if message == "" && len(stanza.Args) > 0 {
		message = stanza.Args[0]
	}
	if message == "" {
		message = "unspecified plugin error"
	}
	return fmt.Errorf("%w: %s", ErrProtocol, message)
}
