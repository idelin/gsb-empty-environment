package netsec

import (
	"errors"
	"testing"
)

func mustNormalize(t *testing.T, words uint16, path []byte) *LogicalPath {
	t.Helper()
	lp, err := NormalizeEPATH(words, path)
	if err != nil {
		t.Fatalf("路径规范化失败: %v", err)
	}
	return lp
}

func TestPolicy_IdentityGetAllowedAndSetDenied(t *testing.T) {
	p := DefaultPolicy()
	words, path := buildEPATH(class8(ClassIdentity), inst8(1))

	allow := p.Admit(&Request{Service: ServiceGetAttributeSingle, PathSizeWords: words, PathData: path})
	if !allow.Allowed || allow.Reason != ReasonAllowed {
		t.Fatalf("身份对象 Get 应放行，得到 %s: %v", allow.Reason, allow.Err)
	}

	deny := p.Admit(&Request{Service: ServiceSetAttributeSingle, PathSizeWords: words, PathData: path})
	if deny.Allowed || deny.Reason != ReasonWriteDenied {
		t.Fatalf("身份对象 Set 应拒绝，得到 %+v", deny)
	}
}

// 攻击复现：8 位与 16 位编码的 0xF5 Set_Attribute_Single 必须同样被拒，
// 证明只比对原始字节前缀的策略会被 16 位段绕过。
func TestPolicy_TCPIPSetBlockedFor8And16BitSegments(t *testing.T) {
	p := DefaultPolicy()

	words8, path8 := buildEPATH(class8(ClassTCPIPInterface), inst8(1), attr8(0x05))
	d8 := p.Admit(&Request{Service: ServiceSetAttributeSingle, PathSizeWords: words8, PathData: path8})
	if d8.Allowed || d8.Reason != ReasonTCPIPWriteBlocked {
		t.Fatalf("8 位段 TCPIP Set 应硬拒绝，得到 %+v", d8)
	}

	words16, path16 := buildEPATH(class16(ClassTCPIPInterface), inst16(1), attr16(0x05))
	d16 := p.Admit(&Request{Service: ServiceSetAttributeSingle, PathSizeWords: words16, PathData: path16})
	if d16.Allowed || d16.Reason != ReasonTCPIPWriteBlocked {
		t.Fatalf("16 位段 TCPIP Set 应硬拒绝（绕过防护），得到 %+v", d16)
	}

	// 同一对象的 Get_Attribute_Single 仍可读。
	g16 := p.Admit(&Request{Service: ServiceGetAttributeSingle, PathSizeWords: words16, PathData: path16})
	if !g16.Allowed {
		t.Fatalf("16 位段 TCPIP Get 应放行，得到 %s: %v", g16.Reason, g16.Err)
	}

	// 两条路径规范化后必须指向同一对象。
	if d8.Path == nil || d16.Path == nil || d8.Path.Class != d16.Path.Class {
		t.Fatalf("规范化结果不一致: %+v vs %+v", d8.Path, d16.Path)
	}
}

func TestPolicy_NonWhitelistedClassDenied(t *testing.T) {
	p := DefaultPolicy()
	words, path := buildEPATH(class16(0x6B), inst16(1))
	d := p.Admit(&Request{Service: ServiceGetAttributeSingle, PathSizeWords: words, PathData: path})
	if d.Allowed || d.Reason != ReasonClassNotWhitelisted {
		t.Fatalf("非白名单类应拒绝，得到 %+v", d)
	}
}

func TestPolicy_UnsupportedServiceDenied(t *testing.T) {
	p := DefaultPolicy()
	words, path := buildEPATH(class8(ClassIdentity), inst8(1))
	d := p.Admit(&Request{Service: 0x4B, PathSizeWords: words, PathData: path})
	if d.Allowed || d.Reason != ReasonUnsupportedService {
		t.Fatalf("未知服务码应拒绝，得到 %+v", d)
	}
}

// 最高位是请求/响应标志，准入时必须屏蔽，不能把 0x8E 误判为未知服务。
func TestPolicy_ResponseBitStripped(t *testing.T) {
	p := DefaultPolicy()
	words, path := buildEPATH(class8(ClassIdentity), inst8(1))
	d := p.Admit(&Request{Service: ServiceGetAttributeSingle | 0x80, PathSizeWords: words, PathData: path})
	if !d.Allowed || d.Reason != ReasonAllowed {
		t.Fatalf("带响应位的 Get 服务码应被识别并放行，得到 %s: %v", d.Reason, d.Err)
	}
}

func TestPolicy_MalformedPathRejected(t *testing.T) {
	p := DefaultPolicy()
	_, path := buildEPATH(class16(0xF5), inst16(1))
	d := p.Admit(&Request{
		Service:       ServiceSetAttributeSingle,
		PathSizeWords: uint16(len(path)/2 + 2),
		PathData:      path,
	})
	if d.Allowed || d.Reason != ReasonMalformedPath || !errors.Is(d.Err, ErrPathSizeMismatch) {
		t.Fatalf("字长错位应判为畸形路径，得到 %+v", d)
	}
}

