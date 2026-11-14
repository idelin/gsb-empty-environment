package netsec

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"
)

// Dialer establishes a connection to a policy-approved referral target.
// Implementations must not perform a bind; the follower decides whether the
// bind is authenticated or anonymous.
type Dialer interface {
	Dial(context.Context, Target) (Conn, error)
}

// Conn is the LDAP operations needed by the referral follower.
type Conn interface {
	Bind(context.Context, string, string) (BindResult, error)
	Search(context.Context, SearchRequest) (SearchResult, error)
	Close() error
}

// BindResult is an LDAP simple-bind response.
type BindResult struct {
	Code       int
	Diagnostic string
	Referrals  []string
}

// SearchRequest describes the search repeated at each followable referral.
// The referral's DN, filter, scope, and attributes override these values when
// present in the LDAP URL.
type SearchRequest struct {
	BaseDN     string
	Scope      Scope
	Filter     string
	Attributes []string
	SizeLimit  int
	TimeLimit  time.Duration
	TypesOnly  bool
}

// Entry is a returned LDAP object.
type Entry struct {
	DN         string
	Attributes map[string][]string
}

// SearchResult contains entries and referral URLs returned by a search.
type SearchResult struct {
	Entries    []Entry
	References []string
	Code       int
	Diagnostic string
}

// LDAPDialer is the default TLS-enforcing transport.
type LDAPDialer struct {
	TLSConfig *tls.Config
	Timeout   time.Duration
	// AllowCleartext must be set before Dial will honor TLSModeNone.
	AllowCleartext bool

	// dialContext is a test seam and extension point for embedding the
	// transport in an existing connection pool.
	dialContext func(context.Context, string, string) (net.Conn, error)
}

// Dial connects to target. LDAPS performs the TLS handshake before returning.
// StartTLS performs the extended operation and TLS handshake before returning,
// so a caller can never send a bind over cleartext after a failed upgrade.
func (d *LDAPDialer) Dial(ctx context.Context, target Target) (Conn, error) {
	if target.Host == "" || target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("netsec: invalid target address")
	}
	switch target.Mode {
	case TLSModeLDAPS:
		if target.Scheme != SchemeLDAPS {
			return nil, fmt.Errorf("netsec: LDAPS mode requires the ldaps scheme")
		}
	case TLSModeStartTLS:
		if target.Scheme != SchemeLDAP {
			return nil, fmt.Errorf("netsec: StartTLS mode requires the ldap scheme")
		}
	case TLSModeNone:
		if target.Scheme != SchemeLDAP {
			return nil, fmt.Errorf("netsec: cleartext mode requires the ldap scheme")
		}
		if !d.AllowCleartext {
			return nil, ErrTLSRequired
		}
	default:
		return nil, fmt.Errorf("netsec: unknown TLS mode %q", target.Mode)
	}

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialContext := d.dialContext
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: timeout}
		dialContext = dialer.DialContext
	}
	raw, err := dialContext(ctx, "tcp", target.Address())
	if err != nil {
		return nil, fmt.Errorf("netsec: dial %s: %w", target.Address(), err)
	}
	if raw == nil {
		return nil, fmt.Errorf("netsec: dial %s returned no connection", target.Address())
	}

	tlsConfig, err := d.tlsConfigFor(target.Host)
	if err != nil {
		raw.Close()
		return nil, err
	}

	switch target.Mode {
	case TLSModeNone:
		return newWireConn(raw, timeout), nil
	case TLSModeLDAPS:
		tlsConn := tls.Client(raw, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("netsec: LDAPS handshake with %s: %w", target.Host, err)
		}
		if state := tlsConn.ConnectionState(); !state.HandshakeComplete {
			tlsConn.Close()
			return nil, fmt.Errorf("netsec: LDAPS handshake did not complete")
		}
		return newWireConn(tlsConn, timeout), nil
	case TLSModeStartTLS:
		wire := newWireConn(raw, timeout)
		if err := wire.startTLS(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		tlsConn := tls.Client(raw, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("netsec: StartTLS handshake with %s: %w", target.Host, err)
		}
		if state := tlsConn.ConnectionState(); !state.HandshakeComplete {
			tlsConn.Close()
			return nil, fmt.Errorf("netsec: StartTLS handshake did not complete")
		}
		return newWireConn(tlsConn, timeout), nil
	default:
		raw.Close()
		return nil, fmt.Errorf("netsec: unknown TLS mode %q", target.Mode)
	}
}

