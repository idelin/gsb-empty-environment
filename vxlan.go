package netsec

// VXLAN 头部（RFC 7348），8 字节：
//
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|R|R|R|R|I|R|R|R|            Reserved                           |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                VXLAN Network Identifier (VNI) |   Reserved    |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
const (
	vxlanHdrLen = 8
	vxlanIFlag  = 0x08
)

// parseVXLAN 校验 VXLAN 头并返回 VNI 与内层以太帧。
// I 标志必须置位，且所有保留位必须为 0。
func parseVXLAN(p []byte) (vni uint32, inner []byte, err error) {
	if len(p) < vxlanHdrLen {
		return 0, nil, ErrTruncated
	}
	if p[0]&vxlanIFlag == 0 {
		return 0, nil, ErrMissingIFlag
	}
	// 标志字节除 I 位外全部保留；第 1-3 字节与第 7 字节保留。
	if p[0] != vxlanIFlag || p[1] != 0 || p[2] != 0 || p[3] != 0 || p[7] != 0 {
		return 0, nil, ErrReservedBits
	}
	vni = uint32(p[4])<<16 | uint32(p[5])<<8 | uint32(p[6])
	return vni, p[vxlanHdrLen:], nil
}
