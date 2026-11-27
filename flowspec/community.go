package flowspec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
)

// BGP path attribute type code for Extended Communities (RFC 4360).
const AttrExtCommunities uint8 = 16

// Extended community type octets.
const (
	ECTypeTransitiveTwoAS  uint8 = 0x00
	ECTypeTransitiveIPv4   uint8 = 0x01
	ECTypeTransitiveFourAS uint8 = 0x02
	ECTypeTransitiveOpaque uint8 = 0x03
	// ECTypeFlowSpec is the experimental/generic type used by RFC 8955
	// traffic-rate / traffic-action / redirect action communities.
	ECTypeFlowSpec uint8 = 0x80
)

// FlowSpec action subtypes (paired with ECTypeFlowSpec unless noted).
const (
	ECSubTrafficRate   uint8 = 0x06 // 0x8006
	ECSubTrafficAction uint8 = 0x07 // 0x8007
	ECSubRedirect      uint8 = 0x08 // 0x8008
	// ECSubRedirectToIP is the redirect-to-IP subtype under
	// ECTypeTransitiveIPv4 (0x010b).
	ECSubRedirectToIP uint8 = 0x0b
)

// Traffic-action flag bits in the final value octet.
const (
	TrafficActionTerminal byte = 0x01
	TrafficActionSample   byte = 0x02
	RedirectToIPCopyFlag  byte = 0x01
)

// Community errors.
var (
	ErrBadCommunityLength = errors.New("flowspec: extended community must be exactly 8 octets")
	ErrBadAttrHeader      = errors.New("flowspec: malformed extended-community path attribute")
	ErrNotExtCommunity    = errors.New("flowspec: path attribute is not type 16 (extended communities)")
)

// ExtendedCommunity is a raw 8-octet BGP extended community. The typed
// FlowSpec action views are derived from Type/Subtype/Value; Raw preserves the
// exact wire bytes.
type ExtendedCommunity struct {
	Type    uint8
	Subtype uint8
	Value   [6]byte
	Raw     [8]byte
}

// ID returns the 16-bit type/subtype identifier (e.g. 0x8006).
func (ec ExtendedCommunity) ID() uint16 {
	return uint16(ec.Type)<<8 | uint16(ec.Subtype)
}

// DecodeEC decodes exactly one 8-octet extended community.
func DecodeEC(b []byte) (ExtendedCommunity, error) {
	if len(b) != 8 {
		return ExtendedCommunity{}, fmt.Errorf("%w: got %d octet(s)", ErrBadCommunityLength, len(b))
	}
	ec := ExtendedCommunity{Type: b[0], Subtype: b[1]}
	copy(ec.Value[:], b[2:8])
	copy(ec.Raw[:], b[0:8])
	return ec, nil
}

// DecodeECList decodes an extended-community attribute value: a sequence of
// 8-octet communities.
func DecodeECList(payload []byte) ([]ExtendedCommunity, error) {
	if len(payload)%8 != 0 {
		return nil, fmt.Errorf("%w: attribute value length %d is not a multiple of 8", ErrBadCommunityLength, len(payload))
	}
	ecs := make([]ExtendedCommunity, 0, len(payload)/8)
	for off := 0; off < len(payload); off += 8 {
		ec, err := DecodeEC(payload[off : off+8])
		if err != nil {
			return nil, fmt.Errorf("community at offset %d: %w", off, err)
		}
		ecs = append(ecs, ec)
	}
	return ecs, nil
}

// DecodeExtCommunityAttribute decodes a complete BGP path attribute
// ([flags][type=16][length...][value]) carrying extended communities. It
// returns the communities and the total attribute length consumed. Both
// one-octet and extended (two-octet) attribute lengths are supported.
func DecodeExtCommunityAttribute(b []byte) ([]ExtendedCommunity, int, error) {
	if len(b) < 3 {
		return nil, 0, fmt.Errorf("%w: short header", ErrBadAttrHeader)
	}
	flags := b[0]
	atype := b[1]
	hdr := 3
	var length int
	if flags&0x10 != 0 { // Extended Length bit
		if len(b) < 4 {
			return nil, 0, fmt.Errorf("%w: short extended-length header", ErrBadAttrHeader)
		}
		length = int(binary.BigEndian.Uint16(b[2:4]))
		hdr = 4
	} else {
		length = int(b[2])
	}
	if atype != AttrExtCommunities {
		return nil, 0, fmt.Errorf("%w: got type %d", ErrNotExtCommunity, atype)
	}
	if len(b) < hdr+length {
		return nil, 0, fmt.Errorf("%w: value length %d exceeds %d available octets", ErrBadAttrHeader, length, len(b)-hdr)
	}
	ecs, err := DecodeECList(b[hdr : hdr+length])
	if err != nil {
		return nil, 0, err
	}
	return ecs, hdr + length, nil
}

func makeEC(typ, subtype uint8, value [6]byte) ExtendedCommunity {
	ec := ExtendedCommunity{Type: typ, Subtype: subtype, Value: value}
	ec.Raw[0] = typ
	ec.Raw[1] = subtype
	copy(ec.Raw[2:8], value[:])
	return ec
}

// TrafficRateCommunity builds a traffic-rate community (0x8006). A zero rate
// is the discard/blackhole action.
func TrafficRateCommunity(as uint16, rate float32) ExtendedCommunity {
	var v [6]byte
	binary.BigEndian.PutUint16(v[0:2], as)
	binary.BigEndian.PutUint32(v[2:6], math.Float32bits(rate))
	return makeEC(ECTypeFlowSpec, ECSubTrafficRate, v)
}

