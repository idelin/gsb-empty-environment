package netsec

// NATType 描述 NAT 的穿透难易程度。
type NATType int

const (
	// NATUnknown 表示尚未分类。
	NATUnknown NATType = iota
	// NATUnrestricted 表示非限制型 NAT（如全锥型），可被任意客户端匹配。
	NATUnrestricted
	// NATRestricted 表示限制型 NAT（如对称型），只能服务非限制型客户端。
	NATRestricted
)

// natCompatible 报告具有 clientNAT 的客户端 offer 能否交给 proxyNAT 的代理。
// 限制型 NAT 的客户端只能匹配到非限制型 NAT 的代理，其余组合均允许。
func natCompatible(clientNAT, proxyNAT NATType) bool {
	if clientNAT == NATRestricted {
		return proxyNAT == NATUnrestricted
	}
	return true
}