func (d *LDAPDialer) tlsConfigFor(host string) (*tls.Config, error) {
	cfg := &tls.Config{}
	if d.TLSConfig != nil {
		cfg = d.TLSConfig.Clone()
	}
	if cfg.InsecureSkipVerify {
		return nil, fmt.Errorf("netsec: InsecureSkipVerify is not allowed")
	}
	// Trust is bound to the literal referral host. ServerName also drives
	// certificate DNS-name/IP verification.
	cfg.ServerName = host
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	return cfg, nil
}

type wireConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writer  *bufio.Writer
	timeout time.Duration
	mu      sync.Mutex
	nextID  int32
}

func newWireConn(conn net.Conn, timeout time.Duration) *wireConn {
	return &wireConn{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		writer:  bufio.NewWriter(conn),
		timeout: timeout,
	}
}

func (c *wireConn) close() error { return c.conn.Close() }

func (c *wireConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetDeadline(time.Now().Add(time.Second))
	messageID := c.newMessageID()
	_, _ = c.writer.Write(berSequence(berInteger(int(messageID)), berElement(0x42, nil)))
	_ = c.writer.Flush()
	return c.close()
}

func (c *wireConn) newMessageID() int32 {
	c.nextID++
	if c.nextID <= 0 {
		c.nextID = 1
	}
	return c.nextID
}

func (c *wireConn) setDeadline(ctx context.Context) error {
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return c.conn.SetDeadline(deadline)
}

func (c *wireConn) writeMessage(messageID int32, op []byte) error {
	message := berSequence(berInteger(int(messageID)), op)
	if _, err := c.writer.Write(message); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *wireConn) readMessage() (int32, byte, []byte, error) {
	for {
		tag, content, err := readBERElement(c.reader)
		if err != nil {
			return 0, 0, nil, err
		}
		if tag != berTagSequence {
			return 0, 0, nil, fmt.Errorf("netsec: expected LDAPMessage SEQUENCE, got tag %#x", tag)
		}
		idTag, idContent, rest, err := readBERElementBytes(content)
		if err != nil {
			return 0, 0, nil, err
		}
		if idTag != berTagInteger {
			return 0, 0, nil, fmt.Errorf("netsec: LDAPMessage has no message ID")
		}
		messageID, err := parseBERInt(idContent)
		if err != nil {
			return 0, 0, nil, err
		}
		opTag, opContent, trailing, err := readBERElementBytes(rest)
		if err != nil {
			return 0, 0, nil, err
		}
		if len(trailing) != 0 {
			return 0, 0, nil, fmt.Errorf("netsec: trailing bytes in LDAPMessage")
		}
		// Message ID 0 is an unsolicited notification and never completes
		// the operation we are waiting for.
		if messageID == 0 {
			continue
		}
		return int32(messageID), opTag, opContent, nil
	}
}

func (c *wireConn) Bind(ctx context.Context, bindDN, password string) (BindResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.setDeadline(ctx); err != nil {
		return BindResult{}, err
	}
	messageID := c.newMessageID()
	op := berElement(0x60, concat(
		berInteger(3),
		berOctetString(bindDN),
		berElement(0x80, []byte(password)),
	))
	if err := c.writeMessage(messageID, op); err != nil {
		return BindResult{}, err
	}
	for {
		id, tag, content, err := c.readMessage()
		if err != nil {
			return BindResult{}, err
		}
		if id != messageID {
			continue
		}
		if tag != 0x61 {
			return BindResult{}, fmt.Errorf("netsec: expected BindResponse, got tag %#x", tag)
		}
		result, err := parseLDAPResult(content)
		if err != nil {
			return BindResult{}, err
		}
		response := BindResult{Code: result.code, Diagnostic: result.diagnostic, Referrals: result.referrals}
		if result.code != 0 {
			return response, &LDAPError{Code: result.code, Diagnostic: result.diagnostic, Referrals: result.referrals}
		}
		return response, nil
	}
}

