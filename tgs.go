// Package netsec provides pluggable, stateful detectors for security
// events collected from Windows domain controllers.
//
// The TGS detector in this package identifies Kerberoasting activity on
// Event ID 4769 ("A Kerberos service ticket was requested") records: an
// attacker holding an ordinary domain account requests service tickets for
// many service principal names (SPNs), then cracks the RC4-HMAC (etype
// 0x17) ticket payloads offline to recover the service account passwords.
package netsec

import (
	"strings"
	"sync"
	"time"
)

// ETypeRC4HMAC is the Kerberos encryption type targeted by Kerberoasting:
// RC4-HMAC (ARCFOUR-HMAC). The encrypted part of such a ticket is keyed
// with the service account's NT hash, which is why 0x17 tickets can be
// cracked offline once captured.
const ETypeRC4HMAC uint32 = 0x17

const (
	// optRenewable marks an issued ticket as renewable. Renewable is a
	// normal property of Windows tickets and must not be treated as a
	// renewal request.
	optRenewable uint32 = 0x00800000
	// optRenew is the RFC 4120 KDC-OPTIONS bit (KDC_OPT_RENEW) set on a
	// TGS-REQ that asks the KDC to renew an existing ticket rather than
	// issue a new one. Renewal requests do not represent fresh SPN
	// enumeration.
	optRenew uint32 = 0x00000002
)

// Alert kind values reported by the TGS detector.
const (
	// AlertKerberoastingSPNSweep is raised once when a single requesting
	// account obtains RC4-HMAC tickets for at least
	// DetectorConfig.SPNThreshold distinct SPNs within one trailing
	// DetectorConfig.Window interval.
	AlertKerberoastingSPNSweep = "kerberoasting_spn_sweep"
	// AlertAESEnforcedRC4Ticket is raised when an RC4-HMAC ticket is
	// issued for a service account whose domain policy enforces AES only.
	AlertAESEnforcedRC4Ticket = "aes_enforced_rc4_ticket"
)

// DetectorConfig tunes the TGS detector. The zero value is not usable;
// construct one with DefaultDetectorConfig and override fields as needed.
type DetectorConfig struct {
	// Window is the trailing time window over which distinct SPNs are
	// counted per requesting account.
	Window time.Duration
	// SPNThreshold is the number of distinct SPNs at or above which a
	// sweep alert is raised within Window. Repeated requests for the
	// same SPN only count once.
	SPNThreshold int
	// AESOnlyServiceAccounts lists service accounts (matched case
	// insensitively against TGSEvent.ServiceName) whose domain policy
	// enforces AES. Any RC4-HMAC ticket observed for one of them is
	// alerted immediately, independently of the sweep window.
	AESOnlyServiceAccounts []string
	// IgnoreRenewals makes the detector skip TGS renewal requests (the
	// KDC_OPT_RENEW bit in TicketOptions). Renewal requests repeat an
	// already granted SPN and do not represent new service-ticket
	// activity.
	IgnoreRenewals bool
}

// DefaultDetectorConfig returns the configuration required by the rule
// set: a 10-minute trailing window, an alert at 5 or more distinct SPNs,
// and renewal requests ignored.
func DefaultDetectorConfig() DetectorConfig {
	return DetectorConfig{
		Window:         10 * time.Minute,
		SPNThreshold:   5,
		IgnoreRenewals: true,
	}
}

// TGSEvent is the normalized representation of a Windows security Event ID
// 4769 record. Field names mirror the event's XML data names.
type TGSEvent struct {
	// Time is the event timestamp (System/TimeCreated).
	Time time.Time
	// AccountName is the requesting account (TargetUserName in 4769).
	AccountName string
	// ServiceName is the requested SPN / service account name
	// (ServiceName in 4769), e.g. "MSSQLSvc/db01.corp.local:1433".
	ServiceName string
	// EType is the ticket encryption type (TicketEncryptionType), e.g.
	// 0x17 for RC4-HMAC, 0x12 for AES-256, 0x11 for AES-128.
	EType uint32
	// TicketOptions is the raw ticket options bitmask (TicketOptions).
	TicketOptions uint32
	// StatusCode is the Kerberos status code (Status); 0x0 is success.
	StatusCode uint32
}

