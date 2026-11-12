package netsec

import "encoding/binary"

// Geneve 头部（RFC 8926），基础头 8 字节 + 变长选项：
//
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|Ver|  Opt Len  |O|C|    Rsvd.  |          Protocol Type        |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|        Virtual Network Identifier (VNI)       |    Reserved   |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                    Variable Length Options                    |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//
// 选项格式：
//
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|          Option Class         |      Type     |R|R|R| Length  |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                      Variable Length Option Data              |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
const (
	geneveBaseHdrLen = 8
	// 透明以太网桥接，解封装后交付内层以太帧。
	geneveProtoEth = 0x6558
	// 选项 Type 字段最高位为关键位（critical）。
	geneveOptCritical = 0x80
)

// parseGeneve 校验 Geneve 头与选项区，返回 VNI 与内层以太帧。
// 选项区长度必须自洽；未识别且置关键位的选项导致丢包。
func (g *Gateway) parseGeneve(p []byte) (vni uint32, inner []byte, err error) {
	if len(p) < geneveBaseHdrLen {
		return 0, nil, ErrTruncated
	}
	if p[0]>>6 != 0 {
		return 0, nil, ErrGeneveVersion
	}
	optLen := int(p[0]&0x3f) * 4
	// 第 1 字节低 6 位保留；O/C 标志仅作提示，安全判定基于选项本身。
	if p[1]&0x3f != 0 {
		return 0, nil, ErrReservedBits
	}
	if binary.BigEndian.Uint16(p[2:4]) != geneveProtoEth {
		return 0, nil, ErrGeneveProto
	}
	if p[7] != 0 {
		return 0, nil, ErrReservedBits
	}
	vni = uint32(p[4])<<16 | uint32(p[5])<<8 | uint32(p[6])

	// 选项区必须完整落在包内。
	if len(p) < geneveBaseHdrLen+optLen {
		return 0, nil, ErrGeneveOptOverrun
	}
	opts := p[geneveBaseHdrLen : geneveBaseHdrLen+optLen]
	for len(opts) > 0 {
		if len(opts) < 4 {
			return 0, nil, ErrGeneveOptOverrun
		}
		class := binary.BigEndian.Uint16(opts[0:2])
		typ := opts[2]
		dataLen := int(opts[3]&0x1f) * 4
		if len(opts) < 4+dataLen {
			return 0, nil, ErrGeneveOptOverrun
		}
		if _, known := g.opts[geneveOptionID{class: class, typ: typ & 0x7f}]; !known &&
			typ&geneveOptCritical != 0 {
			return 0, nil, ErrCriticalOption
		}
		opts = opts[4+dataLen:]
	}
	return vni, p[geneveBaseHdrLen+optLen:], nil
}
