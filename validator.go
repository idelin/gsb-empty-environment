package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Validation failures returned by Validator.
var (
	ErrForgedRoute        = errors.New("netsec: Route target outside trusted domains")
	ErrUntrustedDomain    = errors.New("netsec: cross-domain target without explicit trust policy")
	ErrViaMismatch        = errors.New("netsec: Via sent-by does not match observed transport peer")
	ErrForgedReceived     = errors.New("netsec: client-supplied Via received parameter")
	ErrBadBranch          = errors.New("netsec: Via branch missing magic cookie")
	ErrBranchReuse        = errors.New("netsec: Via branch reused across transactions")
	ErrDialogMismatch     = errors.New("netsec: dialog identifier or tag mismatch")
	ErrCSeqReplay         = errors.New("netsec: CSeq not monotonic within dialog")
	ErrUnknownTransaction = errors.New("netsec: response matches no live transaction")
)

// Config configures a Validator.
type Config struct {
	// OwnDomains are the domains this proxy is authoritative for. Route
	// targets and request URIs inside these domains are always accepted.
	OwnDomains []string
	// TrustedPeers is the explicit cross-domain trust policy: a request
	// whose next hop falls in one of these domains may be forwarded there.
	// Anything else is rejected with ErrUntrustedDomain.
	TrustedPeers []string
}

// Decision is the outcome of validating a request.
type Decision struct {
	// NextHop is where the proxy may forward the request. It is derived
	// only from the proxy's own recorded route set and trusted Route
	// entries, never from client-supplied Record-Route headers.
	NextHop URI
	// RespHost/RespPort are where responses to this request must be sent:
	// the transport peer the proxy actually observed, not header data.
	RespHost string
	RespPort int
	// Retransmission is true when the request repeats an in-flight
	// transaction (same branch, same sender, same CSeq).
	Retransmission bool
}

type transaction struct {
	callID   string
	sentBy   string
	method   string
	cseq     uint32
	respHost string
	respPort int
}

type dialog struct {
	fromTag  string
	toTag    string
	cseq     map[string]uint32 // method -> highest CSeq seen
	routeSet []URI             // route set recorded by this proxy only
}

// Validator checks SIP routing headers for a proxy. It is safe for
// concurrent use.
type Validator struct {
	own   map[string]bool
	peers map[string]bool

	mu      sync.Mutex
	txns    map[string]transaction // branch -> transaction
	dialogs map[string]*dialog     // Call-ID -> dialog state
}

// NewValidator builds a Validator from cfg.
func NewValidator(cfg Config) *Validator {
	v := &Validator{
		own:     map[string]bool{},
		peers:   map[string]bool{},
		txns:    map[string]transaction{},
		dialogs: map[string]*dialog{},
	}
	for _, d := range cfg.OwnDomains {
		v.own[strings.ToLower(d)] = true
	}
	for _, d := range cfg.TrustedPeers {
		v.peers[strings.ToLower(d)] = true
	}
	return v
}

func (v *Validator) trusted(host string) bool {
	host = strings.ToLower(host)
	return v.own[host] || v.peers[host]
}

// RecordOwnRoute appends a route entry this proxy itself inserted for the
// dialog identified by callID. Only entries recorded through this method
// are ever used to compute in-dialog next hops; Record-Route headers
// received from clients are ignored for routing.
func (v *Validator) RecordOwnRoute(callID string, route URI) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d := v.dialogs[callID]
	if d == nil {
		d = &dialog{cseq: map[string]uint32{}}
		v.dialogs[callID] = d
	}
	d.routeSet = append(d.routeSet, route)
}

