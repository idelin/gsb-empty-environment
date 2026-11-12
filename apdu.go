package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var errMalformedAPDU = errors.New("malformed BACnet APDU")

// DecodeRequest accepts either a bare BACnet APDU or a complete BACnet/IP
// frame (BVLC + NPDU + APDU) and returns the normalized request. Unknown or
// unsupported services are still returned so that Policy.Admit can reject
// them with a stable reason.
func DecodeRequest(data []byte) (Request, error) {
	if len(data) == 0 {
		return Request{}, fmt.Errorf("%w: empty input", errMalformedAPDU)
	}

	if data[0] == 0x81 {
		apdu, err := unwrapBACnetIP(data)
		if err != nil {
			return Request{}, err
		}
		return decodeAPDU(apdu)
	}

	// A bare NPDU is also accepted. Try it first and fall back to treating
	// the input as an APDU, because a confirmed-request APDU can begin with
	// the same octet as the NPDU version.
	if data[0] == 0x01 {
		if apdu, err := unwrapNPDU(data); err == nil {
			if req, err := decodeAPDU(apdu); err == nil {
				return req, nil
			}
		}
	}

	return decodeAPDU(data)
}

// DecodeAPDU decodes a bare BACnet APDU.
func DecodeAPDU(apdu []byte) (Request, error) {
	return decodeAPDU(apdu)
}

// DecodeFrame decodes a complete BACnet/IP frame.
func DecodeFrame(frame []byte) (Request, error) {
	apdu, err := unwrapBACnetIP(frame)
	if err != nil {
		return Request{}, err
	}
	return decodeAPDU(apdu)
}

// AdmitAPDU decodes data (a bare APDU or complete BACnet/IP frame) and
// applies the policy. A parse failure is a denial, never an implicit allow.
func (p *Policy) AdmitAPDU(data []byte) Decision {
	req, err := DecodeRequest(data)
	if err != nil {
		return denied(ReasonMalformed, -1, err.Error())
	}
	return p.Admit(req)
}

// AuthorizeAPDU is the error-returning form of AdmitAPDU.
func (p *Policy) AuthorizeAPDU(data []byte) error {
	return p.AdmitAPDU(data).Err()
}

// AdmitFrame decodes a complete BACnet/IP frame and applies the policy.
func (p *Policy) AdmitFrame(frame []byte) Decision {
	req, err := DecodeFrame(frame)
	if err != nil {
		return denied(ReasonMalformed, -1, err.Error())
	}
	return p.Admit(req)
}

// AuthorizeFrame is the error-returning form of AdmitFrame.
func (p *Policy) AuthorizeFrame(frame []byte) error {
	return p.AdmitFrame(frame).Err()
}

func unwrapBACnetIP(frame []byte) ([]byte, error) {
	if len(frame) < 4 {
		return nil, fmt.Errorf("%w: short BVLC header", errMalformedAPDU)
	}
	if frame[0] != 0x81 {
		return nil, fmt.Errorf("%w: unsupported BVLC type %#x", errMalformedAPDU, frame[0])
	}
	length := int(binary.BigEndian.Uint16(frame[2:4]))
	if length < 6 || length > len(frame) {
		return nil, fmt.Errorf("%w: invalid BVLC length %d", errMalformedAPDU, length)
	}
	return unwrapNPDU(frame[4:length])
}

