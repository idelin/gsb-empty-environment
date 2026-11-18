package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ServerResponse is the CDS/CDNSKEY/DNSKEY material returned by one
// authoritative server for the child zone apex. RRSIG slices must contain the
// signatures covering the corresponding RRset.
//
// Err represents a transport, timeout or malformed-response failure. Any
// error makes the whole observation invalid: a continuous observation window
// is reset rather than treating the missing server as agreeing.
type ServerResponse struct {
	Server string
	Err    error

	CDS           []CDS
	CDSSignatures []RRSIG

	CDNSKEY           []CDNSKEY
	CDNSKEYSignatures []RRSIG

	DNSKEY           []DNSKEY
	DNSKEYSignatures []RRSIG
}

// Request is one observation of all authoritative servers for one child zone.
type Request struct {
	// Zone is the child zone apex, for example "child.example.".
	Zone string

	// CurrentDS is the DS RRset currently published in the parent.
	CurrentDS []DS

	// Servers must contain every authoritative server for the child zone.
	Servers []ServerResponse

	// CDNSKEYDigestTypes is used only when the child publishes CDNSKEY without
	// CDS. If empty, SHA-256 is used.
	CDNSKEYDigestTypes []uint8
}

// Signal describes the intent of a validated candidate.
type Signal uint8

const (
	SignalNone Signal = iota
	SignalUpdate
	SignalDelete
)

func (s Signal) String() string {
	switch s {
	case SignalNone:
		return "none"
	case SignalUpdate:
		return "update"
	case SignalDelete:
		return "delete"
	default:
		return fmt.Sprintf("signal(%d)", s)
	}
}

// Candidate is a validated CDS/CDNSKEY proposal. NewDS is the DS RRset the
// parent would publish if the proposal is accepted.
type Candidate struct {
	Signal  Signal
	Zone    string
	CDS     []DS
	CDNSKEY []DNSKEY
	NewDS   []DS
}

// Evaluate validates one round of observations. It does not implement the
// continuous-observation requirement; use Acceptor for that.
//
// A nil error with SignalNone means all servers consistently published no
// CDS/CDNSKEY update signal.
func Evaluate(now time.Time, req Request) (Candidate, error) {
	zone, err := canonicalName(req.Zone)
	if err != nil {
		return Candidate{}, fmt.Errorf("zone: %w", err)
	}
	if len(req.CurrentDS) == 0 {
		return Candidate{}, errors.New("no current DS records")
	}
	currentDS, err := canonicalDSList(req.CurrentDS)
	if err != nil {
		return Candidate{}, fmt.Errorf("current DS: %w", err)
	}
	if len(req.Servers) == 0 {
		return Candidate{}, errors.New("no authoritative servers supplied")
	}

	digestTypes := req.CDNSKEYDigestTypes
	if len(digestTypes) == 0 {
		digestTypes = []uint8{DigestSHA256}
	}

	states := make([]serverState, len(req.Servers))
	for i, response := range req.Servers {
		state, err := evaluateServer(now, zone, currentDS, digestTypes, response)
		if err != nil {
			return Candidate{}, fmt.Errorf("authoritative server %s: %w", serverName(response, i), err)
		}
		states[i] = state
	}

	anyCDS := false
	anyCDNSKEY := false
	for _, state := range states {
		anyCDS = anyCDS || len(state.cds) != 0
		anyCDNSKEY = anyCDNSKEY || len(state.cdnskeys) != 0
	}
	if !anyCDS && !anyCDNSKEY {
		return Candidate{Signal: SignalNone, Zone: zone}, nil
	}
	if anyCDS {
		for i := 1; i < len(states); i++ {
			if !dsListEqual(states[0].cds, states[i].cds) {
				return Candidate{}, fmt.Errorf(
					"authoritative server %s returned a different CDS set than %s",
					serverName(req.Servers[i], i), serverName(req.Servers[0], 0))
			}
		}
	}
	if anyCDNSKEY {
		for i := 1; i < len(states); i++ {
			if !dnskeyListEqual(states[0].cdnskeys, states[i].cdnskeys) {
				return Candidate{}, fmt.Errorf(
					"authoritative server %s returned a different CDNSKEY set than %s",
					serverName(req.Servers[i], i), serverName(req.Servers[0], 0))
			}
		}
	}

	first := states[0]
	if first.signal == SignalDelete {
		return Candidate{
			Signal:  SignalDelete,
			Zone:    zone,
			CDS:     first.cds,
			CDNSKEY: first.cdnskeys,
		}, nil
	}

	newDS := first.cds
	if len(newDS) == 0 {
		newDS = first.derivedDS
	}
	newDS = append([]DS(nil), newDS...)
	return Candidate{
		Signal:  SignalUpdate,
		Zone:    zone,
		CDS:     first.cds,
		CDNSKEY: first.cdnskeys,
		NewDS:   newDS,
	}, nil
}

