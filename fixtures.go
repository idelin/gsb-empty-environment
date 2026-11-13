package netsec

// CorrectDevice is a regression fixture implementing the protocol exactly
// as the reference rules prescribe. Exploration must find no vulnerability.
type CorrectDevice struct{}

// Step implements Device.
func (CorrectDevice) Step(st State, msg Message, proofOK bool) (State, bool) {
	switch msg.Kind {
	case KindBegin:
		return State{Phase: PhaseChallenge, Session: msg.Session}, false
	case KindProof:
		if st.Session != msg.Session {
			return st, false
		}
		if st.Phase == PhaseChallenge {
			if proofOK {
				return State{Phase: PhaseAuthenticated, Session: st.Session, AuthFlag: true}, false
			}
			return InitialState(), false
		}
		return st, false
	case KindAbort:
		if st.Session == msg.Session {
			return InitialState(), false
		}
		return st, false
	case KindReset:
		return InitialState(), false
	case KindWrite:
		return st, st.Phase == PhaseAuthenticated && st.Session == msg.Session
	}
	return st, false
}

// LeakyAuthFlagDevice is a regression fixture whose Begin handler forgets
// to clear authFlag. Its Write gate trusts authFlag plus a session match,
// so a stale flag from a previously authenticated session reopens the write
// path after a fresh Begin.
type LeakyAuthFlagDevice struct{}

// Step implements Device.
func (LeakyAuthFlagDevice) Step(st State, msg Message, proofOK bool) (State, bool) {
	switch msg.Kind {
	case KindBegin:
		// Bug: authFlag is not cleared when the new challenge is issued.
		return State{Phase: PhaseChallenge, Session: msg.Session, AuthFlag: st.AuthFlag}, false
	case KindProof:
		if st.Session != msg.Session {
			return st, false
		}
		if st.Phase == PhaseChallenge {
			if proofOK {
				return State{Phase: PhaseAuthenticated, Session: st.Session, AuthFlag: true}, false
			}
			return InitialState(), false
		}
		return st, false
	case KindAbort:
		if st.Session == msg.Session {
			return InitialState(), false
		}
		return st, false
	case KindReset:
		return InitialState(), false
	case KindWrite:
		// Bug surfaces here: the gate consults authFlag instead of the phase.
		return st, st.AuthFlag && st.Session == msg.Session
	}
	return st, false
}

// CrossSessionProofDevice is a regression fixture whose Proof handler does
// not check that the proof targets the session that was challenged: any
// valid signature authenticates the currently challenged session.
type CrossSessionProofDevice struct{}

// Step implements Device.
func (CrossSessionProofDevice) Step(st State, msg Message, proofOK bool) (State, bool) {
	switch msg.Kind {
	case KindBegin:
		return State{Phase: PhaseChallenge, Session: msg.Session}, false
	case KindProof:
		if st.Phase == PhaseChallenge {
			// Bug: msg.Session is never compared against st.Session.
			if proofOK {
				return State{Phase: PhaseAuthenticated, Session: st.Session, AuthFlag: true}, false
			}
			return InitialState(), false
		}
		return st, false
	case KindAbort:
		if st.Session == msg.Session {
			return InitialState(), false
		}
		return st, false
	case KindReset:
		return InitialState(), false
	case KindWrite:
		return st, st.Phase == PhaseAuthenticated && st.Session == msg.Session
	}
	return st, false
}
