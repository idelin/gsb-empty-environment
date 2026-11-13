package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// Cache is consulted only for admitted recursive (RD=1) queries whose names
// are not in a local authoritative zone.
type Cache interface {
	Lookup(ctx context.Context, question Question) (Resolution, bool)
}

// CacheFunc adapts a function to Cache.
type CacheFunc func(context.Context, Question) (Resolution, bool)

// Lookup implements Cache.
func (f CacheFunc) Lookup(ctx context.Context, question Question) (Resolution, bool) {
	return f(ctx, question)
}

// Upstream performs recursive resolution after an admitted RD=1 cache miss.
type Upstream interface {
	Resolve(ctx context.Context, question Question) (Resolution, error)
}

// UpstreamFunc adapts a function to Upstream.
type UpstreamFunc func(context.Context, Question) (Resolution, error)

// Resolve implements Upstream.
func (f UpstreamFunc) Resolve(ctx context.Context, question Question) (Resolution, error) {
	return f(ctx, question)
}

// HandlerConfig contains the local authority and optional recursive backends.
// Policy is required.
type HandlerConfig struct {
	Policy    *Policy
	Zones     []*Zone
	Authority Authority
	Cache     Cache
	Upstream  Upstream
}

// Handler enforces source-network admission and the RD=0/cache separation.
// It is safe for concurrent use when its Policy, zones, cache, and upstream
// are safe for concurrent use.
type Handler struct {
	policy    *Policy
	zones     []*Zone
	authority Authority
	cache     Cache
	upstream  Upstream
}

// NewHandler builds an admission handler. A nil policy is rejected rather
// than silently allowing every client.
func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Policy == nil {
		return nil, fmt.Errorf("netsec: handler policy is required")
	}
	zones := make([]*Zone, 0, len(config.Zones))
	for _, zone := range config.Zones {
		if zone == nil {
			return nil, fmt.Errorf("netsec: nil authoritative zone")
		}
		zones = append(zones, zone)
	}
	return &Handler{
		policy:    config.Policy,
		zones:     zones,
		authority: config.Authority,
		cache:     config.Cache,
		upstream:  config.Upstream,
	}, nil
}

// New is a convenience alias for NewHandler.
func New(config HandlerConfig) (*Handler, error) {
	return NewHandler(config)
}

// Policy returns the handler's admission policy.
func (h *Handler) Policy() *Policy {
	if h == nil {
		return nil
	}
	return h.policy
}

// Resolve answers one already-decoded query.
//
// The order of checks is security-relevant:
//  1. Source admission, before any cache or upstream access.
//  2. For RD=0, only local authoritative data.
//  3. For RD=1, local authoritative data, then cache, then upstream.
//
// A REFUSED result is always returned with empty answer, authority, and
// additional sections. All records in every response use Policy.ResponseTTL.
func (h *Handler) Resolve(ctx context.Context, request Request) Response {
	base := Response{
		ID:                 request.ID,
		Opcode:             request.Opcode,
		RecursionDesired:   request.RecursionDesired,
		RecursionAvailable: h != nil && h.upstream != nil,
		Question:           []Question{request.Question},
	}
	if h == nil || h.policy == nil || !h.policy.Admit(request.Client) {
		return refused(base)
	}
	if request.Opcode != 0 {
		base.RCode = RCodeNotImp
		return base
	}

	if !request.RecursionDesired {
		if resolution, ok := h.lookupAuthority(request.Question); ok {
			return h.fromResolution(base, resolution, true)
		}
		// RD=0 must not expose cache contents or reveal cache hit/miss state.
		return refused(base)
	}

	if resolution, ok := h.lookupAuthority(request.Question); ok {
		return h.fromResolution(base, resolution, true)
	}

	if h.cache != nil {
		if resolution, ok := h.cache.Lookup(ctx, request.Question); ok {
			return h.fromResolution(base, resolution, false)
		}
	}
	if h.upstream == nil {
		base.RCode = RCodeServFail
		return base
	}
	resolution, err := h.upstream.Resolve(ctx, request.Question)
	if err != nil {
		base.RCode = RCodeServFail
		return base
	}
	return h.fromResolution(base, resolution, false)
}

// Query is an alias for Resolve.
func (h *Handler) Query(ctx context.Context, request Request) Response {
	return h.Resolve(ctx, request)
}

// HandlePacket decodes, resolves, and encodes a DNS query. The client
// address must come from the transport, never from packet contents.
func (h *Handler) HandlePacket(packet []byte, client netip.Addr) ([]byte, error) {
	if len(packet) < headerLen {
		return nil, ErrMalformed
	}
	request, err := ParseQuery(packet)
	if err != nil {
		// An external source is refused even if its query is malformed. The
		// ID and RD bit can be recovered from a valid header without trusting
		// any variable-length question data.
		if h == nil || h.policy == nil || !h.policy.Admit(client) {
			response := headerOnlyResponse(packet, RCodeRefused)
			return EncodeResponse(response)
		}
		if errors.Is(err, ErrNotQuery) {
			return nil, err
		}
		response := headerOnlyResponse(packet, RCodeFormErr)
		return EncodeResponse(response)
	}
	request.Client = client
	response := h.Resolve(context.Background(), request)
	return EncodeResponse(response)
}

// HandleQuery is an alias for HandlePacket.
func (h *Handler) HandleQuery(packet []byte, client netip.Addr) ([]byte, error) {
	return h.HandlePacket(packet, client)
}

func (h *Handler) lookupAuthority(question Question) (Resolution, bool) {
	if authority := h.authority; authority != nil {
		return authority.Lookup(question)
	}
	name, err := canonicalName(question.Name)
	if err != nil {
		return Resolution{}, false
	}
	var best *Zone
	for _, zone := range h.zones {
		if !zone.Contains(name) {
			continue
		}
		if best == nil || len(zone.origin) > len(best.origin) {
			best = zone
		}
	}
	if best == nil {
		return Resolution{}, false
	}
	return best.Lookup(question)
}

func (h *Handler) fromResolution(base Response, resolution Resolution, authoritative bool) Response {
	if resolution.RCode == RCodeRefused {
		return refused(base)
	}
	base.RCode = resolution.RCode
	base.Authoritative = authoritative
	base.Answer = h.tidyRecords(resolution.Answer)
	base.Authority = h.tidyRecords(resolution.Authority)
	base.Additional = h.tidyRecords(resolution.Additional)
	return base
}

func (h *Handler) tidyRecords(records []Record) []Record {
	if records == nil {
		return nil
	}
	out := make([]Record, len(records))
	for i, record := range records {
		out[i] = record
		out[i].TTL = h.policy.ResponseTTL()
		if out[i].Class == 0 {
			out[i].Class = ClassINET
		}
		out[i].Data = append([]byte(nil), record.Data...)
	}
	return out
}

func refused(base Response) Response {
	base.RCode = RCodeRefused
	base.Authoritative = false
	base.Answer = nil
	base.Authority = nil
	base.Additional = nil
	return base
}

func headerOnlyResponse(packet []byte, rcode uint16) Response {
	flags := binary.BigEndian.Uint16(packet[2:4])
	return Response{
		ID:               binary.BigEndian.Uint16(packet[0:2]),
		Opcode:           uint8(flags>>11) & 0x0f,
		RCode:            rcode,
		RecursionDesired: flags&flagRecursion != 0,
	}
}
