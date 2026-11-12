package netsec

import "encoding/binary"

const (
	etherHdrLen   = 14
	etherTypeIPv4 = 0x0800
	etherTypeIPv6 = 0x86dd
	ipProtoUDP    = 17
)

// 叠加网络隧道端口：内层载荷不得再指向这些端口（禁止再次封装）。
var tunnelPorts = map[uint16]struct{}{
	4789: {}, // VXLAN
	4790: {}, // VXLAN-GPE
	6081: {}, // Geneve
	8472: {}, // VXLAN (Linux  legacy)
}

// validateInnerFrame 校验解封装出的内层以太帧：
// 帧内声明的长度必须与实际长度自洽，且不允许再次封装。
func validateInnerFrame(f []byte) error {
	if len(f) < etherHdrLen {
		return ErrTruncated
	}
	etherType := binary.BigEndian.Uint16(f[12:14])
	payload := f[etherHdrLen:]
	switch etherType {
	case etherTypeIPv4:
		return validateInnerIPv4(payload)
	case etherTypeIPv6:
		return validateInnerIPv6(payload)
	default:
		// ARP 等无内嵌长度字段的类型无需进一步校验。
		return nil
	}
}

func validateInnerIPv4(p []byte) error {
	if len(p) < 20 {
		return ErrTruncated
	}
	if p[0]>>4 != 4 {
		return ErrInnerMalformed
	}
	ihl := int(p[0]&0x0f) * 4
	if ihl < 20 || len(p) < ihl {
		return ErrInnerMalformed
	}
	totalLen := int(binary.BigEndian.Uint16(p[2:4]))
	// IP 总长度必须落在实收帧内（允许以太网尾部填充，不允许越界声明）。
	if totalLen < ihl || totalLen > len(p) {
		return ErrLengthMismatch
	}
	if p[9] == ipProtoUDP {
		if totalLen < ihl+8 {
			return ErrTruncated
		}
		dstPort := binary.BigEndian.Uint16(p[ihl+2 : ihl+4])
		if _, nested := tunnelPorts[dstPort]; nested {
			return ErrNestedEncap
		}
	}
	return nil
}

func validateInnerIPv6(p []byte) error {
	if len(p) < 40 {
		return ErrTruncated
	}
	if p[0]>>4 != 6 {
		return ErrInnerMalformed
	}
	payloadLen := int(binary.BigEndian.Uint16(p[4:6]))
	// 载荷长度必须落在实收帧内。
	if payloadLen+40 > len(p) {
		return ErrLengthMismatch
	}
	if p[6] == ipProtoUDP {
		if payloadLen < 8 {
			return ErrTruncated
		}
		dstPort := binary.BigEndian.Uint16(p[42:44])
		if _, nested := tunnelPorts[dstPort]; nested {
			return ErrNestedEncap
		}
	}
	return nil
}
