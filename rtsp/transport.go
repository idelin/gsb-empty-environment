package rtsp

import (
	"net/netip"
	"strconv"
	"strings"
)

// TransportSpec is the validated result of parsing a Transport header and
// binding it to the observed request source.
type TransportSpec struct {
	// Destination must equal the request source address. It is copied here
	// only so callers can inspect the validated value; Target is what the
	// manager actually pins.
	Destination netip.Addr
	// ClientPort is the even first port of the client_port pair.
	ClientPort uint16
}

// ParseTransport parses and validates an RTSP Transport header such as
//
//	RTP/AVP;unicast;client_port=5000-5001;destination=203.0.113.9
//
// against the observed request source. The destination parameter, when
// present, must be the source address itself, and client_port must be an
// even port followed by its consecutive odd port. Multicast transport is
// rejected outright because it has no per-client binding to validate.
func ParseTransport(header string, source netip.Addr) (TransportSpec, error) {
	if !source.IsValid() {
		return TransportSpec{}, errBadRequest
	}

	var spec TransportSpec
	havePort := false
	haveDestination := false

	parts := strings.Split(header, ";")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) != "RTP/AVP" {
		return TransportSpec{}, errBadRequest
	}

	for _, part := range parts[1:] {
		param := strings.TrimSpace(part)
		key, value, hasValue := strings.Cut(param, "=")
		switch key {
		case "unicast", "mode":
			// unicast is the only supported delivery mode; mode=PLAY is
			// informational and carries no security decision.
			continue
		case "multicast":
			return TransportSpec{}, errBadRequest
		case "destination":
			if !hasValue {
				return TransportSpec{}, errBadRequest
			}
			dest, err := netip.ParseAddr(strings.TrimSpace(value))
			if err != nil || dest.Unmap() != source.Unmap() {
				return TransportSpec{}, errBadRequest
			}
			spec.Destination = dest
			haveDestination = true
		case "client_port":
			if !hasValue {
				return TransportSpec{}, errBadRequest
			}
			lo, hi, ok := strings.Cut(strings.TrimSpace(value), "-")
			if !ok {
				return TransportSpec{}, errBadRequest
			}
			first, err := strconv.ParseUint(lo, 10, 16)
			if err != nil {
				return TransportSpec{}, errBadRequest
			}
			second, err := strconv.ParseUint(hi, 10, 16)
			if err != nil {
				return TransportSpec{}, errBadRequest
			}
			if first == 0 || first%2 != 0 || second != first+1 {
				return TransportSpec{}, errBadRequest
			}
			spec.ClientPort = uint16(first)
			havePort = true
		default:
			// Unknown parameters (for example source, ssrc) carry no
			// binding semantics and are ignored rather than trusted.
			continue
		}
	}

	if !havePort {
		return TransportSpec{}, errBadRequest
	}
	if !haveDestination {
		// RFC 2326 defaults destination to the request source; pinning the
		// default keeps the same no-reflection guarantee as an explicit one.
		spec.Destination = source
	}
	spec.Destination = spec.Destination.Unmap()
	return spec, nil
}
