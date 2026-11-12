// Package netsec implements an admission-control layer for a MySQL client
// driver's packet-reading loop.
//
// A malicious MySQL server can request arbitrary local files from a client at
// any point in a connection by sending a LOCAL INFILE request (a packet whose
// first payload byte is 0xFB). It can also downgrade authentication to
// mysql_clear_password via an AuthSwitchRequest (0xFE) and harvest the plaintext
// password. This package lets the driver ask a single stateful [Guard] whether
// such a request is allowed instead of trusting the server.
//
// The Guard is fail-closed:
//
//   - A 0xFB request is honored only when the client has just issued a
//     "LOAD DATA LOCAL INFILE '<file>'" statement and the file name carried in
//     the request byte-for-byte matches the one in that statement. Every other
//     0xFB request is rejected with an empty (file-content terminating) packet.
//   - An AuthSwitchRequest asking for mysql_clear_password is honored only when
//     the caller explicitly enabled cleartext passwords and the transport is
//     TLS. Other auth plugins are outside this policy and pass through.
package netsec

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

// Errors returned by the guard. They are sentinel values so callers can branch
// on them with errors.Is; additional context is wrapped around them.
var (
	// ErrLocalInfileDenied is returned for a rejected 0xFB request. An empty
	// terminating packet has already been written by the time this is returned,
	// so the driver must simply discard any file-transfer state and continue
	// reading the command's result stream.
	ErrLocalInfileDenied = errors.New("netsec: LOCAL INFILE request denied")

	// ErrCleartextDenied is returned for an AuthSwitchRequest that asks for
	// mysql_clear_password without both the cleartext opt-in and TLS.
	ErrCleartextDenied = errors.New("netsec: mysql_clear_password auth switch denied")
)

// PacketWriter is the subset of a MySQL connection used by the guard to send
// the empty packet that aborts a denied LOCAL INFILE transfer. Implementations
// write one MySQL packet (the 4-byte length/sequence header is added by the
// driver layer; WritePacket receives the payload only).
type PacketWriter interface {
	WritePacket(payload []byte) error
}

// FileOpener opens a local file for reading. It is an indirection so tests can
// run without touching the real file system. NewGuard wires it to os.Open.
type FileOpener func(name string) (io.ReadCloser, error)

// Guard holds the admission policy and the per-connection state that binds a
// LOCAL INFILE request to the statement that authorized it.
//
// A MySQL connection is strictly request/response and used by one goroutine at
// a time; Guard is therefore not safe for concurrent use, matching the driver
// connection it guards.
type Guard struct {
	allowCleartext bool
	tls            bool
	openFile       FileOpener

	// pendingInfile is the file name parsed from the most recent
	// "LOAD DATA LOCAL INFILE" statement, and is non-empty only while its
	// result stream is being read. It is consumed by the first 0xFB request,
	// whether admitted or denied, and reset by every subsequent statement.
	pendingInfile string
}

// NewGuard builds a Guard. allowCleartext mirrors the driver's
// allowCleartextPasswords option and tls reports whether the transport is
// encrypted.
func NewGuard(allowCleartext, tls bool) *Guard {
	return &Guard{
		allowCleartext: allowCleartext,
		tls:            tls,
		openFile: func(name string) (io.ReadCloser, error) {
			return os.Open(name)
		},
	}
}

// NoteQuery must be called whenever the client sends a COM_QUERY statement.
// It records the file name only when the statement is an explicit
// "LOAD DATA LOCAL INFILE '<file>'", so an unrelated statement disarms any
// previously pending file request.
func (g *Guard) NoteQuery(query string) {
	g.pendingInfile = parseLocalInfileFile(query)
}

