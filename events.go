package netsec

import "net/netip"

// EventType identifies the source of a network state-machine event.
type EventType string

const (
	EventTunnelChanged  EventType = "tunnel_changed"
	EventNetworkChanged EventType = "network_changed"
	EventUserAction     EventType = "user_action"
	EventTimeout        EventType = "timeout"
)

// TunnelStatus is the status reported by the VPN tunnel.
type TunnelStatus string

const (
	TunnelConnecting TunnelStatus = "connecting"
	TunnelUp         TunnelStatus = "up"
	TunnelDown       TunnelStatus = "down"
)

// UserAction is an explicit action from the user. A tunnel transition can
// never override StateDisconnectedByUser; only UserConnect does that.
type UserAction string

const (
	UserConnect    UserAction = "connect"
	UserDisconnect UserAction = "disconnect"
)

// NetworkChange is a complete snapshot of the physical interfaces and, when
// non-nil, the current VPN server addresses. PhysicalInterfaces always
// replaces the previous interface set, so old physical-interface permits are
// revoked as soon as this event is handled. A nil VPNServers slice retains the
// previous addresses; a non-nil empty slice clears them.
type NetworkChange struct {
	PhysicalInterfaces []string
	VPNServers         []netip.Addr
}

// Event is delivered by network-change callbacks, tunnel monitors, UI actions,
// or timers.
type Event struct {
	Type    EventType
	Tunnel  TunnelStatus
	Network *NetworkChange
	User    UserAction
}
