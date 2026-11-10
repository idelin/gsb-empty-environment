package main

// Scenario is the independently replayable input description.
type Scenario struct {
	Forbidden []string        `json:"forbidden_cidrs"`
	Hosts     map[string]Host `json:"hosts"`
	Requests  []RequestSpec   `json:"requests"`
}

// Host describes the deterministic, scripted resolution answers for one name.
type Host struct {
	Port  int        `json:"port"`
	TTL   int        `json:"ttl"`
	Steps [][]string `json:"resolution_steps"`
}

// RequestSpec is one outbound request attempt.
type RequestSpec struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	ValidateAt int64  `json:"validate_at"`
	DialAt     int64  `json:"dial_at"`
	Port       int    `json:"port,omitempty"`
}

const (
	EvTimerExpiry      = "dns_timer_expiry"
	EvCacheLookup      = "dns_cache_lookup"
	EvResolve          = "dns_resolve"
	EvCacheStore       = "dns_cache_store"
	EvValidate         = "ssrf_check"
	EvPoolGet          = "pool_get"
	EvPoolMiss         = "pool_miss"
	EvDial             = "dial"
	EvPoolPut          = "pool_put"
	EvViolation        = "violation"
	ViolationDial      = "V1_DIAL_FORBIDDEN_AFTER_PASS"
	ViolationPoolReuse = "V2_POOL_PEER_FORBIDDEN_AFTER_PASS"
)

// Event is one causally ordered step of the replay.
type Event struct {
	Seq       int               `json:"seq"`
	Type      string            `json:"type"`
	At        int64             `json:"at"`
	RequestID string            `json:"request_id,omitempty"`
	Host      string            `json:"host,omitempty"`
	VC        map[string]int    `json:"vector_clock"`
	Parents   []int             `json:"parents"`
	Detail    map[string]string `json:"detail"`
	Violation string            `json:"violation,omitempty"`
}

type cacheEntry struct {
	addrs   []string
	stored  int64
	expires int64
	ttl     int
	seq     int
}

type conn struct {
	id     int
	peer   string
	key    string
	bornAt int64
}

// Violation is one request whose check passed but whose network action
// (dial or pool reuse) reached a forbidden address.
type Violation struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	RequestID       string   `json:"request_id"`
	Host            string   `json:"host"`
	ValidatedAddrs  []string `json:"validated_addresses"`
	DialAddress     string   `json:"dial_address,omitempty"`
	ReusedPeer      string   `json:"reused_peer,omitempty"`
	ReusedConnID    int      `json:"reused_conn_id,omitempty"`
	ValidateAt      int64    `json:"validate_at"`
	ActionAt        int64    `json:"action_at"`
	EventSeq        int      `json:"event_seq"`
	CausalEventSeqs []int    `json:"causal_event_seqs"`
	SubScenario     Scenario `json:"subscenario"`
}

// Report is the full replay result.
type Report struct {
	ForbiddenCIDRs []string    `json:"forbidden_cidrs"`
	Events         []Event     `json:"events"`
	Violations     []Violation `json:"violations"`
}
