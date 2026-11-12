package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	vendorSpecificElementID = 221
	oweTransitionOUIType    = 0x1c
	rsnElementID            = 48
	oweAKMSuiteType         = 18
)

var (
	// ErrNoTransitionElement means the supplied information elements do not
	// contain an OWE Transition Mode element.
	ErrNoTransitionElement = errors.New("no OWE Transition Mode element")

	// ErrMalformedTransitionElement means an OWE Transition Mode element was
	// present but its contents were not valid.
	ErrMalformedTransitionElement = errors.New("malformed OWE Transition Mode element")

	// ErrMalformedIEs means the information element stream itself was malformed.
	ErrMalformedIEs = errors.New("malformed information elements")

	// ErrAmbiguousTransitionElement means more than one different OWE
	// Transition Mode element was found on the same BSS.
	ErrAmbiguousTransitionElement = errors.New("conflicting OWE Transition Mode elements")
)

var oweOUI = [3]byte{0x50, 0x6f, 0x9a}

// Security describes the authentication and encryption advertised by a BSS.
type Security uint8

const (
	SecurityUnknown Security = iota
	SecurityOpen
	SecurityOWE
)

// Convenience aliases for the security values.
const (
	Open = SecurityOpen
	OWE  = SecurityOWE
)

func (s Security) String() string {
	switch s {
	case SecurityOpen:
		return "open"
	case SecurityOWE:
		return "owe"
	default:
		return "unknown"
	}
}

// TransitionMode is the payload of an OWE Transition Mode vendor-specific
// information element. It names the counterpart BSS and its SSID.
//
// In OWE transition mode the open BSS advertises the hidden OWE BSS, and the
// OWE BSS advertises the open BSS. Both directions must be present and agree
// before a client may treat the pair as a verified transition.
type TransitionMode struct {
	BSSID net.HardwareAddr
	SSID  string
}

// OWETransition is an alias for TransitionMode.
type OWETransition = TransitionMode

func (t TransitionMode) String() string {
	return fmt.Sprintf("bssid=%s ssid=%q", t.BSSID, t.SSID)
}

func (t TransitionMode) equal(other TransitionMode) bool {
	return sameBSSID(t.BSSID, other.BSSID) && t.SSID == other.SSID
}

// BSS is one scan result. Transition, when non-nil, is the already parsed OWE
// Transition Mode element. If Transition is nil and IEs is set, the transition
// element is parsed from IEs.
//
// An empty SSID on an OWE BSS denotes a hidden SSID. Such a BSS can only be
// selected as part of a verified OWE transition pair, where the hidden SSID is
// supplied by the open BSS's transition element.
type BSS struct {
	BSSID      net.HardwareAddr
	SSID       string
	Security   Security
	RSSI       int
	Transition *TransitionMode
	IEs        []byte
}

// ScanResult is an alias for BSS.
type ScanResult = BSS

func (b BSS) String() string {
	return fmt.Sprintf("bssid=%s ssid=%q security=%s rssi=%d", b.BSSID, b.SSID, b.Security, b.RSSI)
}

// ParseTransitionElement parses one complete information element. The element
// must have element ID 221, the Wi-Fi Alliance OUI, and the OWE Transition Mode
// OUI type. ErrNoTransitionElement is returned when the element is a valid but
// unrelated information element.
func ParseTransitionElement(element []byte) (TransitionMode, error) {
	if len(element) < 2 {
		return TransitionMode{}, ErrMalformedTransitionElement
	}
	if element[0] != vendorSpecificElementID {
		return TransitionMode{}, ErrNoTransitionElement
	}
	length := int(element[1])
	if length != len(element)-2 {
		return TransitionMode{}, ErrMalformedTransitionElement
	}
	return parseTransitionBody(element[2:])
}

// ParseOWETransitionElement is an alias for ParseTransitionElement.
func ParseOWETransitionElement(element []byte) (TransitionMode, error) {
	return ParseTransitionElement(element)
}

// ParseTransitionIEs finds the OWE Transition Mode element in a raw beacon or
// probe response information element stream. Multiple byte-for-byte identical
// elements are accepted; conflicting elements are rejected.
func ParseTransitionIEs(ies []byte) (TransitionMode, error) {
	var (
		found     TransitionMode
		haveOne   bool
		ambiguous bool
	)

	offset := 0
	for offset < len(ies) {
		if len(ies)-offset < 2 {
			return TransitionMode{}, ErrMalformedIEs
		}
		id := ies[offset]
		length := int(ies[offset+1])
		offset += 2
		if length > len(ies)-offset {
			return TransitionMode{}, ErrMalformedIEs
		}
		body := ies[offset : offset+length]
		offset += length

		if id != vendorSpecificElementID {
			continue
		}
		transition, ok, err := parseTransitionBodyIfPresent(body)
		if err != nil {
			return TransitionMode{}, err
		}
		if !ok {
			continue
		}
		if haveOne && !found.equal(transition) {
			ambiguous = true
			continue
		}
		found = transition
		haveOne = true
	}

	if ambiguous {
		return TransitionMode{}, ErrAmbiguousTransitionElement
	}
	if !haveOne {
		return TransitionMode{}, ErrNoTransitionElement
	}
	return found, nil
}

// ParseOWETransitionIEs is an alias for ParseTransitionIEs.
func ParseOWETransitionIEs(ies []byte) (TransitionMode, error) {
	return ParseTransitionIEs(ies)
}