func unwrapNPDU(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: short NPDU", errMalformedAPDU)
	}
	if data[0] != 0x01 {
		return nil, fmt.Errorf("%w: unsupported NPDU version %#x", errMalformedAPDU, data[0])
	}
	control := data[1]
	if control&0x80 != 0 {
		return nil, fmt.Errorf("%w: NPDU contains a network-layer message", errMalformedAPDU)
	}

	i := 2
	if control&0x20 != 0 {
		if i+3 > len(data) {
			return nil, fmt.Errorf("%w: truncated NPDU destination", errMalformedAPDU)
		}
		addressLength := int(data[i+2])
		i += 4 + addressLength
		if i > len(data) {
			return nil, fmt.Errorf("%w: truncated NPDU destination address", errMalformedAPDU)
		}
	}
	if control&0x08 != 0 {
		if i+3 > len(data) {
			return nil, fmt.Errorf("%w: truncated NPDU source", errMalformedAPDU)
		}
		addressLength := int(data[i+2])
		i += 4 + addressLength
		if i > len(data) {
			return nil, fmt.Errorf("%w: truncated NPDU source address", errMalformedAPDU)
		}
	}
	if i >= len(data) {
		return nil, fmt.Errorf("%w: NPDU has no APDU", errMalformedAPDU)
	}
	return data[i:], nil
}

func decodeAPDU(apdu []byte) (Request, error) {
	if len(apdu) < 3 {
		return Request{}, fmt.Errorf("%w: APDU shorter than 3 octets", errMalformedAPDU)
	}
	if apdu[0]>>4 != 0 {
		return Request{}, fmt.Errorf("%w: unsupported PDU type %d", errMalformedAPDU, apdu[0]>>4)
	}

	service := Service(apdu[2])
	switch service {
	case ServiceWriteProperty:
		return decodeWriteProperty(apdu[3:])
	case ServiceWritePropertyMultiple:
		return decodeWritePropertyMultiple(apdu[3:])
	default:
		// Service-level policy handles unknown and control services. Their
		// request bodies do not need to be decoded.
		return Request{Service: service}, nil
	}
}

func decodeWriteProperty(data []byte) (Request, error) {
	i := 0

	object, next, err := decodeObjectIDTag(data, i, 0)
	if err != nil {
		return Request{}, err
	}
	i = next

	property, next, err := decodeUnsignedTag(data, i, 1)
	if err != nil {
		return Request{}, err
	}
	i = next

	// Optional array index.
	if header, err := readTagHeader(data, i); err == nil &&
		header.context && header.number == 2 {
		i, err = skipTag(data, i)
		if err != nil {
			return Request{}, err
		}
	}

	// Mandatory property value.
	i, err = skipRequiredValueTag(data, i, 3)
	if err != nil {
		return Request{}, err
	}

	var priority uint8
	if header, err := readTagHeader(data, i); err == nil &&
		header.context && header.number == 4 {
		value, next, err := decodeUnsignedTag(data, i, 4)
		if err != nil {
			return Request{}, err
		}
		if value > 0xff {
			return Request{}, fmt.Errorf("%w: priority %d does not fit in one octet", errMalformedAPDU, value)
		}
		priority = uint8(value)
		i = next
	}

	if i != len(data) {
		return Request{}, fmt.Errorf("%w: %d trailing octets after WriteProperty", errMalformedAPDU, len(data)-i)
	}

	return Request{
		Service:  ServiceWriteProperty,
		Object:   object,
		Property: PropertyID(property),
		Priority: priority,
	}, nil
}

