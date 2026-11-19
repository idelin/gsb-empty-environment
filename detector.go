package netsec

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// RC4EType is the Kerberos encryption type used by RC4-HMAC service tickets.
	RC4EType uint32 = 0x17

	// DefaultWindow is the sliding window used for bulk service-ticket requests.
	DefaultWindow = 10 * time.Minute

	// DefaultDistinctSPNThreshold is the number of distinct SPNs that triggers an alert.
	DefaultDistinctSPNThreshold = 5
)

// Event is the subset of a domain-controller TGS event (for example, Windows
// event 4769) that is relevant to Kerberoasting detection.
//
// ServiceAccount is the account that owns the requested SPN, normally the
// ServiceName field in a 4769 event. If it is omitted, it is derived from the
// host part of SPN. Machine accounts ending in "$" and krbtgt are ignored.
type Event struct {
	Time              time.Time `json:"time"`
	RequestingAccount string    `json:"requesting_account"`
	ServiceAccount    string    `json:"service_account,omitempty"`
	SPN               string    `json:"spn"`
	EType             uint32    `json:"etype"`
	Renewal           bool      `json:"renewal,omitempty"`
	AESEnforced       bool      `json:"aes_enforced,omitempty"`
}

// TGSEvent is an alias for Event.
type TGSEvent = Event

// Config controls a Detector. Zero values select the defaults.
type Config struct {
	Window               time.Duration
	DistinctSPNThreshold int

	// AESEnforcedAccounts lists service accounts whose domain policy requires
	// AES. Any RC4 service ticket for one of these accounts is alerted on
	// immediately.
	AESEnforcedAccounts []string

	// AESEnforced is an optional policy lookup for service accounts. It is
	// useful when the account policy is maintained outside this package.
	AESEnforced func(serviceAccount string) bool
}

// AlertReason identifies why an event produced an alert.
type AlertReason string

const (
	// ReasonBulkRC4 means one requester obtained RC4 tickets for enough
	// distinct SPNs within the sliding window.
	ReasonBulkRC4 AlertReason = "bulk_rc4_service_tickets"

	// ReasonAESEnforcedRC4 means an RC4 ticket was issued for a service account
	// whose domain policy requires AES.
	ReasonAESEnforcedRC4 AlertReason = "aes_enforced_rc4_service_ticket"
)

// Alert describes a suspicious TGS request.
type Alert struct {
	Time              time.Time     `json:"time"`
	WindowStart       time.Time     `json:"window_start"`
	RequestingAccount string        `json:"requesting_account"`
	ServiceAccount    string        `json:"service_account,omitempty"`
	SPN               string        `json:"spn"`
	EType             uint32        `json:"etype"`
	Reasons           []AlertReason `json:"reasons"`
	DistinctSPNCount  int           `json:"distinct_spn_count"`
	DistinctSPNs      []string      `json:"distinct_spns,omitempty"`
}

// HasReason reports whether the alert includes reason.
func (a Alert) HasReason(reason AlertReason) bool {
	for _, candidate := range a.Reasons {
		if candidate == reason {
			return true
		}
	}
	return false
}

type spnState struct {
	last time.Time
	name string
}

type requesterState struct {
	spns map[string]spnState
}

// Detector keeps per-requester sliding windows and service-account policy.
//
// A Detector is safe for concurrent use. Policy callbacks are invoked without
// holding internal locks.
type Detector struct {
	mu sync.Mutex

	window     time.Duration
	threshold  int
	aes        map[string]struct{}
	aesPolicy  func(string) bool
	requesters map[string]*requesterState
}

// NewDetector creates a detector. With no config it uses a 10 minute window and
// a threshold of five distinct SPNs. If more than one Config is provided, only
// the first is used.
func NewDetector(configs ...Config) *Detector {
	var cfg Config
	if len(configs) > 0 {
		cfg = configs[0]
	}

	d := &Detector{
		window:     cfg.Window,
		threshold:  cfg.DistinctSPNThreshold,
		aes:        make(map[string]struct{}),
		aesPolicy:  cfg.AESEnforced,
		requesters: make(map[string]*requesterState),
	}
	if d.window <= 0 {
		d.window = DefaultWindow
	}
	if d.threshold <= 0 {
		d.threshold = DefaultDistinctSPNThreshold
	}
	d.SetAESEnforcedAccounts(cfg.AESEnforcedAccounts...)
	return d
}

// SetWindow changes the sliding window. It resets accumulated request state.
func (d *Detector) SetWindow(window time.Duration) {
	if window <= 0 {
		window = DefaultWindow
	}
	d.mu.Lock()
	d.window = window
	d.requesters = make(map[string]*requesterState)
	d.mu.Unlock()
}

// Window returns the configured sliding window.
func (d *Detector) Window() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.window
}

// SetDistinctSPNThreshold changes the distinct-SPN threshold. It resets
// accumulated request state.
func (d *Detector) SetDistinctSPNThreshold(threshold int) {
	if threshold <= 0 {
		threshold = DefaultDistinctSPNThreshold
	}
	d.mu.Lock()
	d.threshold = threshold
	d.requesters = make(map[string]*requesterState)
	d.mu.Unlock()
}

// DistinctSPNThreshold returns the configured threshold.
func (d *Detector) DistinctSPNThreshold() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.threshold
}

// SetAESEnforced adds or removes a service account from the AES-enforced set.
func (d *Detector) SetAESEnforced(serviceAccount string, enforced bool) {
	key := canonicalAccount(serviceAccount)
	if key == "" {
		return
	}

	d.mu.Lock()
	if enforced {
		d.aes[key] = struct{}{}
	} else {
		delete(d.aes, key)
	}
	d.mu.Unlock()
}

