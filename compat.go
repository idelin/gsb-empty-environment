package netsec

// AcceptResponse is an alias for HandleResponse.
func (m *Manager) AcceptResponse(msg HandshakeResponse) (*Binding, error) {
	return m.HandleResponse(msg)
}

// HandleHandshakeResponse is an alias for HandleResponse.
func (m *Manager) HandleHandshakeResponse(msg HandshakeResponse) (*Binding, error) {
	return m.HandleResponse(msg)
}

// AcceptCookie is an alias for HandleCookie.
func (m *Manager) AcceptCookie(msg CookieReply) (*Binding, error) {
	return m.HandleCookie(msg)
}

// HandleCookieReply is an alias for HandleCookie.
func (m *Manager) HandleCookieReply(msg CookieReply) (*Binding, error) {
	return m.HandleCookie(msg)
}

// MarkKeypairActive is an alias for Activate.
func (m *Manager) MarkKeypairActive(b *Binding) error {
	return m.Activate(b)
}

// MarkKeypairActive is an alias for Activate.
func (b *Binding) MarkKeypairActive() error {
	return b.Activate()
}

// StartAsync is an alias for StartCryptoTask.
func (m *Manager) StartAsync(b *Binding) (*CryptoTask, error) {
	return m.StartCryptoTask(b)
}

// Finish is an alias for Done.
func (t *CryptoTask) Finish() bool {
	return t.Done()
}

// FinishAsync is an alias for Done.
func (m *Manager) FinishAsync(t *CryptoTask) bool {
	if t == nil {
		return false
	}
	return t.Done()
}

// Lookup returns the newest binding for a numeric index. Callers processing
// untrusted messages must use HandleResponse or HandleCookie instead.
func (m *Manager) Lookup(index Index) (*Binding, bool) {
	return m.BindingByIndex(index)
}

// CurrentBinding is an alias for BindingByIndex.
func (m *Manager) CurrentBinding(index Index) (*Binding, bool) {
	return m.BindingByIndex(index)
}

// Recycle is an alias for Sweep.
func (m *Manager) Recycle() int {
	return m.Sweep()
}

// RemovePeer is an alias for DeletePeer.
func (m *Manager) RemovePeer(peer PublicKey) int {
	return m.DeletePeer(peer)
}

// NewBinding is an alias for BeginHandshake.
func (m *Manager) NewBinding(peer, ephemeral PublicKey) (*Binding, error) {
	return m.BeginHandshake(peer, ephemeral)
}
