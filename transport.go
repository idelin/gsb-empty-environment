package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

var errUnsupportedTransport = errors.New("unsupported transport")

// PortPair is an adjacent RTP/RTCP port or interleaved-channel pair.
type PortPair struct {
	RTP  uint16
	RTCP uint16
}

// TransportSpec is the validated subset of an RTSP Transport header.
type TransportSpec struct {
	Protocol             string
	TCP                  bool
	Destination          netip.Addr
	DestinationSpecified bool
	ClientPort           PortPair
	HasClientPort        bool
	Interleaved          PortPair
	HasInterleaved       bool
}

// ParseTransport validates a Transport header against the RTSP request source.
//
// A UDP SETUP must provide an even, adjacent client_port pair. A destination
// parameter, when present, must be a numeric unicast address equal to source.
// For TCP transport an even, adjacent interleaved pair is required.
func ParseTransport(header string, source netip.Addr) (TransportSpec, error) {
	source, err := normalizeSource(source)
	if err != nil {
		return TransportSpec{}, err
	}
	if strings.TrimSpace(header) == "" {
		return TransportSpec{}, errors.New("empty Transport header")
	}

	var firstErr error
	for _, alternative := range strings.Split(header, ",") {
		spec, err := parseTransportAlternative(strings.TrimSpace(alternative), source)
		if errors.Is(err, errUnsupportedTransport) {
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return spec, nil
	}
	if firstErr != nil {
		return TransportSpec{}, firstErr
	}
	return TransportSpec{}, errUnsupportedTransport
}

func parseTransportAlternative(alternative string, source netip.Addr) (TransportSpec, error) {
	if alternative == "" {
		return TransportSpec{}, errors.New("empty transport alternative")
	}

	parts := strings.Split(alternative, ";")
	protocol := strings.ToUpper(strings.TrimSpace(parts[0]))
	spec := TransportSpec{Protocol: protocol}
	switch protocol {
	case "RTP/AVP", "RTP/AVP/UDP":
		spec.TCP = false
	case "RTP/AVP/TCP":
		spec.TCP = true
	default:
		return TransportSpec{}, errUnsupportedTransport
	}

	params := make(map[string]string)
	seen := make(map[string]bool)
	for _, raw := range parts[1:] {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return TransportSpec{}, errors.New("empty transport parameter")
		}
		key, value, hasValue := strings.Cut(raw, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if seen[key] {
			return TransportSpec{}, fmt.Errorf("duplicate transport parameter %q", key)
		}
		seen[key] = true
		if !hasValue {
			switch key {
			case "unicast", "multicast":
				params[key] = ""
				continue
			default:
				return TransportSpec{}, fmt.Errorf("transport parameter %q has no value", key)
			}
		}
		params[key] = strings.TrimSpace(value)
	}

	if _, ok := params["multicast"]; ok {
		return TransportSpec{}, errors.New("multicast transport is not allowed")
	}
	if _, ok := params["unicast"]; !ok {
		if _, multicast := params["multicast"]; !multicast {
			params["unicast"] = ""
		}
	}

	if rawDestination, ok := params["destination"]; ok {
		if rawDestination == "" {
			return TransportSpec{}, errors.New("empty destination")
		}
		destination, err := parseDestination(rawDestination)
		if err != nil {
			return TransportSpec{}, err
		}
		if destination != source {
			return TransportSpec{}, fmt.Errorf("destination %s does not match request source %s", destination, source)
		}
		spec.Destination = destination
		spec.DestinationSpecified = true
	} else {
		spec.Destination = source
	}

	if rawPorts, ok := params["client_port"]; ok {
		ports, err := parsePortPair(rawPorts, true)
		if err != nil {
			return TransportSpec{}, fmt.Errorf("invalid client_port: %w", err)
		}
		spec.ClientPort = ports
		spec.HasClientPort = true
	}

	if rawChannels, ok := params["interleaved"]; ok {
		channels, err := parsePortPair(rawChannels, true)
		if err != nil {
			return TransportSpec{}, fmt.Errorf("invalid interleaved pair: %w", err)
		}
		if channels.RTCP > 255 {
			return TransportSpec{}, errors.New("interleaved channels must fit in one byte")
		}
		spec.Interleaved = channels
		spec.HasInterleaved = true
	}

	if spec.TCP {
		if !spec.HasInterleaved {
			return TransportSpec{}, errors.New("TCP transport requires an interleaved pair")
		}
	} else if !spec.HasClientPort {
		return TransportSpec{}, errors.New("UDP transport requires a client_port pair")
	}

	return spec, nil
}

func normalizeSource(addr netip.Addr) (netip.Addr, error) {
	if !addr.IsValid() {
		return netip.Addr{}, errors.New("invalid request source address")
	}
	addr = addr.Unmap().WithZone("")
	if addr.IsUnspecified() || addr.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("request source %s is not a unicast address", addr)
	}
	return addr, nil
}

func parseDestination(raw string) (netip.Addr, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	}
	if strings.ContainsAny(raw, "/%") {
		return netip.Addr{}, fmt.Errorf("destination %q is not a numeric unicast address", raw)
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("destination %q is not a numeric IP address", raw)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("destination %s is not a unicast address", addr)
	}
	return addr, nil
}

func parsePortPair(raw string, requireEven bool) (PortPair, error) {
	first, second, ok := strings.Cut(strings.TrimSpace(raw), "-")
	if !ok {
		return PortPair{}, fmt.Errorf("%q is not a port pair", raw)
	}
	rtp, err := parsePort(strings.TrimSpace(first))
	if err != nil {
		return PortPair{}, err
	}
	rtcp, err := parsePort(strings.TrimSpace(second))
	if err != nil {
		return PortPair{}, err
	}
	if rtp == 0 {
		return PortPair{}, errors.New("port zero is not allowed")
	}
	if requireEven && rtp%2 != 0 {
		return PortPair{}, fmt.Errorf("first port %d is not even", rtp)
	}
	if rtcp != rtp+1 {
		return PortPair{}, fmt.Errorf("ports %d-%d are not adjacent", rtp, rtcp)
	}
	return PortPair{RTP: rtp, RTCP: rtcp}, nil
}

func parsePort(raw string) (uint16, error) {
	if raw == "" {
		return 0, errors.New("empty port")
	}
	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", raw)
	}
	return uint16(value), nil
}

func parseSourceAddress(remoteAddr string) (netip.Addr, error) {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return netip.Addr{}, errors.New("missing request source address")
	}
	if addrPort, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return normalizeSource(addrPort.Addr())
	}
	if addr, err := netip.ParseAddr(remoteAddr); err == nil {
		return normalizeSource(addr)
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
			return normalizeSource(addr)
		}
	}
	return netip.Addr{}, fmt.Errorf("request source %q is not an IP address", remoteAddr)
}