// ValidateRequest checks a request's Via, Route and dialog state and
// returns the safe forwarding decision.
func (v *Validator) ValidateRequest(m *Message) (Decision, error) {
	if m.IsResponse {
		return Decision{}, fmt.Errorf("netsec: ValidateRequest called with a response")
	}
	if m.CallID == "" || m.FromTag == "" {
		return Decision{}, fmt.Errorf("netsec: missing Call-ID or From tag")
	}
	top, err := m.topVia()
	if err != nil {
		return Decision{}, err
	}
	if err := v.checkVia(top, m); err != nil {
		return Decision{}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	retrans, err := v.checkBranchLocked(top, m)
	if err != nil {
		return Decision{}, err
	}
	if err := v.checkDialogLocked(m); err != nil {
		return Decision{}, err
	}
	next, err := v.resolveNextHopLocked(m)
	if err != nil {
		return Decision{}, err
	}

	respPort := top.sentByPortOrDefault()
	if top.HasRPort && top.RPort != 0 {
		respPort = top.RPort
	} else if top.HasRPort && m.SrcPort != 0 {
		respPort = m.SrcPort
	}
	dec := Decision{
		NextHop:        next,
		RespHost:       m.SrcAddr,
		RespPort:       respPort,
		Retransmission: retrans,
	}
	v.txns[top.Branch] = transaction{
		callID:   m.CallID,
		sentBy:   top.SentByHost,
		method:   m.CSeqMethod,
		cseq:     m.CSeqNum,
		respHost: dec.RespHost,
		respPort: dec.RespPort,
	}
	return dec, nil
}

// checkVia enforces hop-by-hop Via rules against the observed peer.
func (v *Validator) checkVia(top Via, m *Message) error {
	if !strings.HasPrefix(top.Branch, BranchMagicCookie) {
		return fmt.Errorf("%w: %q", ErrBadBranch, top.Branch)
	}
	// The received parameter may only be written by this proxy; a value
	// arriving from the wire that contradicts the observed source is a
	// forgery attempt aimed at redirecting the response.
	if top.Received != "" && top.Received != strings.ToLower(m.SrcAddr) {
		return fmt.Errorf("%w: received=%s but peer is %s", ErrForgedReceived, top.Received, m.SrcAddr)
	}
	// The top Via's sent-by must be the host that actually handed us the
	// message, unless it is a trusted proxy hop.
	if m.SrcAddr != "" && top.SentByHost != strings.ToLower(m.SrcAddr) && !v.trusted(top.SentByHost) {
		return fmt.Errorf("%w: sent-by=%s but peer is %s", ErrViaMismatch, top.SentByHost, m.SrcAddr)
	}
	return nil
}

// checkBranchLocked detects branch reuse across transactions.
func (v *Validator) checkBranchLocked(top Via, m *Message) (retransmission bool, err error) {
	prev, ok := v.txns[top.Branch]
	if !ok {
		return false, nil
	}
	sameTxn := prev.callID == m.CallID && prev.sentBy == top.SentByHost &&
		prev.method == m.CSeqMethod && prev.cseq == m.CSeqNum
	if sameTxn {
		return true, nil
	}
	// ACK and CANCEL legitimately share the branch of the transaction they
	// belong to.
	if prev.callID == m.CallID && prev.sentBy == top.SentByHost &&
		(m.CSeqMethod == "ACK" || m.CSeqMethod == "CANCEL") {
		return false, nil
	}
	return false, fmt.Errorf("%w: branch %q", ErrBranchReuse, top.Branch)
}

// checkDialogLocked validates Call-ID, tags and CSeq against dialog state.
func (v *Validator) checkDialogLocked(m *Message) error {
	d, ok := v.dialogs[m.CallID]
	if !ok {
		if m.ToTag != "" {
			return fmt.Errorf("%w: in-dialog request for unknown dialog %q", ErrDialogMismatch, m.CallID)
		}
		d = &dialog{cseq: map[string]uint32{}}
		v.dialogs[m.CallID] = d
	}
	if d.fromTag != "" && d.fromTag != m.FromTag {
		return fmt.Errorf("%w: From tag", ErrDialogMismatch)
	}
	if d.fromTag == "" {
		d.fromTag = m.FromTag
	}
	if d.toTag != "" && m.ToTag != "" && d.toTag != m.ToTag {
		return fmt.Errorf("%w: To tag", ErrDialogMismatch)
	}
	if d.toTag == "" && m.ToTag != "" {
		d.toTag = m.ToTag
	}
	method := m.CSeqMethod
	if last, seen := d.cseq[method]; seen {
		switch {
		case m.CSeqNum < last:
			return fmt.Errorf("%w: %s CSeq %d < %d", ErrCSeqReplay, method, m.CSeqNum, last)
		case m.CSeqNum > last:
			d.cseq[method] = m.CSeqNum
		}
		// equal: retransmission, accepted without state change
	} else {
		d.cseq[method] = m.CSeqNum
	}
	return nil
}

// resolveNextHopLocked computes the next hop using only trusted inputs:
// the proxy's own recorded route set, and Route header entries whose
// targets lie inside trusted domains. Client Record-Route headers never
// influence the result.
func (v *Validator) resolveNextHopLocked(m *Message) (URI, error) {
	// Consume Route entries addressed to this proxy.
	routes := m.Routes
	for len(routes) > 0 && v.own[routes[0].Host] {
		routes = routes[1:]
	}
	// Every remaining Route target must stay inside the trust set.
	for _, r := range routes {
		if !v.trusted(r.Host) {
			return URI{}, fmt.Errorf("%w: %s", ErrForgedRoute, r.Host)
		}
	}
	var next URI
	switch {
	case len(routes) > 0:
		next = routes[0]
	default:
		if d, ok := v.dialogs[m.CallID]; ok && len(d.routeSet) > 0 {
			next = d.routeSet[0]
		} else {
			next = m.RequestURI
		}
	}
	if next.Host == "" {
		return URI{}, fmt.Errorf("netsec: cannot determine next hop")
	}
	if !v.trusted(next.Host) {
		return URI{}, fmt.Errorf("%w: %s", ErrUntrustedDomain, next.Host)
	}
	return next, nil
}

// ValidateResponse matches a response against the transaction table and
// returns where it must be sent: always the transport peer recorded when
// the request was validated, so a forged Via cannot redirect responses.
func (v *Validator) ValidateResponse(m *Message) (host string, port int, err error) {
	if !m.IsResponse {
		return "", 0, fmt.Errorf("netsec: ValidateResponse called with a request")
	}
	top, err := m.topVia()
	if err != nil {
		return "", 0, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	txn, ok := v.txns[top.Branch]
	if !ok || txn.callID != m.CallID || txn.method != m.CSeqMethod || txn.cseq != m.CSeqNum {
		return "", 0, fmt.Errorf("%w: branch %q", ErrUnknownTransaction, top.Branch)
	}
	return txn.respHost, txn.respPort, nil
}

// CompleteTransaction removes a completed transaction so its branch cannot
// be replayed against stale state, and returns whether it existed.
func (v *Validator) CompleteTransaction(branch string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.txns[branch]; !ok {
		return false
	}
	delete(v.txns, branch)
	return true
}
