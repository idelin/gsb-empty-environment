package lease

import "time"

type CampaignRequest struct {
	Candidate string
}

type CampaignResponse struct {
	Granted   bool
	Term      uint64
	GrantedAt time.Time
	ExpiresAt time.Time
	Reason    string
}

type RenewRequest struct {
	Node string
	Term uint64
}

type RenewResponse struct {
	Granted   bool
	Term      uint64
	GrantedAt time.Time
	ExpiresAt time.Time
	Reason    string
}

type TokenRequest struct {
	Node      string
	Term      uint64
	RequestID uint64
}

type TokenResponse struct {
	Granted   bool
	Term      uint64
	Fence     uint64
	RequestID uint64
	Reason    string
}

type WriteRequest struct {
	From  string
	Term  uint64
	Fence uint64
	Value string
}

type WriteResponse struct {
	Accepted bool
	Fence    uint64
	Value    string
	Reason   string
}

type timerKind int

const (
	renewTimer timerKind = iota + 1
	campaignTimer
)

type timerMsg struct {
	kind timerKind
	term uint64
}