// SetAESEnforcedAccounts replaces the AES-enforced service-account set.
func (d *Detector) SetAESEnforcedAccounts(accounts ...string) {
	next := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if key := canonicalAccount(account); key != "" {
			next[key] = struct{}{}
		}
	}

	d.mu.Lock()
	d.aes = next
	d.mu.Unlock()
}

// SetAESPolicy installs a service-account policy lookup. Passing nil removes
// the callback. The callback is called without internal locks held.
func (d *Detector) SetAESPolicy(policy func(serviceAccount string) bool) {
	d.mu.Lock()
	d.aesPolicy = policy
	d.mu.Unlock()
}

// Reset clears accumulated per-requester state while preserving configuration.
func (d *Detector) Reset() {
	d.mu.Lock()
	d.requesters = make(map[string]*requesterState)
	d.mu.Unlock()
}

// Evaluate processes one event and reports whether it produced an alert.
// A single event can contain both a bulk-request reason and an AES-policy
// reason.
func (d *Detector) Evaluate(event Event) (Alert, bool) {
	if event.EType != RC4EType {
		return Alert{}, false
	}

	spn := strings.TrimSpace(event.SPN)
	serviceAccount := strings.TrimSpace(event.ServiceAccount)
	if serviceAccount == "" {
		serviceAccount = serviceAccountFromSPN(spn)
	}

	filterAccount := serviceAccount
	if filterAccount == "" {
		filterAccount = spn
	}
	if isExcludedService(filterAccount) {
		return Alert{}, false
	}
	if spn == "" && serviceAccount == "" {
		return Alert{}, false
	}

	policyAccount := serviceAccount
	if policyAccount == "" {
		policyAccount = filterAccount
	}

	d.mu.Lock()
	_, knownEnforced := d.aes[canonicalAccount(policyAccount)]
	policy := d.aesPolicy
	window := d.window
	d.mu.Unlock()

	aesEnforced := event.AESEnforced || knownEnforced
	if !aesEnforced && policy != nil {
		aesEnforced = policy(policyAccount)
	}

	reasons := make([]AlertReason, 0, 2)
	distinctSPNs := []string(nil)
	distinctCount := 0

	if spn != "" {
		d.mu.Lock()
		window = d.window
		requesterKey := canonicalAccount(event.RequestingAccount)
		state := d.requesters[requesterKey]
		if state == nil {
			state = &requesterState{spns: make(map[string]spnState)}
			d.requesters[requesterKey] = state
		}

		cutoff := event.Time.Add(-d.window)
		for key, seen := range state.spns {
			if seen.last.Before(cutoff) {
				delete(state.spns, key)
			}
		}
		before := len(state.spns)

		spnKey := canonicalSPN(spn)
		if previous, ok := state.spns[spnKey]; !ok || event.Time.After(previous.last) {
			state.spns[spnKey] = spnState{last: event.Time, name: spn}
		}
		after := len(state.spns)

		distinctCount = after
		distinctSPNs = distinctSPNNames(state.spns)
		bulkAlert := before < d.threshold && after >= d.threshold
		d.mu.Unlock()

		if bulkAlert {
			reasons = append(reasons, ReasonBulkRC4)
		}
	}

	if aesEnforced {
		reasons = append(reasons, ReasonAESEnforcedRC4)
	}
	if len(reasons) == 0 {
		return Alert{}, false
	}

	return Alert{
		Time:              event.Time,
		WindowStart:       event.Time.Add(-window),
		RequestingAccount: strings.TrimSpace(event.RequestingAccount),
		ServiceAccount:    serviceAccount,
		SPN:               spn,
		EType:             event.EType,
		Reasons:           reasons,
		DistinctSPNCount:  distinctCount,
		DistinctSPNs:      distinctSPNs,
	}, true
}

// Observe processes one event and returns an alert, or nil when the event is
// not suspicious.
func (d *Detector) Observe(event Event) *Alert {
	alert, ok := d.Evaluate(event)
	if !ok {
		return nil
	}
	return &alert
}

// Process processes one event and returns zero or one alert.
func (d *Detector) Process(event Event) []Alert {
	alert, ok := d.Evaluate(event)
	if !ok {
		return nil
	}
	return []Alert{alert}
}

// ProcessBatch processes events in ascending timestamp order without modifying
// the caller's slice. Use this for imported logs that may not be sorted.
func (d *Detector) ProcessBatch(events []Event) []Alert {
	ordered := append([]Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Time.Before(ordered[j].Time)
	})

	var alerts []Alert
	for _, event := range ordered {
		if alert, ok := d.Evaluate(event); ok {
			alerts = append(alerts, alert)
		}
	}
	return alerts
}

func canonicalAccount(account string) string {
	return strings.ToLower(strings.TrimSpace(account))
}

func canonicalSPN(spn string) string {
	return strings.ToLower(strings.TrimSpace(spn))
}

func isExcludedService(account string) bool {
	account = strings.ToLower(strings.TrimSpace(account))
	if strings.HasSuffix(account, "$") {
		return true
	}
	return account == "krbtgt" || strings.HasPrefix(account, "krbtgt/")
}

func serviceAccountFromSPN(spn string) string {
	spn = strings.TrimSpace(spn)
	slash := strings.IndexByte(spn, '/')
	if slash < 0 || slash+1 >= len(spn) {
		return ""
	}

	host := spn[slash+1:]
	if next := strings.IndexByte(host, '/'); next >= 0 {
		host = host[:next]
	}
	if port := strings.IndexByte(host, ':'); port >= 0 {
		host = host[:port]
	}
	return strings.TrimSpace(host)
}

func distinctSPNNames(spns map[string]spnState) []string {
	keys := make([]string, 0, len(spns))
	for key := range spns {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, spns[key].name)
	}
	return names
}