func (c *wireConn) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.Scope < ScopeBase || req.Scope > ScopeSub {
		return SearchResult{}, fmt.Errorf("netsec: invalid search scope %d", req.Scope)
	}
	if req.SizeLimit < 0 || req.TimeLimit < 0 {
		return SearchResult{}, fmt.Errorf("netsec: search limits must not be negative")
	}
	if err := c.setDeadline(ctx); err != nil {
		return SearchResult{}, err
	}
	filter, err := EncodeFilter(req.Filter)
	if err != nil {
		return SearchResult{}, err
	}
	timeLimit := int(req.TimeLimit / time.Second)
	if req.TimeLimit > 0 && timeLimit == 0 {
		timeLimit = 1
	}
	var attributes []byte
	for _, attr := range req.Attributes {
		attributes = append(attributes, berOctetString(attr)...)
	}
	op := berElement(0x63, concat(
		berOctetString(req.BaseDN),
		berEnumerated(int(req.Scope)),
		berEnumerated(0), // never dereference aliases
		berInteger(req.SizeLimit),
		berInteger(timeLimit),
		berBoolean(req.TypesOnly),
		filter,
		berSequence(attributes),
	))

	messageID := c.newMessageID()
	if err := c.writeMessage(messageID, op); err != nil {
		return SearchResult{}, err
	}

	var result SearchResult
	for {
		id, tag, content, err := c.readMessage()
		if err != nil {
			return result, err
		}
		if id != messageID {
			continue
		}
		switch tag {
		case 0x64:
			entry, err := parseSearchEntry(content)
			if err != nil {
				return result, err
			}
			result.Entries = append(result.Entries, entry)
		case 0x73:
			refs, err := parseReferralOctets(content)
			if err != nil {
				return result, err
			}
			result.References = append(result.References, refs...)
		case 0x65:
			ldapResult, err := parseLDAPResult(content)
			if err != nil {
				return result, err
			}
			result.Code = ldapResult.code
			result.Diagnostic = ldapResult.diagnostic
			result.References = append(result.References, ldapResult.referrals...)
			if ldapResult.code != 0 && ldapResult.code != 10 {
				return result, &LDAPError{
					Code:       ldapResult.code,
					Diagnostic: ldapResult.diagnostic,
					Referrals:  result.References,
				}
			}
			return result, nil
		default:
			return result, fmt.Errorf("netsec: unexpected search response tag %#x", tag)
		}
	}
}

func (c *wireConn) startTLS(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.setDeadline(ctx); err != nil {
		return err
	}
	messageID := c.newMessageID()
	op := berElement(0x77, berElement(0x80, []byte("1.3.6.1.4.1.1466.20037")))
	if err := c.writeMessage(messageID, op); err != nil {
		return err
	}
	for {
		id, tag, content, err := c.readMessage()
		if err != nil {
			return err
		}
		if id != messageID {
			continue
		}
		if tag != 0x78 {
			return fmt.Errorf("netsec: expected StartTLS ExtendedResponse, got tag %#x", tag)
		}
		result, err := parseLDAPResult(content)
		if err != nil {
			return err
		}
		if result.code != 0 {
			return &LDAPError{Code: result.code, Diagnostic: result.diagnostic, Referrals: result.referrals}
		}
		return nil
	}
}

type ldapResult struct {
	code       int
	diagnostic string
	referrals  []string
}