// AdmitLocalInfile is called by the result-reading loop when the server sends a
// 0xFB LOCAL INFILE request. payload is the full request packet payload, i.e.
// 0xFB followed by the NUL-terminated requested file name.
//
// On success the requested file is returned opened and ready to stream back.
// On denial an empty terminating packet is written to w and
// ErrLocalInfileDenied is returned. The pending statement is consumed either
// way, so a second, unsolicited 0xFB in the same stream cannot ride on the
// first statement's authorization.
func (g *Guard) AdmitLocalInfile(payload []byte, w PacketWriter) (io.ReadCloser, error) {
	requested := parseLocalInfileRequest(payload)
	authorized := g.pendingInfile
	g.pendingInfile = ""

	if authorized == "" || requested == "" || requested != authorized {
		if err := w.WritePacket(nil); err != nil {
			return nil, errors.Join(ErrLocalInfileDenied, err)
		}
		return nil, errors.Join(ErrLocalInfileDenied,
			errors.New("requested file does not match the pending LOAD DATA LOCAL INFILE statement"))
	}

	file, err := g.openFile(requested)
	if err != nil {
		// We cannot stream the file: terminate the transfer with an empty
		// packet so the server does not wait for contents, then surface the
		// open failure.
		if writeErr := w.WritePacket(nil); writeErr != nil {
			return nil, errors.Join(ErrLocalInfileDenied, err, writeErr)
		}
		return nil, errors.Join(ErrLocalInfileDenied, err)
	}
	return file, nil
}

// AdmitAuthSwitch is called by the connection state machine when the server
// sends an AuthSwitchRequest (0xFE). payload is the full packet payload, i.e.
// 0xFE followed by the NUL-terminated plugin name and plugin data.
//
// It returns nil when the switch may proceed. A switch to
// mysql_clear_password is admitted only with the caller's explicit opt-in and
// over TLS; otherwise ErrCleartextDenied is returned and no password must be
// sent. Requests for any other plugin are outside this policy and are allowed.
func (g *Guard) AdmitAuthSwitch(payload []byte) error {
	plugin, ok := parseAuthSwitchRequest(payload)
	if !ok {
		return errors.Join(ErrCleartextDenied, errors.New("malformed AuthSwitchRequest payload"))
	}
	if plugin != "mysql_clear_password" {
		return nil
	}
	if g.allowCleartext && g.tls {
		return nil
	}
	return errors.Join(ErrCleartextDenied, errors.New(
		"mysql_clear_password requires allowCleartextPasswords and a TLS connection"))
}

// PendingInfile reports the currently armed file name (empty when disarmed).
// It is intended for tests and diagnostics.
func (g *Guard) PendingInfile() string { return g.pendingInfile }

// TLS reports whether the guarded connection runs over TLS.
func (g *Guard) TLS() bool { return g.tls }

