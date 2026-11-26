package tls13sm

// trafficManager owns the active read and write traffic states of one
// connection and enforces the per-direction KeyUpdate budget of RFC 8446
// section 4.6.3.
type trafficManager struct {
	suite         *CipherSuite
	schedule      *keySchedule
	maxKeyUpdates int

	writeEpoch   Epoch
	write        *trafficState
	writeUpdates int

	readEpoch   Epoch
	read        *trafficState
	readUpdates int

	pendingRead *trafficState // early-data epoch while handshake keys exist
	earlyWrite  *trafficState // retained 0-RTT write keys for EndOfEarlyData
}

func newTrafficManager(suite *CipherSuite, schedule *keySchedule, maxKeyUpdates int) *trafficManager {
	if maxKeyUpdates <= 0 {
		maxKeyUpdates = 1024
	}
	return &trafficManager{
		suite:         suite,
		schedule:      schedule,
		maxKeyUpdates: maxKeyUpdates,
		writeEpoch:    EpochPlaintext,
		readEpoch:     EpochPlaintext,
	}
}

func (m *trafficManager) installWrite(epoch Epoch, state *trafficState) {
	m.writeEpoch = epoch
	m.write = state
}

func (m *trafficManager) installRead(epoch Epoch, state *trafficState) {
	m.readEpoch = epoch
	m.read = state
}

// retainEarlyWrite stashes the current early-data write state before
// handshake write keys replace it, so EndOfEarlyData can still be sealed under
// the client_early_traffic key.
func (m *trafficManager) retainEarlyWrite() {
	m.earlyWrite = m.write
}

// sealRecord emits a protected record under the current write epoch.
func (m *trafficManager) sealRecord(innerType uint8, body []byte) (Record, error) {
	if m.write == nil {
		return Record{}, errState("no write traffic state installed")
	}
	return m.write.cipher.sealRecord(innerType, body), nil
}

// openRecord authenticates a record and verifies that it arrives under the
// expected epoch, blocking cross-epoch/ciphertext confusion.
func (m *trafficManager) openRecord(record Record) (uint8, []byte, error) {
	if record.Epoch != m.readEpoch {
		return 0, nil, errIllegal("record epoch %d does not match active read epoch %d", record.Epoch, m.readEpoch)
	}
	if m.read == nil {
		return 0, nil, errIllegal("no read traffic state installed for epoch %d", record.Epoch)
	}
	return m.read.cipher.openRecord(record)
}

// rotateWrite advances the write traffic secret via the "traffic_upd"
// derivation and resets the sequence number. The rotation count is bounded so
// an attacker cannot force unbounded KeyUpdate churn.
func (m *trafficManager) rotateWrite() error {
	if m.writeUpdates >= m.maxKeyUpdates {
		return ErrKeyUpdateLimit
	}
	nextSecret := m.schedule.applicationTrafficSecret(m.write.Secret)
	state, err := m.schedule.newTrafficState(nextSecret)
	if err != nil {
		return err
	}
	m.write = state
	m.writeUpdates++
	return nil
}

func (m *trafficManager) rotateRead() error {
	if m.readUpdates >= m.maxKeyUpdates {
		return ErrKeyUpdateLimit
	}
	nextSecret := m.schedule.applicationTrafficSecret(m.read.Secret)
	state, err := m.schedule.newTrafficState(nextSecret)
	if err != nil {
		return err
	}
	m.read = state
	m.readUpdates++
	return nil
}
