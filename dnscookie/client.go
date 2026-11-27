package dnscookie

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"sync"
)

var (
	ErrUnknownTXID           = errors.New("dnscookie: response TXID matches no pending query")
	ErrUnexpectedSource      = errors.New("dnscookie: response from unexpected address/port")
	ErrQuestionMismatch      = errors.New("dnscookie: response question does not match the query")
	ErrClientCookieMismatch  = errors.New("dnscookie: response client cookie mismatch")
	ErrMalformedServerCookie = errors.New("dnscookie: response server cookie has invalid format")
	ErrMissingCookieOption   = errors.New("dnscookie: response lacks the COOKIE option")
)

// ResponseStatus is the outcome of the client-side anti-poisoning
// state machine for one UDP response.
type ResponseStatus int

const (
	// StatusAccepted means the response passed all off-path checks.
	StatusAccepted ResponseStatus = iota
	// StatusBadCookieRetry means the server answered BADCOOKIE with a
	// fresh Server Cookie; it has been cached and the query should be
	// retried with RetryQuery.
	StatusBadCookieRetry
	// StatusTruncated means the server set TC=1; retry over TCP.
	StatusTruncated
)

// Result is the verdict for one processed response.
type Result struct {
	Status  ResponseStatus
	Message *Message
	// ServerCookie is the newly learned Server Cookie, if any.
	ServerCookie []byte
}

type pendingQuery struct {
	server netip.AddrPort
	qname  string
	qtype  uint16
	qclass uint16
	sentSC []byte // server cookie sent with the query
}

// Client implements the RFC 7873/9018 client-side anti-poisoning
// state machine. It accepts a UDP response only when the tuple
// (TXID, source address/port, QNAME, QTYPE, Client Cookie) matches a
// pending query and any Server Cookie is well-formed, and it caches
// learned Server Cookies per server IP.
type Client struct {
	Cookie  []byte // 8-byte Client Cookie
	UDPSize uint16

	mu            sync.Mutex
	pending       map[uint16]*pendingQuery
	serverCookies map[netip.Addr][]byte
}

// NewClient returns a Client with a fresh random Client Cookie.
func NewClient() (*Client, error) {
	c, err := NewClientCookie()
	if err != nil {
		return nil, err
	}
	return &Client{
		Cookie:        c,
		UDPSize:       1232,
		pending:       make(map[uint16]*pendingQuery),
		serverCookies: make(map[netip.Addr][]byte),
	}, nil
}

func (c *Client) randomIDLocked() uint16 {
	var b [2]byte
	for {
		_, _ = rand.Read(b[:])
		id := binary.BigEndian.Uint16(b[:])
		if _, taken := c.pending[id]; !taken {
			return id
		}
	}
}

// Query builds a UDP query for (qname, qtype) directed at server and
// registers it as pending. A cached Server Cookie for the server IP is
// attached when available.
func (c *Client) Query(server netip.AddrPort, qname string, qtype uint16) (wire []byte, id uint16, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id = c.randomIDLocked()
	sc := c.serverCookies[server.Addr()]
	wire, err = BuildQuery(id, qname, qtype, c.Cookie, sc, c.UDPSize)
	if err != nil {
		return nil, 0, err
	}
	c.pending[id] = &pendingQuery{
		server: server,
		qname:  normalizeQName(qname),
		qtype:  qtype,
		qclass: 1,
		sentSC: append([]byte(nil), sc...),
	}
	return wire, id, nil
}

func normalizeQName(q string) string {
	q = strings.ToLower(q)
	if !strings.HasSuffix(q, ".") {
		q += "."
	}
	return q
}

// ServerCookieFor returns the cached Server Cookie for a server IP.
func (c *Client) ServerCookieFor(ip netip.Addr) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sc, ok := c.serverCookies[ip]
	return append([]byte(nil), sc...), ok
}

// ProcessResponse validates one UDP response arriving from src
// against the pending-query state. Off-path or forged packets are
// rejected with a descriptive error.
func (c *Client) ProcessResponse(resp []byte, src netip.AddrPort) (*Result, error) {
	m, err := ParseMessage(resp)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	pq, ok := c.pending[m.ID]
	if !ok {
		return nil, ErrUnknownTXID
	}
	if src != pq.server {
		return nil, ErrUnexpectedSource
	}
	if normalizeQName(m.QName) != pq.qname || m.QType != pq.qtype {
		return nil, ErrQuestionMismatch
	}

	// The response must echo our Client Cookie; anything else is an
	// off-path forgery attempt.
	if !m.HasCookieOption {
		return nil, ErrMissingCookieOption
	}
	if len(m.ClientCookie) != ClientCookieLen || string(m.ClientCookie) != string(c.Cookie) {
		return nil, ErrClientCookieMismatch
	}

	// A Server Cookie, when present, must be well-formed; cache it
	// keyed by the server IP.
	if len(m.ServerCookie) > 0 {
		if err := CheckServerCookieFormat(m.ServerCookie); err != nil {
			return nil, ErrMalformedServerCookie
		}
		c.serverCookies[src.Addr()] = append([]byte(nil), m.ServerCookie...)
	}

	switch {
	case m.RCODE == RCodeBadCookie:
		// Keep the pending entry; the caller retries with the new
		// Server Cookie via RetryQuery.
		return &Result{Status: StatusBadCookieRetry, Message: m, ServerCookie: m.ServerCookie}, nil
	case m.Truncated:
		delete(c.pending, m.ID)
		return &Result{Status: StatusTruncated, Message: m, ServerCookie: m.ServerCookie}, nil
	default:
		delete(c.pending, m.ID)
		return &Result{Status: StatusAccepted, Message: m, ServerCookie: m.ServerCookie}, nil
	}
}

// RetryQuery rebuilds the query for a pending TXID, attaching the
// latest cached Server Cookie (e.g. after a BADCOOKIE reply).
func (c *Client) RetryQuery(id uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pq, ok := c.pending[id]
	if !ok {
		return nil, ErrUnknownTXID
	}
	sc := c.serverCookies[pq.server.Addr()]
	wire, err := BuildQuery(id, pq.qname, pq.qtype, c.Cookie, sc, c.UDPSize)
	if err != nil {
		return nil, err
	}
	pq.sentSC = append([]byte(nil), sc...)
	return wire, nil
}
