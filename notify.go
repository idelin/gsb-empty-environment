package netsec

import "net/netip"

// ZoneConfig is the per-zone admission configuration. Primaries is the
// complete set of primary servers allowed to send NOTIFY for this zone.
type ZoneConfig struct {
	Name      string
	Primaries []netip.Addr
}

// IsPrimary reports whether source is one of the configured primary
// addresses for the zone. IPv4-mapped IPv6 addresses are normalised before
// comparison.
func (c ZoneConfig) IsPrimary(source netip.Addr) bool {
	if !source.IsValid() {
		return false
	}
	normalized := source.Unmap()
	for _, primary := range c.Primaries {
		if primary.IsValid() && primary.Unmap() == normalized {
			return true
		}
	}
	return false
}

// TSIGVerifier is injected by the caller's DNS message layer. It must return
// nil only when the TSIG (or equivalent shared-secret) signature on the
// NOTIFY message verified successfully.
type TSIGVerifier func(zone string, source netip.Addr) error

// NotifyDecision is the result of NOTIFY admission. Refresh is true only when
// both the source and TSIG checks succeeded.
type NotifyDecision struct {
	Refresh bool
	Reason  string
}

// Allowed is an alias for Refresh.
func (d NotifyDecision) Allowed() bool {
	return d.Refresh
}

// AdmitNotify applies the two NOTIFY admission conditions: source must be a
// configured primary for the zone, and the caller's TSIG verification must
// have succeeded. A forged source is rejected before tsigVerified is
// consulted.
func AdmitNotify(config ZoneConfig, source netip.Addr, tsigVerified bool) NotifyDecision {
	if !config.IsPrimary(source) {
		return NotifyDecision{Reason: "NOTIFY source is not a configured primary"}
	}
	if !tsigVerified {
		return NotifyDecision{Reason: "NOTIFY TSIG verification failed"}
	}
	return NotifyDecision{Refresh: true}
}

// AdmitNotify applies the zone configuration method form of NOTIFY admission.
func (c ZoneConfig) AdmitNotify(source netip.Addr, tsigVerified bool) NotifyDecision {
	return AdmitNotify(c, source, tsigVerified)
}

// AdmitNotifyWithVerifier performs NOTIFY admission using a caller-injected
// TSIG verifier. The verifier is not called for an untrusted source address.
func AdmitNotifyWithVerifier(config ZoneConfig, source netip.Addr, verify TSIGVerifier) NotifyDecision {
	if !config.IsPrimary(source) {
		return NotifyDecision{Reason: "NOTIFY source is not a configured primary"}
	}
	if verify == nil {
		return NotifyDecision{Reason: "NOTIFY TSIG verifier is not configured"}
	}
	if err := verify(config.Name, source); err != nil {
		return NotifyDecision{Reason: "NOTIFY TSIG verification failed: " + err.Error()}
	}
	return NotifyDecision{Refresh: true}
}

// AdmitNotifyWithVerifier applies the zone configuration method form of
// NOTIFY admission with a caller-injected TSIG verifier.
func (c ZoneConfig) AdmitNotifyWithVerifier(source netip.Addr, verify TSIGVerifier) NotifyDecision {
	return AdmitNotifyWithVerifier(c, source, verify)
}

// NotifyTriggersRefresh is a boolean convenience wrapper around AdmitNotify.
func NotifyTriggersRefresh(config ZoneConfig, source netip.Addr, tsigVerified bool) bool {
	return AdmitNotify(config, source, tsigVerified).Refresh
}
