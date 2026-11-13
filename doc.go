// Package netsec provides a fail-closed state machine for routing and firewall
// rules around VPN tunnel transitions. Events come from tunnel monitors,
// physical-network callbacks, user actions, and timers. Every state change
// produces a complete ruleset for an atomic filter-layer replacement.
package netsec
