package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// ForwardedForHeader is the header carrying the multi-hop forwarding records.
const ForwardedForHeader = "X-Forwarded-For"

// ErrConflictingHeaders is returned when a request carries multiple
// Forwarded-For header lines that do not agree.
var ErrConflictingHeaders = errors.New("netsec: conflicting forwarded header lines")

type originContextKey struct{}

// FromContext returns the Origin stored by Middleware.
func FromContext(ctx context.Context) (Origin, bool) {
	origin, ok := ctx.Value(originContextKey{}).(Origin)
	return origin, ok
}

// RequestOrigin resolves the client origin of r using the connection peer
// (r.RemoteAddr) and the X-Forwarded-For records, reordered from nearest to
// farthest.
func (r *Resolver) RequestOrigin(req *http.Request) (Origin, error) {
	peer, err := parseRemoteAddr(req.RemoteAddr)
	if err != nil {
		return Origin{}, err
	}
	records, err := forwardedRecords(req.Header)
	if err != nil {
		return Origin{}, err
	}
	return r.Resolve(peer, records)
}

// Middleware resolves the request origin and stores it in the request
// context (see FromContext). Ambiguous input is rejected with 400.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		origin, err := r.RequestOrigin(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx := context.WithValue(req.Context(), originContextKey{}, origin)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// parseRemoteAddr extracts the peer IP from a host:port RemoteAddr, including
// bracketed IPv6 forms such as "[::1]:8080". A bare IP without port is also
// accepted. IPv4-mapped addresses are unmapped.
func parseRemoteAddr(remote string) (netip.Addr, error) {
	if addrPort, err := netip.ParseAddrPort(remote); err == nil {
		return addrPort.Addr().Unmap(), nil
	}
	if addr, err := netip.ParseAddr(remote); err == nil && addr.Zone() == "" {
		return addr.Unmap(), nil
	}
	return netip.Addr{}, fmt.Errorf("netsec: cannot parse remote address %q", remote)
}

// forwardedRecords reads the X-Forwarded-For header and returns the records
// ordered from nearest to farthest. Multiple header lines must be identical;
// conflicting lines are rejected as ambiguous.
func forwardedRecords(header http.Header) ([]string, error) {
	lines := header.Values(ForwardedForHeader)
	switch len(lines) {
	case 0:
		return nil, nil
	case 1:
	default:
		for _, line := range lines[1:] {
			if line != lines[0] {
				return nil, ErrConflictingHeaders
			}
		}
		lines = lines[:1]
	}
	parts := strings.Split(lines[0], ",")
	records := make([]string, 0, len(parts))
	for i := len(parts) - 1; i >= 0; i-- {
		records = append(records, strings.TrimSpace(parts[i]))
	}
	return records, nil
}
