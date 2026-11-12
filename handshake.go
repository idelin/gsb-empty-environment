package netsec

// Connect performs a registration lookup and handshake on behalf of a
// transport that has just created a connection from a peer presenting
// creds.
//
// The returned Conn is bound to whatever endpoint instance currently owns
// name, to creds and to a freshly minted Challenge. auth runs
// asynchronously and decides whether the peer proves possession of the
// challenge; messages are rejected until the callback succeeds.
//
// If name is unknown or the current instance was revoked concurrently, the
// handshake fails with ErrConnRejected and no connection is registered.
func (r *Registry) Connect(name string, creds PeerCreds, auth Authenticator) (*Conn, Challenge, error) {
	if auth == nil {
		return nil, Challenge{}, ErrConnRejected
	}
	challenge, err := NewChallenge()
	if err != nil {
		return nil, Challenge{}, err
	}

	r.mu.RLock()
	ep := r.endpoints[name]
	r.mu.RUnlock()
	if ep == nil {
		return nil, Challenge{}, ErrConnRejected
	}

	ep.mu.Lock()
	if ep.revoked {
		ep.mu.Unlock()
		return nil, Challenge{}, ErrConnRejected
	}
	conn := &Conn{
		ep:        ep,
		instance:  ep.instance,
		creds:     creds,
		challenge: challenge,
		auth:      auth,
		doneCh:    make(chan struct{}),
	}
	ep.conns[conn] = struct{}{}
	ep.mu.Unlock()

	go auth.Authenticate(creds, challenge, conn.authed)
	return conn, challenge, nil
}