type serverState struct {
	signal    Signal
	cds       []DS
	cdnskeys  []DNSKEY
	derivedDS []DS
}

func evaluateServer(now time.Time, zone string, currentDS []DS, digestTypes []uint8, response ServerResponse) (serverState, error) {
	if response.Err != nil {
		return serverState{}, response.Err
	}

	cds, err := canonicalDSList(response.CDS)
	if err != nil {
		return serverState{}, fmt.Errorf("CDS RRset: %w", err)
	}
	cdnskeys, err := canonicalDNSKEYList(response.CDNSKEY)
	if err != nil {
		return serverState{}, fmt.Errorf("CDNSKEY RRset: %w", err)
	}
	if len(cds) == 0 && len(cdnskeys) == 0 {
		return serverState{}, nil
	}

	dnskeys, err := canonicalDNSKEYList(response.DNSKEY)
	if err != nil {
		return serverState{}, fmt.Errorf("DNSKEY RRset: %w", err)
	}
	if len(dnskeys) == 0 {
		return serverState{}, errors.New("missing DNSKEY RRset")
	}
	trusted := trustedKSKs(zone, currentDS, dnskeys)
	if len(trusted) == 0 {
		return serverState{}, errors.New("published DNSKEY RRset contains no KSK matching the current DS")
	}
	dnskeysRData := dnskeyRData(dnskeys)
	if err := verifyWithAnyKey(zone, TypeDNSKEY, dnskeysRData, response.DNSKEYSignatures, trusted, now); err != nil {
		return serverState{}, fmt.Errorf("DNSKEY RRset: %w", err)
	}

	for _, k := range cdnskeys {
		if k.IsDelete() {
			return serverState{}, errors.New("CDNSKEY delete record is not a valid CDS update signal")
		}
	}

	if containsDeleteCDS(cds) {
		return evaluateDelete(zone, cds, cdnskeys, response.CDSSignatures, trusted, now)
	}

	state := serverState{
		signal:   SignalUpdate,
		cds:      cds,
		cdnskeys: cdnskeys,
	}

	if len(cds) != 0 {
		cdsRData := dsRData(cds)
		if err := verifyWithAnyKey(zone, TypeCDS, cdsRData, response.CDSSignatures, trusted, now); err != nil {
			return serverState{}, fmt.Errorf("CDS RRset: %w", err)
		}
		for _, d := range cds {
			key, ok := matchingPublishedKey(zone, d, dnskeys)
			if !ok {
				return serverState{}, fmt.Errorf("CDS record %s does not match a published DNSKEY", d)
			}
			if !key.IsKSK() {
				return serverState{}, fmt.Errorf("CDS record %s does not match a published KSK", d)
			}
		}
	}

	if len(cdnskeys) != 0 {
		cdnskeyRData := dnskeyRData(cdnskeys)
		if err := verifyWithAnyKey(zone, TypeCDNSKEY, cdnskeyRData, response.CDNSKEYSignatures, trusted, now); err != nil {
			return serverState{}, fmt.Errorf("CDNSKEY RRset: %w", err)
		}
		for _, k := range cdnskeys {
			if !k.IsKSK() {
				return serverState{}, fmt.Errorf("CDNSKEY %d/%d is not a published KSK", k.KeyTag(), k.Algorithm)
			}
			if !containsDNSKEY(dnskeys, k) {
				return serverState{}, fmt.Errorf("CDNSKEY %d/%d does not match a published DNSKEY", k.KeyTag(), k.Algorithm)
			}
		}
		if err := validateCDSCDNSKEYCorrespondence(zone, cds, cdnskeys); err != nil {
			return serverState{}, err
		}
	}

	if len(cds) == 0 {
		derived, err := deriveDS(zone, cdnskeys, digestTypes)
		if err != nil {
			return serverState{}, err
		}
		state.derivedDS = derived
	}
	return state, nil
}