// IsRenewal reports whether the TGS request carries the KDC_OPT_RENEW
// option, i.e. it asks the KDC to renew an existing ticket. The
// KDC_OPT_RENEWABLE bit alone only means the issued ticket may be renewed
// later and is present on ordinary Windows tickets.
func (e TGSEvent) IsRenewal() bool {
	return e.TicketOptions&optRenew != 0
}

// TGSAlert describes a single detection. A single event may produce more
// than one alert (for example a sweep event for an AES-enforced account).
type TGSAlert struct {
	// Kind is one of the Alert* constants.
	Kind string `json:"kind"`
	// Time is the timestamp of the event that triggered the alert.
	Time time.Time `json:"time"`
	// AccountName is the requesting account.
	AccountName string `json:"account_name"`
	// ServiceName is the SPN / service account on the triggering event.
	ServiceName string `json:"service_name"`
	// EType is the encryption type observed (0x17 for every alert).
	EType uint32 `json:"etype"`
	// DistinctSPNs is the number of distinct SPNs in the triggering
	// window. It is zero for alerts that are not sweep alerts.
	DistinctSPNs int `json:"distinct_spns,omitempty"`
}

// spnObservation records the first time an SPN was observed inside the
// current trailing window. Only the earliest timestamp matters: a later
// request for the same SPN must neither re-count the SPN nor extend its
// lifetime in the window.
type spnObservation struct {
	firstSeen time.Time
}

type accountState struct {
	spns    map[string]spnObservation
	alerted bool
}

// TGSDetector is a stateful, goroutine-safe detector for Kerberoasting on
// TGS (event 4769) events. Feed events chronologically per detector via
// Process; out-of-order events are tolerated as long as their timestamps
// fall inside the configured trailing window of later events.
type TGSDetector struct {
	cfg          DetectorConfig
	aesOnlyAccts map[string]struct{}

	mu       sync.Mutex
	accounts map[string]*accountState
}

// NewTGSDetector builds a detector from cfg. Use DefaultDetectorConfig to
// obtain the standard rule set.
func NewTGSDetector(cfg DetectorConfig) *TGSDetector {
	d := &TGSDetector{
		cfg:      cfg,
		accounts: make(map[string]*accountState),
	}
	if d.cfg.Window <= 0 {
		d.cfg.Window = 10 * time.Minute
	}
	if d.cfg.SPNThreshold <= 0 {
		d.cfg.SPNThreshold = 5
	}
	if len(cfg.AESOnlyServiceAccounts) > 0 {
		d.aesOnlyAccts = make(map[string]struct{}, len(cfg.AESOnlyServiceAccounts))
		for _, account := range cfg.AESOnlyServiceAccounts {
			account = normalizeAccount(account)
			if account != "" {
				d.aesOnlyAccts[account] = struct{}{}
			}
		}
	}
	return d
}

// Reset drops all accumulated window state. Configuration is retained.
func (d *TGSDetector) Reset() {
	d.mu.Lock()
	d.accounts = make(map[string]*accountState)
	d.mu.Unlock()
}

// Purge removes per-account state whose most recent observation is older
// than now minus Window, bounding memory on long-running hosts.
func (d *TGSDetector) Purge(now time.Time) {
	cutoff := now.Add(-d.cfg.Window)
	d.mu.Lock()
	for name, state := range d.accounts {
		var latest time.Time
		for _, obs := range state.spns {
			if obs.firstSeen.After(latest) {
				latest = obs.firstSeen
			}
		}
		if !latest.After(cutoff) {
			delete(d.accounts, name)
		}
	}
	d.mu.Unlock()
}

