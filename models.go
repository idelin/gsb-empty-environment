package netsec

// CorrectHandshake builds a three-message mutual-authentication handshake.
//
//	c1: initiator -> responder   messages M1, M3
//	c2: responder -> initiator   message M2
//
// Each channel carries one message per leg, so with capacity >= 1 no send is
// ever discarded and both overflow policies verify Safe. Success states bind
// the concrete message tags each side must have received.
func CorrectHandshake(capacity int) Spec {
	return Spec{
		Channels: []Channel{
			{Name: "c1", Capacity: capacity},
			{Name: "c2", Capacity: capacity},
		},
		Endpoints: []Endpoint{
			{
				Name:     "I",
				Init:     "I_INIT",
				Incoming: "c2",
				Transitions: []Transition{
					{From: "I_INIT", To: "I_S1", Do: []Action{Send("c1", "M1")}},
					{From: "I_S1", OnRecv: "M2", To: "I_S2", Do: []Action{Send("c1", "M3")}},
				},
				Success: []SuccessBinding{{State: "I_S2", RequiredTags: []string{"M2"}}},
			},
			{
				Name:     "R",
				Init:     "R_INIT",
				Incoming: "c1",
				Transitions: []Transition{
					{From: "R_INIT", OnRecv: "M1", To: "R_S1", Do: []Action{Send("c2", "M2")}},
					{From: "R_S1", OnRecv: "M3", To: "R_DONE"},
				},
				Success: []SuccessBinding{{State: "R_DONE", RequiredTags: []string{"M1", "M3"}}},
			},
		},
	}
}

// StrictOrderHandshake adds one bounded retransmission of M1 on a
// capacity-1 c1 channel. R_DONE requires tags M1 and M3 and additionally a
// gap-free sequence history, which is exactly where the two policies diverge:
//
//   - DropNew: the duplicate M1 is discarded and never enters the queue, so
//     R receives a gap-free history and the search is Safe.
//   - DropOld: the duplicate evicts the already-queued M1. R then receives a
//     later sequence without the earlier one, the no-gap binding fails and
//     the search is Unsafe.
func StrictOrderHandshake(capacity int) Spec {
	return Spec{
		Channels: []Channel{
			{Name: "c1", Capacity: capacity},
			{Name: "c2", Capacity: capacity},
		},
		Endpoints: []Endpoint{
			{
				Name:     "I",
				Init:     "I_INIT",
				Incoming: "c2",
				Transitions: []Transition{
					{From: "I_INIT", To: "I_S1", Do: []Action{Send("c1", "M1")}},
					// One bounded retry: a separate state keeps the state
					// space finite because no transition can retry twice.
					{From: "I_S1", To: "I_S1R", Do: []Action{Send("c1", "M1")}},
					{From: "I_S1", OnRecv: "M2", To: "I_S2", Do: []Action{Send("c1", "M3")}},
					{From: "I_S1R", OnRecv: "M2", To: "I_S2", Do: []Action{Send("c1", "M3")}},
				},
				Success: []SuccessBinding{{State: "I_S2", RequiredTags: []string{"M2"}}},
			},
			{
				Name:     "R",
				Init:     "R_INIT",
				Incoming: "c1",
				Transitions: []Transition{
					{From: "R_INIT", OnRecv: "M1", To: "R_S1", Do: []Action{Send("c2", "M2")}},
					{From: "R_S1", OnRecv: "M3", To: "R_DONE"},
				},
				Success: []SuccessBinding{{
					State:        "R_DONE",
					RequiredTags: []string{"M1", "M3"},
					RequireNoGap: true,
				}},
			},
		},
	}
}

// HalfAuthHandshake builds the same topology but with a classic
// half-authentication flaw: the responder treats the first leg M1 as
// completion and enters R_DONE, while the binding still requires M3. No M3
// is ever produced on its state machine path, so the flaw is reachable with
// unlimited queues and under either overflow policy.
func HalfAuthHandshake(capacity int) Spec {
	return Spec{
		Channels: []Channel{
			{Name: "c1", Capacity: capacity},
			{Name: "c2", Capacity: capacity},
		},
		Endpoints: []Endpoint{
			{
				Name:     "I",
				Init:     "I_INIT",
				Incoming: "c2",
				Transitions: []Transition{
					{From: "I_INIT", To: "I_S1", Do: []Action{Send("c1", "M1")}},
					{From: "I_S1", OnRecv: "M2", To: "I_S2", Do: []Action{Send("c1", "M3")}},
				},
				Success: []SuccessBinding{{State: "I_S2", RequiredTags: []string{"M2"}}},
			},
			{
				Name:     "R",
				Init:     "R_INIT",
				Incoming: "c1",
				Transitions: []Transition{
					// Flaw: one leg is accepted as a full authentication.
					{From: "R_INIT", OnRecv: "M1", To: "R_DONE"},
				},
				Success: []SuccessBinding{{State: "R_DONE", RequiredTags: []string{"M1", "M3"}}},
			},
		},
	}
}