func evaluateDelete(zone string, cds []DS, cdnskeys []DNSKEY, sigs []RRSIG, trusted []DNSKEY, now time.Time) (serverState, error) {
	if len(cds) != 1 || !cds[0].IsDelete() {
		return serverState{}, errors.New("CDS delete record 0 0 0 00 must not be mixed with other CDS records")
	}
	if len(cdnskeys) != 0 {
		return serverState{}, errors.New("CDS delete record 0 0 0 00 must not be mixed with CDNSKEY records")
	}
	if err := verifyWithAnyKey(zone, TypeCDS, dsRData(cds), sigs, trusted, now); err != nil {
		return serverState{}, fmt.Errorf("CDS delete RRset: %w", err)
	}
	return serverState{
		signal: SignalDelete,
		cds:    cds,
	}, nil
}

func containsDeleteCDS(cds []DS) bool {
	for _, d := range cds {
		if d.IsDelete() {
			return true
		}
	}
	return false
}

func trustedKSKs(zone string, currentDS []DS, dnskeys []DNSKEY) []DNSKEY {
	var trusted []DNSKEY
	for _, key := range dnskeys {
		if !key.IsKSK() {
			continue
		}
		for _, d := range currentDS {
			if d.Matches(zone, key) {
				trusted = append(trusted, key)
				break
			}
		}
	}
	return trusted
}

func verifyWithAnyKey(zone string, typ uint16, rdata [][]byte, sigs []RRSIG, keys []DNSKEY, now time.Time) error {
	var lastErr error
	for _, key := range keys {
		if err := VerifyRRSet(zone, typ, rdata, sigs, key, now); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no trusted KSK available")
	}
	return lastErr
}

func matchingPublishedKey(zone string, d DS, dnskeys []DNSKEY) (DNSKEY, bool) {
	for _, key := range dnskeys {
		if d.Matches(zone, key) {
			return key, true
		}
	}
	return DNSKEY{}, false
}

func containsDNSKEY(keys []DNSKEY, want DNSKEY) bool {
	for _, key := range keys {
		if bytes.Equal(key.rdata(), want.rdata()) {
			return true
		}
	}
	return false
}

func validateCDSCDNSKEYCorrespondence(zone string, cds []DS, cdnskeys []DNSKEY) error {
	if len(cds) == 0 {
		return nil
	}
	for _, d := range cds {
		found := false
		for _, k := range cdnskeys {
			if d.Matches(zone, k) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("CDS record %s has no matching CDNSKEY", d)
		}
	}
	for _, k := range cdnskeys {
		found := false
		for _, d := range cds {
			if d.Matches(zone, k) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("CDNSKEY %d/%d has no matching CDS record", k.KeyTag(), k.Algorithm)
		}
	}
	return nil
}