// parseLocalInfileRequest extracts the file name from a 0xFB LOCAL INFILE
// request payload: 0xFB, then the name up to the first NUL.
func parseLocalInfileRequest(payload []byte) string {
	if len(payload) == 0 || payload[0] != 0xFB {
		return ""
	}
	name := payload[1:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return string(name)
}

// parseAuthSwitchRequest extracts the plugin name from an AuthSwitchRequest
// payload: 0xFE, then the NUL-terminated plugin name.
func parseAuthSwitchRequest(payload []byte) (string, bool) {
	if len(payload) == 0 || payload[0] != 0xFE {
		return "", false
	}
	rest := payload[1:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return "", false
	}
	return string(rest[:i]), true
}

// parseLocalInfileFile returns the quoted file name when query is an explicit
// "LOAD DATA [LOW_PRIORITY|CONCURRENT] LOCAL INFILE '<file>'" statement, and
// the empty string for anything else (including non-LOCAL and multi-statement
// input). Matching is case-insensitive for keywords as required by MySQL.
func parseLocalInfileFile(query string) string {
	s := &sqlScanner{text: query}

	s.skipSeparators()
	if !s.consumeKeyword("LOAD") {
		return ""
	}
	s.skipSeparators()
	if !s.consumeKeyword("DATA") {
		return ""
	}
	s.skipSeparators()
	// Optional LOW_PRIORITY / CONCURRENT modifiers.
	for s.consumeKeyword("LOW_PRIORITY") || s.consumeKeyword("CONCURRENT") {
		s.skipSeparators()
	}
	if !s.consumeKeyword("LOCAL") {
		return ""
	}
	s.skipSeparators()
	if !s.consumeKeyword("INFILE") {
		return ""
	}
	s.skipSeparators()

	file, ok := s.readStringLiteral()
	if !ok || file == "" {
		return ""
	}

	// Nothing but separators may follow the single statement. Reject
	// multi-statement payloads such as "LOAD DATA ...; SELECT ...".
	s.skipSeparators()
	if s.pos != len(s.text) {
		return ""
	}
	return file
}

// sqlScanner is a tiny tokenizer for the fixed prefix of a LOAD DATA statement.
type sqlScanner struct {
	text string
	pos  int
}

// skipSeparators consumes whitespace and SQL comments.
func (s *sqlScanner) skipSeparators() {
	for s.pos < len(s.text) {
		c := s.text[s.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			s.pos++
		case c == '-' && s.peek(1) == '-' && isSeparator(s.peek(2)):
			// "-- " line comment; runs to end of line.
			s.pos += 2
			for s.pos < len(s.text) && s.text[s.pos] != '\n' {
				s.pos++
			}
		case c == '#':
			for s.pos < len(s.text) && s.text[s.pos] != '\n' {
				s.pos++
			}
		case c == '/' && s.peek(1) == '*':
			s.pos += 2
			for s.pos < len(s.text) {
				if s.text[s.pos] == '*' && s.peek(1) == '/' {
					s.pos += 2
					break
				}
				s.pos++
			}
		default:
			return
		}
	}
}

// peek returns the byte at offset rel from the current position, or 0 past the
// end. 0 is never a valid SQL keyword character so it safely terminates
// lookahead.
func (s *sqlScanner) peek(rel int) byte {
	if s.pos+rel >= len(s.text) {
		return 0
	}
	return s.text[s.pos+rel]
}

// consumeKeyword reports whether keyword (upper-case) appears at the current
// position as a complete, case-insensitive word.
func (s *sqlScanner) consumeKeyword(keyword string) bool {
	if s.pos+len(keyword) > len(s.text) {
		return false
	}
	if !strings.EqualFold(s.text[s.pos:s.pos+len(keyword)], keyword) {
		return false
	}
	after := s.peek(len(keyword))
	if isIdentifierChar(after) {
		return false
	}
	s.pos += len(keyword)
	return true
}

// readStringLiteral consumes a single- or double-quoted SQL string and returns
// its decoded value. Backslash escapes and doubled-quote escaping are handled.
func (s *sqlScanner) readStringLiteral() (string, bool) {
	if s.pos >= len(s.text) {
		return "", false
	}
	quote := s.text[s.pos]
	if quote != '\'' && quote != '"' {
		return "", false
	}
	s.pos++

	var b strings.Builder
	for s.pos < len(s.text) {
		c := s.text[s.pos]
		switch {
		case c == quote:
			// A doubled quote is an escaped quote; otherwise the string ends.
			if s.peek(1) == quote {
				b.WriteByte(quote)
				s.pos += 2
				continue
			}
			s.pos++
			return b.String(), true
		case c == '\\' && s.pos+1 < len(s.text):
			s.pos++
			b.WriteByte(decodeMySQLEscape(s.text[s.pos]))
			s.pos++
		default:
			b.WriteByte(c)
			s.pos++
		}
	}
	return "", false
}

func decodeMySQLEscape(c byte) byte {
	switch c {
	case '0':
		return 0
	case 'b':
		return '\b'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'Z':
		return 0x1a
	default:
		return c
	}
}

func isIdentifierChar(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

func isSeparator(c byte) bool {
	return c == 0 || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