// BuildTransitionElement builds a complete OWE Transition Mode information
// element for the supplied counterpart BSS.
func BuildTransitionElement(transition TransitionMode) ([]byte, error) {
	if err := validateTransitionTarget(transition); err != nil {
		return nil, err
	}

	ssid := []byte(transition.SSID)
	length := 4 + 6 + 1 + len(ssid)
	element := make([]byte, 2+length)
	element[0] = vendorSpecificElementID
	element[1] = byte(length)
	copy(element[2:5], oweOUI[:])
	element[5] = oweTransitionOUIType
	copy(element[6:12], transition.BSSID)
	element[12] = byte(len(ssid))
	copy(element[13:], ssid)
	return element, nil
}

// OWETransitionElement is an alias for BuildTransitionElement.
func OWETransitionElement(transition TransitionMode) ([]byte, error) {
	return BuildTransitionElement(transition)
}

// InferSecurity derives a security mode from an information element stream.
// It recognizes OWE from RSN AKM suite 18 and treats a beacon without an RSN
// element as open. Other RSN configurations return SecurityUnknown.
func InferSecurity(ies []byte) Security {
	rsnPresent := false
	owe := false

	offset := 0
	for offset < len(ies) {
		if len(ies)-offset < 2 {
			return SecurityUnknown
		}
		id := ies[offset]
		length := int(ies[offset+1])
		offset += 2
		if length > len(ies)-offset {
			return SecurityUnknown
		}
		body := ies[offset : offset+length]
		offset += length

		if id != rsnElementID {
			continue
		}
		rsnPresent = true
		if rsnHasOWEAKM(body) {
			owe = true
		}
	}

	if !rsnPresent {
		return SecurityOpen
	}
	if owe {
		return SecurityOWE
	}
	return SecurityUnknown
}

func rsnHasOWEAKM(body []byte) bool {
	if len(body) < 2 {
		return false
	}
	// Version (2), group cipher suite (4), pairwise count (2).
	if len(body) < 8 {
		return false
	}
	offset := 2 + 4
	pairwiseCount := int(binary.LittleEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if pairwiseCount > (len(body)-offset)/4 {
		return false
	}
	offset += pairwiseCount * 4
	if len(body)-offset < 2 {
		return false
	}
	akmCount := int(binary.LittleEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if akmCount > (len(body)-offset)/4 {
		return false
	}
	for i := 0; i < akmCount; i++ {
		suite := body[offset+i*4 : offset+i*4+4]
		if suite[0] == 0x00 && suite[1] == 0x0f && suite[2] == 0xac && suite[3] == oweAKMSuiteType {
			return true
		}
	}
	return false
}

func parseTransitionBody(body []byte) (TransitionMode, error) {
	transition, ok, err := parseTransitionBodyIfPresent(body)
	if err != nil {
		return TransitionMode{}, err
	}
	if !ok {
		return TransitionMode{}, ErrNoTransitionElement
	}
	return transition, nil
}

func parseTransitionBodyIfPresent(body []byte) (TransitionMode, bool, error) {
	if len(body) < 4 {
		return TransitionMode{}, false, nil
	}
	if !bytes.Equal(body[:3], oweOUI[:]) || body[3] != oweTransitionOUIType {
		return TransitionMode{}, false, nil
	}

	payload := body[4:]
	if len(payload) < 7 {
		return TransitionMode{}, true, ErrMalformedTransitionElement
	}
	ssidLength := int(payload[6])
	if ssidLength > 32 || ssidLength != len(payload)-7 {
		return TransitionMode{}, true, ErrMalformedTransitionElement
	}

	transition := TransitionMode{
		BSSID: cloneBSSID(payload[:6]),
		SSID:  string(payload[7:]),
	}
	if err := validateTransitionTarget(transition); err != nil {
		return TransitionMode{}, true, fmt.Errorf("%w: %v", ErrMalformedTransitionElement, err)
	}
	return transition, true, nil
}

func validateTransitionTarget(transition TransitionMode) error {
	if err := validateBSSID(transition.BSSID); err != nil {
		return fmt.Errorf("invalid transition BSSID: %w", err)
	}
	if transition.SSID == "" {
		return errors.New("transition SSID must not be empty")
	}
	if len([]byte(transition.SSID)) > 32 {
		return errors.New("transition SSID exceeds 32 octets")
	}
	return nil
}

func validateBSSID(bssid net.HardwareAddr) error {
	if len(bssid) != 6 {
		return fmt.Errorf("BSSID must be 6 octets, got %d", len(bssid))
	}
	allZero := true
	for _, b := range bssid {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return errors.New("BSSID must not be all zeros")
	}
	if bssid[0]&1 == 1 {
		return errors.New("BSSID must be a unicast address")
	}
	return nil
}

func cloneBSSID(bssid net.HardwareAddr) net.HardwareAddr {
	if bssid == nil {
		return nil
	}
	clone := make(net.HardwareAddr, len(bssid))
	copy(clone, bssid)
	return clone
}

func cloneBSS(bss BSS) BSS {
	clone := bss
	clone.BSSID = cloneBSSID(bss.BSSID)
	if bss.Transition != nil {
		transition := *bss.Transition
		transition.BSSID = cloneBSSID(bss.Transition.BSSID)
		clone.Transition = &transition
	}
	if bss.IEs != nil {
		clone.IEs = append([]byte(nil), bss.IEs...)
	}
	return clone
}

func sameBSSID(a, b net.HardwareAddr) bool {
	return len(a) == len(b) && bytes.Equal(a, b)
}

func bssidKey(bssid net.HardwareAddr) string {
	return strings.ToLower(bssid.String())
}