// buildUnconnectedSend 构造一帧合法的 Unconnected Send（发往连接管理器）。
func buildUnconnectedSend(inner *Request) *Request {
	pathWords, path := buildEPATH(class8(ClassConnectionManager), inst8(1))

	// 内嵌报文 = 服务码 + 单字节路径字长 + 完整路径/数据缓冲（嵌套时含更内层消息）。
	embedded := append([]byte{inner.Service & 0x7F, byte(inner.PathSizeWords)}, inner.PathData...)

	body := []byte{
		0x0A, // priority / time ticks
		0x05, // timeout ticks
		byte(len(embedded)),
		byte(len(embedded) >> 8),
	}
	body = append(body, embedded...)

	full := append(append([]byte{}, path...), body...)
	return &Request{Service: ServiceUnconnectedSend, PathSizeWords: pathWords, PathData: full}
}

// 嵌套转发：Unconnected Send 里嵌一个对 0xF5 的 Set，递归检查必须拦下。
func TestPolicy_UnconnectedSendNestedTCPIPSetBlocked(t *testing.T) {
	p := DefaultPolicy()

	innerWords, innerPath := buildEPATH(class16(ClassTCPIPInterface), inst16(1), attr16(0x05))
	inner := &Request{Service: ServiceSetAttributeSingle, PathSizeWords: innerWords, PathData: innerPath}
	outer := buildUnconnectedSend(inner)

	d := p.Admit(outer)
	if d.Allowed || d.Reason != ReasonTCPIPWriteBlocked {
		t.Fatalf("嵌套 TCPIP Set 应递归拒绝，得到 %s: %v", d.Reason, d.Err)
	}
}

// 嵌套转发中的合法 Get 应放行。
func TestPolicy_UnconnectedSendNestedGetAllowed(t *testing.T) {
	p := DefaultPolicy()

	innerWords, innerPath := buildEPATH(class16(ClassIdentity), inst16(1), attr16(1))
	inner := &Request{Service: ServiceGetAttributeSingle, PathSizeWords: innerWords, PathData: innerPath}

	d := p.Admit(buildUnconnectedSend(inner))
	if !d.Allowed || d.Reason != ReasonAllowed {
		t.Fatalf("嵌套身份对象 Get 应放行，得到 %s: %v", d.Reason, d.Err)
	}
}

// 深度有限：超过 MaxDepth 的多层 Unconnected Send 必须拒绝。
func TestPolicy_UnconnectedSendDepthLimit(t *testing.T) {
	p := DefaultPolicy()

	innerWords, innerPath := buildEPATH(class8(ClassIdentity), inst8(1))
	req := &Request{Service: ServiceGetAttributeSingle, PathSizeWords: innerWords, PathData: innerPath}

	// 第 MaxDepth 层内嵌消息在解析前即触发深度限制。
	for i := 0; i < p.MaxDepth+1; i++ {
		req = buildUnconnectedSend(req)
	}

	d := p.Admit(req)
	if d.Allowed || d.Reason != ReasonDepthExceeded {
		t.Fatalf("超深嵌套应拒绝，得到 %s: %v", d.Reason, d.Err)
	}
}

// 恰好达到深度上限的嵌套仍可被递归检查并放行。
func TestPolicy_UnconnectedSendAtDepthBoundaryAllowed(t *testing.T) {
	p := DefaultPolicy()

	innerWords, innerPath := buildEPATH(class8(ClassIdentity), inst8(1))
	req := &Request{Service: ServiceGetAttributeSingle, PathSizeWords: innerWords, PathData: innerPath}

	// 恰好在深度上限内的最后一层（深度 MaxDepth-1）应正常递归并放行。
	for i := 0; i < p.MaxDepth; i++ {
		req = buildUnconnectedSend(req)
	}

	d := p.Admit(req)
	if !d.Allowed {
		t.Fatalf("深度边界内的嵌套应放行，得到 %s: %v", d.Reason, d.Err)
	}
}

func TestPolicy_UnconnectedSendMalformedLength(t *testing.T) {
	p := DefaultPolicy()
	pathWords, path := buildEPATH(class8(ClassConnectionManager), inst8(1))

	// 声明内嵌消息长度 200，但请求体根本没有那么多数据。
	body := []byte{0x0A, 0x05, 0xC8, 0x00, 0x0E, 0x02}
	full := append(append([]byte{}, path...), body...)

	d := p.Admit(&Request{Service: ServiceUnconnectedSend, PathSizeWords: pathWords, PathData: full})
	if d.Allowed || d.Reason != ReasonUnconnectedMalformed {
		t.Fatalf("畸形内嵌长度应拒绝，得到 %s: %v", d.Reason, d.Err)
	}
}

func TestPolicy_UnconnectedSendWrongTargetClass(t *testing.T) {
	p := DefaultPolicy()
	// Unconnected Send 发往身份对象而非连接管理器，直接拒绝。
	words, path := buildEPATH(class8(ClassIdentity), inst8(1))
	d := p.Admit(&Request{Service: ServiceUnconnectedSend, PathSizeWords: words, PathData: path})
	if d.Allowed || d.Reason != ReasonClassNotWhitelisted {
		t.Fatalf("Unconnected Send 目标类错误应拒绝，得到 %+v", d)
	}
}

func TestDecision_Error(t *testing.T) {
	words, path := buildEPATH(class8(1), inst8(1))
	lp := mustNormalize(t, words, path)
	ok := &Decision{Allowed: true, Reason: ReasonAllowed, Path: lp}
	if ok.Error() != "" {
		t.Fatalf("放行决策的 Error 应为空串，得到 %q", ok.Error())
	}
}
