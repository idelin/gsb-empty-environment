package netsec

// DataSender transmits application data on an already validated, active path.
// It receives the exact [Path] so the implementation can bind the datagram to
// path.Local and address it to path.Peer.
type DataSender func(path Path, payload []byte) error

// Host is the transport-host entry point. It owns a [Validator] and refuses to
// emit application data on any path that has not completed validation and been
// selected as the active path.
type Host struct {
	validator *Validator
	sendData  DataSender
}

// NewHost wraps v. sendData is invoked only after the sending restriction has
// been satisfied.
func NewHost(v *Validator, sendData DataSender) *Host {
	return &Host{validator: v, sendData: sendData}
}

// Validator exposes the underlying validator for candidate and connection
// management.
func (h *Host) Validator() *Validator { return h.validator }

// SendData sends application payload on path. Before validation completes the
// call is rejected and sendData is never invoked, which keeps pre-validation
// traffic confined to PATH_CHALLENGE probes.
func (h *Host) SendData(path Path, payload []byte) error {
	if err := h.validator.CheckSend(path); err != nil {
		return err
	}
	return h.sendData(path, payload)
}