func parseLDAPResult(content []byte) (ldapResult, error) {
	var out ldapResult
	tag, codeContent, rest, err := readBERElementBytes(content)
	if err != nil {
		return out, err
	}
	if tag != berTagEnumerated {
		return out, fmt.Errorf("netsec: LDAP result has no result code")
	}
	out.code, err = parseBERInt(codeContent)
	if err != nil {
		return out, err
	}
	tag, _, rest, err = readBERElementBytes(rest)
	if err != nil {
		return out, err
	}
	if tag != berTagOctet {
		return out, fmt.Errorf("netsec: LDAP result has no matched DN")
	}
	tag, diagnostic, rest, err := readBERElementBytes(rest)
	if err != nil {
		return out, err
	}
	if tag != berTagOctet {
		return out, fmt.Errorf("netsec: LDAP result has no diagnostic message")
	}
	out.diagnostic = string(diagnostic)
	for len(rest) > 0 {
		tag, value, next, err := readBERElementBytes(rest)
		if err != nil {
			return out, err
		}
		rest = next
		if tag == 0xa3 {
			refs, err := parseReferralOctets(value)
			if err != nil {
				return out, err
			}
			out.referrals = append(out.referrals, refs...)
		}
	}
	return out, nil
}

// parseReferralOctets parses a Referral value. Implicitly tagged referrals
// contain a list of OCTET STRINGs directly; tolerate an explicit SEQUENCE too.
func parseReferralOctets(content []byte) ([]string, error) {
	if len(content) > 0 && content[0] == berTagSequence {
		_, nested, _, err := readBERElementBytes(content)
		if err != nil {
			return nil, err
		}
		content = nested
	}
	var refs []string
	for len(content) > 0 {
		tag, value, rest, err := readBERElementBytes(content)
		if err != nil {
			return nil, err
		}
		if tag != berTagOctet {
			return nil, fmt.Errorf("netsec: referral contains tag %#x instead of a URL", tag)
		}
		refs = append(refs, string(value))
		content = rest
	}
	return refs, nil
}

func parseSearchEntry(content []byte) (Entry, error) {
	tag, dnContent, rest, err := readBERElementBytes(content)
	if err != nil {
		return Entry{}, err
	}
	if tag != berTagOctet {
		return Entry{}, fmt.Errorf("netsec: search entry has no DN")
	}
	entry := Entry{DN: string(dnContent), Attributes: make(map[string][]string)}
	tag, attrsContent, rest, err := readBERElementBytes(rest)
	if err != nil {
		return Entry{}, err
	}
	if tag != berTagSequence {
		return Entry{}, fmt.Errorf("netsec: search entry has no attribute list")
	}
	if len(rest) != 0 {
		return Entry{}, fmt.Errorf("netsec: trailing bytes in search entry")
	}
	for len(attrsContent) > 0 {
		var attrContent []byte
		tag, attrContent, attrsContent, err = readBERElementBytes(attrsContent)
		if err != nil {
			return Entry{}, err
		}
		if tag != berTagSequence {
			return Entry{}, fmt.Errorf("netsec: malformed attribute")
		}
		tag, name, attrRest, err := readBERElementBytes(attrContent)
		if err != nil {
			return Entry{}, err
		}
		if tag != berTagOctet {
			return Entry{}, fmt.Errorf("netsec: attribute has no name")
		}
		tag, values, trailing, err := readBERElementBytes(attrRest)
		if err != nil {
			return Entry{}, err
		}
		if tag != berTagSet || len(trailing) != 0 {
			return Entry{}, fmt.Errorf("netsec: attribute has no value set")
		}
		var decoded []string
		for len(values) > 0 {
			tag, value, next, err := readBERElementBytes(values)
			if err != nil {
				return Entry{}, err
			}
			if tag != berTagOctet {
				return Entry{}, fmt.Errorf("netsec: malformed attribute value")
			}
			decoded = append(decoded, string(value))
			values = next
		}
		entry.Attributes[string(name)] = decoded
	}
	return entry, nil
}

func concat(parts ...[]byte) []byte {
	var total int
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}