func decodeWritePropertyMultiple(data []byte) (Request, error) {
	i := 0
	outer := false

	// Some encoders wrap the top-level SEQUENCE OF in an explicit [0]
	// opening tag. Accept both forms.
	if header, err := readTagHeader(data, i); err == nil &&
		header.context && header.number == 0 && header.opening {
		outer = true
		i = header.next
	}

	writes := make([]WriteAccess, 0, 4)
	for i < len(data) {
		header, err := readTagHeader(data, i)
		if err != nil {
			return Request{}, err
		}
		if header.context && header.closing {
			if outer {
				i = header.next
				outer = false
				break
			}
			return Request{}, fmt.Errorf("%w: unexpected closing tag", errMalformedAPDU)
		}

		object, next, err := decodeObjectIDTag(data, i, 0)
		if err != nil {
			return Request{}, err
		}
		i = next

		listHeader, err := readTagHeader(data, i)
		if err != nil {
			return Request{}, err
		}
		if !listHeader.context || listHeader.number != 1 || !listHeader.opening {
			return Request{}, fmt.Errorf("%w: expected [1] opening listOfProperties", errMalformedAPDU)
		}
		i = listHeader.next

		listClosed := false
		for i < len(data) {
			propHeader, err := readTagHeader(data, i)
			if err != nil {
				return Request{}, err
			}
			if propHeader.context && propHeader.number == 1 && propHeader.closing {
				i = propHeader.next
				listClosed = true
				break
			}

			property, next, err := decodeUnsignedTag(data, i, 0)
			if err != nil {
				return Request{}, err
			}
			i = next

			// Optional array index.
			if header, err := readTagHeader(data, i); err == nil &&
				header.context && header.number == 1 {
				i, err = skipTag(data, i)
				if err != nil {
					return Request{}, err
				}
			}

			// Mandatory property value: [2] in a property write spec.
			var nextIndex int
			nextIndex, err = skipRequiredValueTag(data, i, 2)
			if err != nil {
				return Request{}, err
			}
			i = nextIndex

			var priority uint8
			if header, err := readTagHeader(data, i); err == nil &&
				header.context && header.number == 3 {
				value, next, err := decodeUnsignedTag(data, i, 3)
				if err != nil {
					return Request{}, err
				}
				if value > 0xff {
					return Request{}, fmt.Errorf("%w: priority %d does not fit in one octet", errMalformedAPDU, value)
				}
				priority = uint8(value)
				i = next
			}

			writes = append(writes, WriteAccess{
				Object:   object,
				Property: PropertyID(property),
				Priority: priority,
			})
		}
		if !listClosed {
			return Request{}, fmt.Errorf("%w: unterminated listOfProperties", errMalformedAPDU)
		}
	}

	if outer {
		return Request{}, fmt.Errorf("%w: unterminated outer write-access list", errMalformedAPDU)
	}
	if i != len(data) {
		return Request{}, fmt.Errorf("%w: %d trailing octets after WritePropertyMultiple", errMalformedAPDU, len(data)-i)
	}

	return Request{
		Service: ServiceWritePropertyMultiple,
		Writes:  writes,
	}, nil
}

func decodeObjectIDTag(data []byte, i int, number uint8) (ObjectID, int, error) {
	header, err := readTagHeader(data, i)
	if err != nil {
		return ObjectID{}, 0, err
	}
	if !header.context || header.number != number || header.opening || header.closing || header.length != 4 {
		return ObjectID{}, 0, fmt.Errorf("%w: expected [%d] object identifier with length 4",
			errMalformedAPDU, number)
	}
	end := header.next + 4
	if end > len(data) {
		return ObjectID{}, 0, fmt.Errorf("%w: truncated object identifier", errMalformedAPDU)
	}
	raw := data[header.next:end]
	object := ObjectID{
		Type:     ObjectType(uint16(raw[0])<<2 | uint16(raw[1])>>6),
		Instance: uint32(raw[1]&0x3f)<<16 | uint32(raw[2])<<8 | uint32(raw[3]),
	}
	return object, end, nil
}

func decodeUnsignedTag(data []byte, i int, number uint8) (uint32, int, error) {
	header, err := readTagHeader(data, i)
	if err != nil {
		return 0, 0, err
	}
	if !header.context || header.number != number || header.opening || header.closing {
		return 0, 0, fmt.Errorf("%w: expected primitive context tag [%d]", errMalformedAPDU, number)
	}
	if header.length > 4 {
		return 0, 0, fmt.Errorf("%w: tag [%d] value has %d octets", errMalformedAPDU, number, header.length)
	}
	end := header.next + header.length
	if end > len(data) {
		return 0, 0, fmt.Errorf("%w: truncated tag [%d] value", errMalformedAPDU, number)
	}

	var value uint32
	for _, octet := range data[header.next:end] {
		value = value<<8 | uint32(octet)
	}
	return value, end, nil
}