// Process evaluates one event 4769 record and returns the alerts it
// triggers. Failed requests, non-RC4 tickets, renewal requests and records
// that do not target a service account are ignored without touching window
// state. The returned slice is nil when nothing is detected.
func (d *TGSDetector) Process(event TGSEvent) []TGSAlert {
	if event.StatusCode != 0 {
		return nil
	}
	if event.EType != ETypeRC4HMAC {
		return nil
	}
	if d.cfg.IgnoreRenewals && event.IsRenewal() {
		return nil
	}
	if !isServiceAccount(event.ServiceName) {
		return nil
	}

	account := normalizeAccount(event.AccountName)
	if account == "" {
		return nil
	}
	service := normalizeSPN(event.ServiceName)

	d.mu.Lock()
	defer d.mu.Unlock()

	state := d.accounts[account]
	if state == nil {
		state = &accountState{spns: make(map[string]spnObservation)}
		d.accounts[account] = state
	}

	// Evict observations that have aged out of the trailing window.
	cutoff := event.Time.Add(-d.cfg.Window)
	for spn, obs := range state.spns {
		if !obs.firstSeen.After(cutoff) {
			delete(state.spns, spn)
		}
	}
	if len(state.spns) < d.cfg.SPNThreshold {
		state.alerted = false
	}

	// First occurrence in the window counts; repeats only refresh nothing.
	if _, exists := state.spns[service]; !exists {
		state.spns[service] = spnObservation{firstSeen: event.Time}
	}

	var alerts []TGSAlert

	if len(state.spns) >= d.cfg.SPNThreshold && !state.alerted {
		state.alerted = true
		alerts = append(alerts, TGSAlert{
			Kind:         AlertKerberoastingSPNSweep,
			Time:         event.Time,
			AccountName:  account,
			ServiceName:  service,
			EType:        event.EType,
			DistinctSPNs: len(state.spns),
		})
	}

	if d.isAESOnly(service) {
		alerts = append(alerts, TGSAlert{
			Kind:        AlertAESEnforcedRC4Ticket,
			Time:        event.Time,
			AccountName: account,
			ServiceName: service,
			EType:       event.EType,
		})
	}

	return alerts
}

// distinctSPNCount reports the current in-window distinct SPN count for an
// account. It is intended for diagnostics and tests.
func (d *TGSDetector) distinctSPNCount(account string) int {
	state := d.accounts[normalizeAccount(account)]
	if state == nil {
		return 0
	}
	return len(state.spns)
}

func (d *TGSDetector) isAESOnly(service string) bool {
	if len(d.aesOnlyAccts) == 0 {
		return false
	}
	if _, ok := d.aesOnlyAccts[service]; ok {
		return true
	}
	if base := serviceAccountBase(service); base != "" {
		_, ok := d.aesOnlyAccts[strings.ToLower(base)]
		return ok
	}
	return false
}

// isServiceAccount applies the scope rule: machine accounts (names ending
// in '$') and the krbtgt account are not service accounts for the purpose
// of this detector. The optional "@REALM" suffix is stripped first so that
// "DC01$@CORP.LOCAL" is still recognized as a machine account.
func isServiceAccount(serviceName string) bool {
	base := serviceAccountBase(serviceName)
	if base == "" {
		return false
	}
	if strings.HasSuffix(base, "$") {
		return false
	}
	if strings.EqualFold(base, "krbtgt") {
		return false
	}
	return true
}

// serviceAccountBase strips a trailing "@REALM" (Kerberos principal realm
// suffix) from a service name and trims surrounding whitespace.
func serviceAccountBase(serviceName string) string {
	name := strings.TrimSpace(serviceName)
	if at := strings.IndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}
	return strings.TrimSpace(name)
}

func normalizeAccount(account string) string {
	return strings.ToLower(strings.TrimSpace(account))
}

// normalizeSPN lowercases the SPN so that repeated requests differing only
// in letter casing are not counted as distinct SPNs.
func normalizeSPN(spn string) string {
	return strings.ToLower(strings.TrimSpace(spn))
}

// ParseTicketOptions parses a TicketOptions field as emitted in Windows
// event logs. A "0x" prefix selects hexadecimal ("0x40810000"); any other
// value is parsed as decimal ("1082146816"). Empty or malformed input
// yields 0.
func ParseTicketOptions(raw string) uint32 {
	return parseUint32(raw)
}

// ParseEType parses a TicketEncryptionType field the same way as
// ParseTicketOptions, for example "0x17" becomes 0x17 (23).
func ParseEType(raw string) uint32 {
	return parseUint32(raw)
}

func parseUint32(raw string) uint32 {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
		base = 16
	}
	var value uint64
	for _, r := range s {
		var digit uint64
		switch {
		case r >= '0' && r <= '9':
			digit = uint64(r - '0')
		case r >= 'a' && r <= 'f' && base == 16:
			digit = uint64(r-'a') + 10
		case r >= 'A' && r <= 'F' && base == 16:
			digit = uint64(r-'A') + 10
		default:
			return 0
		}
		value = value*uint64(base) + digit
		if value > 0xffffffff {
			return 0
		}
	}
	return uint32(value)
}