func deriveDS(zone string, cdnskeys []DNSKEY, digestTypes []uint8) ([]DS, error) {
	var out []DS
	for _, k := range cdnskeys {
		if !k.IsKSK() {
			return nil, fmt.Errorf("CDNSKEY %d/%d is not a KSK", k.KeyTag(), k.Algorithm)
		}
		for _, digestType := range digestTypes {
			d, err := k.ToDS(zone, digestType)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
	}
	canonical, err := canonicalDSList(out)
	if err != nil {
		return nil, err
	}
	if len(canonical) == 0 {
		return nil, errors.New("no CDNSKEY records to convert to DS")
	}
	return canonical, nil
}

func dnskeyRData(keys []DNSKEY) [][]byte {
	out := make([][]byte, len(keys))
	for i, key := range keys {
		out[i] = key.rdata()
	}
	return out
}

func dsRData(records []DS) [][]byte {
	out := make([][]byte, len(records))
	for i, record := range records {
		out[i] = record.rdata()
	}
	return out
}

func serverName(response ServerResponse, index int) string {
	if response.Server != "" {
		return response.Server
	}
	return fmt.Sprintf("#%d", index)
}

// Status is the outcome of an acceptance observation.
type Status uint8

const (
	StatusNoSignal Status = iota
	StatusRejected
	StatusPending
	StatusAccepted
)

func (s Status) String() string {
	switch s {
	case StatusNoSignal:
		return "no-signal"
	case StatusRejected:
		return "rejected"
	case StatusPending:
		return "pending"
	case StatusAccepted:
		return "accepted"
	default:
		return fmt.Sprintf("status(%d)", s)
	}
}

// Config controls the continuous-observation state machine.
type Config struct {
	// ObservationPeriod is how long a candidate must remain continuously
	// observed before it is accepted. A zero period accepts the first valid
	// observation.
	ObservationPeriod time.Duration

	// MaxObservationGap, when positive, resets the window if two successful
	// observations are farther apart than this. Zero disables the gap check;
	// explicit failed observations still reset the window.
	MaxObservationGap time.Duration

	// CDNSKEYDigestTypes is used for a CDNSKEY-only proposal. If empty,
	// SHA-256 is used.
	CDNSKEYDigestTypes []uint8

	// Clock is optional and defaults to time.Now.
	Clock func() time.Time
}

// Acceptor tracks a candidate across repeated observations.
//
// Each successful observation of the same candidate extends the window. A
// changed candidate, an invalid server response, a no-signal round, a gap
// longer than MaxObservationGap, or a backwards clock resets the window.
type Acceptor struct {
	mu    sync.Mutex
	cfg   Config
	state windowState
}

type windowState struct {
	key        string
	since      time.Time
	last       time.Time
	acceptedAt time.Time
}

// NewAcceptor constructs an Acceptor.
func NewAcceptor(cfg Config) (*Acceptor, error) {
	if cfg.ObservationPeriod < 0 {
		return nil, errors.New("observation period must not be negative")
	}
	if cfg.MaxObservationGap < 0 {
		return nil, errors.New("maximum observation gap must not be negative")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	cfg.CDNSKEYDigestTypes = append([]uint8(nil), cfg.CDNSKEYDigestTypes...)
	return &Acceptor{cfg: cfg}, nil
}

// Result is the decision for one observation.
type Result struct {
	Status Status

	// Accepted is true only when Status is StatusAccepted. In particular, a
	// previously accepted candidate that is contradicted by a later
	// observation is rejected.
	Accepted bool

	Signal  Signal
	Delete  bool
	Zone    string
	NewDS   []DS
	CDS     []DS
	CDNSKEY []DNSKEY

	Since       time.Time
	ObservedFor time.Duration
	AcceptedAt  time.Time

	// Reason explains a rejection or a window reset.
	Reason string
}

// Observe performs a time.Now-based observation.
func (a *Acceptor) Observe(req Request) Result {
	clock := a.cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return a.ObserveAt(clock(), req)
}

// ObserveAt performs an observation at now. It is intended for deterministic
// monitoring loops and tests.
func (a *Acceptor) ObserveAt(now time.Time, req Request) Result {
	if len(req.CDNSKEYDigestTypes) == 0 && len(a.cfg.CDNSKEYDigestTypes) != 0 {
		req.CDNSKEYDigestTypes = a.cfg.CDNSKEYDigestTypes
	}
	candidate, err := Evaluate(now, req)

	a.mu.Lock()
	defer a.mu.Unlock()

	if err != nil {
		a.state = windowState{}
		return Result{
			Status: StatusRejected,
			Reason: err.Error(),
		}
	}
	if candidate.Signal == SignalNone {
		a.state = windowState{}
		return Result{
			Status: StatusNoSignal,
			Signal: SignalNone,
			Zone:   candidate.Zone,
		}
	}

	key, err := observationKey(req, candidate)
	if err != nil {
		a.state = windowState{}
		return Result{
			Status: StatusRejected,
			Reason: err.Error(),
		}
	}

	reason := ""
	if a.state.key == "" || a.state.key != key {
		if a.state.key != "" {
			reason = "candidate changed; observation window restarted"
		}
		a.state = windowState{key: key, since: now, last: now}
	} else if now.Before(a.state.last) {
		reason = "clock moved backwards; observation window restarted"
		a.state = windowState{key: key, since: now, last: now}
	} else if a.cfg.MaxObservationGap > 0 && now.Sub(a.state.last) > a.cfg.MaxObservationGap {
		reason = "observation gap interrupted the continuous window"
		a.state = windowState{key: key, since: now, last: now}
	} else {
		a.state.last = now
	}

	elapsed := now.Sub(a.state.since)
	if elapsed < 0 {
		reason = "clock moved backwards; observation window restarted"
		a.state = windowState{key: key, since: now, last: now}
		elapsed = 0
	}
	result := Result{
		Signal:      candidate.Signal,
		Delete:      candidate.Signal == SignalDelete,
		Zone:        candidate.Zone,
		NewDS:       append([]DS(nil), candidate.NewDS...),
		CDS:         append([]DS(nil), candidate.CDS...),
		CDNSKEY:     append([]DNSKEY(nil), candidate.CDNSKEY...),
		Since:       a.state.since,
		ObservedFor: elapsed,
		Reason:      reason,
	}
	if elapsed >= a.cfg.ObservationPeriod {
		if a.state.acceptedAt.IsZero() {
			a.state.acceptedAt = now
		}
		result.Status = StatusAccepted
		result.Accepted = true
		result.AcceptedAt = a.state.acceptedAt
		return result
	}
	result.Status = StatusPending
	return result
}

// Reset discards any observation window. It does not change configuration.
func (a *Acceptor) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = windowState{}
}

func observationKey(req Request, candidate Candidate) (string, error) {
	currentDS, err := canonicalDSList(req.CurrentDS)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	appendPart := func(part string) {
		fmt.Fprintf(&b, "%d:%s;", len(part), part)
	}
	appendPart(candidate.Zone)
	for _, d := range currentDS {
		appendPart(string(d.rdata()))
	}
	appendPart(candidate.Signal.String())
	for _, d := range candidate.CDS {
		appendPart(string(d.rdata()))
	}
	for _, k := range candidate.CDNSKEY {
		appendPart(string(k.rdata()))
	}
	for _, d := range candidate.NewDS {
		appendPart(string(d.rdata()))
	}
	return b.String(), nil
}

// SortedDS returns a canonical copy of records, sorted and de-duplicated.
func SortedDS(records []DS) ([]DS, error) {
	return canonicalDSList(records)
}

// SortedDNSKEY returns a canonical copy of keys, sorted and de-duplicated.
func SortedDNSKEY(records []DNSKEY) ([]DNSKEY, error) {
	return canonicalDNSKEYList(records)
}
