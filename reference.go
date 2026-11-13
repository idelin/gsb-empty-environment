package netsec

// ReferenceStep applies the reference protocol rules to one message and
// reports whether a Write would be executed. proofOK is the verdict of the
// simulated verifier for Proof messages; it is ignored for other kinds.
//
// Rules:
//   - Begin(s) issues a challenge for session s and clears authFlag.
//   - A valid Proof(s) in challenge phase for the current session enters
//     authenticated and sets authFlag.
//   - An invalid Proof for the current session clears the whole state.
//   - A Proof for any other session leaves the state untouched.
//   - A Proof for the current session while already authenticated leaves
//     the state untouched.
//   - Abort(s) clears authentication only for the current session; the
//     session number returns to 0.
//   - Reset clears everything; the session number returns to 0.
//   - Write(s) is allowed only while authenticated with a matching
//     session; Write never changes the state.
func ReferenceStep(st State, msg Message, proofOK bool) (next State, wrote bool) {
	switch msg.Kind {
	case KindBegin:
		return State{Phase: PhaseChallenge, Session: msg.Session, AuthFlag: false}, false
	case KindProof:
		if st.Session != msg.Session {
			// Proof for another session: no state change.
			return st, false
		}
		switch st.Phase {
		case PhaseChallenge:
			if proofOK {
				return State{Phase: PhaseAuthenticated, Session: st.Session, AuthFlag: true}, false
			}
			// Invalid proof for the current session clears the state.
			return InitialState(), false
		default:
			// Idle, or already authenticated: no state change.
			return st, false
		}
	case KindAbort:
		if st.Session == msg.Session {
			return InitialState(), false
		}
		return st, false
	case KindReset:
		return InitialState(), false
	case KindWrite:
		allowed := st.Phase == PhaseAuthenticated && st.Session == msg.Session
		return st, allowed
	}
	return st, false
}
