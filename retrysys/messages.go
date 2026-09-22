package retrysys

// All cross-layer interaction is an explicit simulator message. There are no
// direct method calls between proxy, service and replicas, so network delay,
// drops, timer firings, cancellation and late replies are all observable in
// virtual time.

// CallReq is proxy -> service: start one lower-level retry loop.
type CallReq struct {
	Call    int
	Proxy   int
	ProxyID AttemptID
	Budget  *Budget
}

// CallResp is service -> proxy: the lower loop reached a terminal result.
type CallResp struct {
	Call  int
	Proxy int
	OK    bool
	Why   Why
}

// ReplicaReq is service -> replica: one physical backend request. Ordinal is
// the physical ordinal within the top-level call and drives the fault script.
type ReplicaReq struct {
	Call     int
	Service  int
	Replica  string
	Physical AttemptID
	Ordinal  int
}

// ReplicaResp is replica -> service.
type ReplicaResp struct {
	Call     int
	Service  int
	Replica  string
	Physical AttemptID
	OK       bool
	Late     bool
}

// ReplicaCancel is service -> replica: cancel one physical request. A replica
// script may honor it (suppress the pending reply) or ignore it (the cancel
// was lost/too late, modeling a late failure after settlement).
type ReplicaCancel struct {
	Call     int
	Physical AttemptID
}

// CancelReqs is an internal service control message routed through the
// simulator so cancellation respects virtual-time ordering.
type CancelReqs struct{ Call int }

// DeadlineFire is world -> service/proxy: the shared deadline just expired.
type DeadlineFire struct{ Call int }

// ServiceAttemptTimeout fires when one lower attempt exceeds its timeout.
type ServiceAttemptTimeout struct {
	Call    int
	Service int
}

// ProxyRetry schedules the next upper attempt after a backoff.
type ProxyRetry struct{ Call int }

// ProxyAttemptTimeout bounds one upper attempt (the lower loop beneath it).
type ProxyAttemptTimeout struct {
	Call  int
	Proxy int
}

// StartCall is an external kick delivered to the proxy.
type StartCall struct{ Call int }