func skipRequiredValueTag(data []byte, i int, number uint8) (int, error) {
	header, err := readTagHeader(data, i)
	if err != nil {
		return 0, err
	}
	if !header.context || header.number != number {
		return 0, fmt.Errorf("%w: expected value tag [%d]", errMalformedAPDU, number)
	}
	if header.closing {
		return 0, fmt.Errorf("%w: value tag [%d] is a closing tag", errMalformedAPDU, number)
	}
	return skipFromHeader(data, header)
}

type tagHeader struct {
	number  uint8
	context bool
	opening bool
	closing bool
	length  int
	next    int
}

func readTagHeader(data []byte, i int) (tagHeader, error) {
	if i >= len(data) {
		return tagHeader{}, fmt.Errorf("%w: missing tag", errMalformedAPDU)
	}
	octet := data[i]
	header := tagHeader{
		number:  octet >> 4,
		context: octet&0x08 != 0,
		next:    i + 1,
	}
	lvt := octet & 0x07

	if header.context {
		switch lvt {
		case 6:
			header.opening = true
			return header, nil
		case 7:
			header.closing = true
			return header, nil
		default:
			length, next, err := readLVT(data, header.next, lvt)
			if err != nil {
				return tagHeader{}, err
			}
			header.length = length
			header.next = next
			return header, nil
		}
	}

	// Application tags encode the value type in the high four bits. Null
	// and Boolean carry no payload; other application types use the low
	// three bits as a length.
	switch header.number {
	case 0, 1:
		return header, nil
	}
	length, next, err := readLVT(data, header.next, lvt)
	if err != nil {
		return tagHeader{}, err
	}
	header.length = length
	header.next = next
	return header, nil
}

func readLVT(data []byte, i int, lvt uint8) (int, int, error) {
	if lvt < 5 {
		return int(lvt), i, nil
	}
	if lvt != 5 {
		return 0, 0, fmt.Errorf("%w: invalid length/value type %d", errMalformedAPDU, lvt)
	}
	if i >= len(data) {
		return 0, 0, fmt.Errorf("%w: missing extended length", errMalformedAPDU)
	}
	length := int(data[i])
	i++
	switch length {
	case 254:
		if i+2 > len(data) {
			return 0, 0, fmt.Errorf("%w: truncated extended length", errMalformedAPDU)
		}
		length = int(binary.BigEndian.Uint16(data[i : i+2]))
		i += 2
	case 255:
		return 0, 0, fmt.Errorf("%w: invalid extended length 255", errMalformedAPDU)
	}
	return length, i, nil
}

func skipTag(data []byte, i int) (int, error) {
	header, err := readTagHeader(data, i)
	if err != nil {
		return 0, err
	}
	return skipFromHeader(data, header)
}

func skipFromHeader(data []byte, header tagHeader) (int, error) {
	if header.closing {
		return 0, fmt.Errorf("%w: unexpected closing tag", errMalformedAPDU)
	}
	if header.opening {
		return skipConstructed(data, header.next)
	}
	end := header.next + header.length
	if end > len(data) {
		return 0, fmt.Errorf("%w: tag payload runs past APDU", errMalformedAPDU)
	}
	return end, nil
}

func skipConstructed(data []byte, i int) (int, error) {
	depth := 1
	for i < len(data) {
		header, err := readTagHeader(data, i)
		if err != nil {
			return 0, err
		}
		switch {
		case header.opening:
			depth++
			i = header.next
		case header.closing:
			depth--
			i = header.next
			if depth == 0 {
				return i, nil
			}
		default:
			end := header.next + header.length
			if end > len(data) {
				return 0, fmt.Errorf("%w: constructed value runs past APDU", errMalformedAPDU)
			}
			i = end
		}
	}
	return 0, fmt.Errorf("%w: unterminated constructed value", errMalformedAPDU)
}
