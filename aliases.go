package netsec

// Add is an alias for Append.
func (c *Chain) Add(filters ...Filter) (*Chain, error) {
	return c.Append(filters...)
}

// AddFilter is an alias for AppendFilter.
func (c *Chain) AddFilter(filter Filter) (*Chain, error) {
	return c.Append(filter)
}

// NewDefaultFilter is an alias for NewFilterWithDefault.
func NewDefaultFilter(defaultAction Action, defaultData uint32, rules ...Rule) Filter {
	return NewFilterWithDefault(defaultAction, defaultData, rules...)
}

// NewThreadGroup creates a host with the supplied thread IDs.
func NewThreadGroup(threads ...ThreadID) *Host {
	return NewHost(threads...)
}

// NewSandbox creates a host with the supplied thread IDs.
func NewSandbox(threads ...ThreadID) *Host {
	return NewHost(threads...)
}

// RegisterThread is an alias for AddThread.
func (h *Host) RegisterThread(id ThreadID) error {
	return h.AddThread(id)
}

// ChainForThread is an alias for ThreadChain.
func (h *Host) ChainForThread(id ThreadID) (*Chain, error) {
	return h.ThreadChain(id)
}

// InstallAll applies filters to every registered thread as one transaction.
func (h *Host) InstallAll(filters ...Filter) error {
	return h.Install(filters...)
}

// SetInstallHook is an alias for SetInstallFunc.
func (h *Host) SetInstallHook(fn InstallFunc) {
	h.SetInstallFunc(fn)
}

// SetRollbackHook is an alias for SetRollbackFunc.
func (h *Host) SetRollbackHook(fn RollbackFunc) {
	h.SetRollbackFunc(fn)
}
