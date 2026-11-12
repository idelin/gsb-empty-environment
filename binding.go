package netsec

import "fmt"

// ParseBinding extracts lease binding information from packet. Parsing and
// fixed-length validation run first; only when both succeed is a Binding
// assembled. On any failure it returns a nil binding and the check error, so
// callers never observe partial binding information.
func ParseBinding(packet []byte) (*Binding, error) {
	options, err := ParseOptions(packet)
	if err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}

	hlen := int(packet[hlenOffset])
	if hlen == 0 || hlen > chaddrMax {
		return nil, fmt.Errorf("%w: hlen=%d must be in 1..%d", ErrBadHardwareLen, hlen, chaddrMax)
	}

	binding := &Binding{
		HardwareAddr: clone(packet[chaddrOff : chaddrOff+hlen]),
		XID:          clone(packet[xidOffset : xidOffset+4]),
		ClientIP:     ipv4(packet[ciaddrOff : ciaddrOff+4]),
		YourIP:       ipv4(packet[yiaddrOff : yiaddrOff+4]),
		ServerIP:     ipv4(packet[siaddrOff : siaddrOff+4]),
		RelayIP:      ipv4(packet[giaddrOff : giaddrOff+4]),
		Options:      options,
	}
	if msgType, ok := options.Byte(OptMsgType); ok {
		binding.MessageType = msgType
	}
	return binding, nil
}

func clone(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// ipv4 normalizes a 4-byte slice into an independent IPv4 value.
func ipv4(in []byte) []byte {
	return clone(in)
}