// TrafficActionCommunity builds a traffic-action community (0x8007).
func TrafficActionCommunity(terminal, sample bool) ExtendedCommunity {
	var v [6]byte
	var flags byte
	if terminal {
		flags |= TrafficActionTerminal
	}
	if sample {
		flags |= TrafficActionSample
	}
	v[5] = flags
	return makeEC(ECTypeFlowSpec, ECSubTrafficAction, v)
}

// RedirectCommunity builds a VRF/route-target style redirect community (0x8008).
func RedirectCommunity(target [6]byte) ExtendedCommunity {
	return makeEC(ECTypeFlowSpec, ECSubRedirect, target)
}

// RedirectToIPCommunity builds a redirect-to-IP community (0x010b).
func RedirectToIPCommunity(ip net.IP, withCopy bool) ExtendedCommunity {
	var v [6]byte
	v4 := ip.To4()
	if v4 != nil {
		copy(v[0:4], v4)
	} else {
		a16 := ip.To16()
		if a16 != nil {
			copy(v[0:4], a16[12:16])
		}
	}
	if withCopy {
		v[5] |= RedirectToIPCopyFlag
	}
	return makeEC(ECTypeTransitiveIPv4, ECSubRedirectToIP, v)
}

// EncodeECList concatenates the raw 8-octet communities.
func EncodeECList(ecs []ExtendedCommunity) []byte {
	out := make([]byte, 0, len(ecs)*8)
	for i := range ecs {
		out = append(out, ecs[i].Raw[:]...)
	}
	return out
}

// EncodeExtCommunityAttribute builds a transitive-optional BGP path attribute
// (flags 0xC0, type 16) carrying ecs. It uses the two-octet extended length
// form when the value exceeds 255 octets.
func EncodeExtCommunityAttribute(ecs []ExtendedCommunity) []byte {
	payload := EncodeECList(ecs)
	if len(payload) <= 255 {
		out := []byte{0xC0, AttrExtCommunities, byte(len(payload))}
		return append(out, payload...)
	}
	out := []byte{0xD0, AttrExtCommunities, 0, 0}
	binary.BigEndian.PutUint16(out[2:4], uint16(len(payload)))
	return append(out, payload...)
}

// IsTrafficRate reports whether ec is a traffic-rate action.
func (ec ExtendedCommunity) IsTrafficRate() bool {
	return ec.Type == ECTypeFlowSpec && ec.Subtype == ECSubTrafficRate
}

// IsTrafficAction reports whether ec is a traffic-action.
func (ec ExtendedCommunity) IsTrafficAction() bool {
	return ec.Type == ECTypeFlowSpec && ec.Subtype == ECSubTrafficAction
}

// IsRedirect reports whether ec is the VRF redirect action (0x8008).
func (ec ExtendedCommunity) IsRedirect() bool {
	return ec.Type == ECTypeFlowSpec && ec.Subtype == ECSubRedirect
}

// IsRedirectToIP reports whether ec is a redirect-to-IP action (0x010b).
func (ec ExtendedCommunity) IsRedirectToIP() bool {
	return ec.Type == ECTypeTransitiveIPv4 && ec.Subtype == ECSubRedirectToIP
}

// AsTrafficRate returns the typed traffic-rate action.
func (ec ExtendedCommunity) AsTrafficRate() *TrafficRate {
	if !ec.IsTrafficRate() {
		return nil
	}
	as := binary.BigEndian.Uint16(ec.Value[0:2])
	bits := binary.BigEndian.Uint32(ec.Value[2:6])
	return &TrafficRate{AS: as, RateBits: bits, Rate: math.Float32frombits(bits)}
}

// AsTrafficAction returns the typed traffic-action flags.
func (ec ExtendedCommunity) AsTrafficAction() *TrafficAction {
	if !ec.IsTrafficAction() {
		return nil
	}
	flags := ec.Value[5]
	return &TrafficAction{
		Flags:    flags,
		Terminal: flags&TrafficActionTerminal != 0,
		Sample:   flags&TrafficActionSample != 0,
	}
}

// AsRedirect returns the typed VRF redirect target.
func (ec ExtendedCommunity) AsRedirect() *Redirect {
	if !ec.IsRedirect() {
		return nil
	}
	return &Redirect{Target: ec.Value}
}

// AsRedirectToIP returns the typed redirect-to-IP target.
func (ec ExtendedCommunity) AsRedirectToIP() *RedirectToIP {
	if !ec.IsRedirectToIP() {
		return nil
	}
	ip := net.IPv4(ec.Value[0], ec.Value[1], ec.Value[2], ec.Value[3])
	flags := ec.Value[5]
	// The copy flag has been specified in either of the two trailing value
	// octets across revisions; reserved octets are zero in canonical encoding.
	copySet := (ec.Value[4]|ec.Value[5])&RedirectToIPCopyFlag != 0
	return &RedirectToIP{IP: ip, Copy: copySet, Flags: flags}
}

// ActionsFromCommunities extracts the known FlowSpec actions from an extended
// community list while retaining the raw communities.
func ActionsFromCommunities(ecs []ExtendedCommunity) ActionSet {
	set := ActionSet{Communities: ecs}
	for i := range ecs {
		ec := ecs[i]
		switch {
		case ec.IsTrafficRate():
			set.TrafficRate = ec.AsTrafficRate()
		case ec.IsTrafficAction():
			set.TrafficAction = ec.AsTrafficAction()
		case ec.IsRedirect():
			set.Redirect = ec.AsRedirect()
		case ec.IsRedirectToIP():
			set.RedirectToIP = ec.AsRedirectToIP()
		}
	}
	return set
}

// DecodeActionCommunities decodes an EC attribute value and extracts actions.
func DecodeActionCommunities(payload []byte) (ActionSet, error) {
	ecs, err := DecodeECList(payload)
	if err != nil {
		return ActionSet{}, err
	}
	return ActionsFromCommunities(ecs), nil
}
