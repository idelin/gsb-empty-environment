package netsec

import "context"

// Resolver 是收信宿主注入的 DNS 查询接口。
// 实现方可以是真实解析器，也可以是测试用的脚本化解析器。
type Resolver interface {
	LookupTXT(ctx context.Context, domain string) ([]string, error)
}

// DNSError 描述一次 DNS 查询失败，Temporary 区分临时错误与永久错误。
type DNSError struct {
	Temporary bool
	Message   string
}

func (e *DNSError) Error() string {
	if e.Temporary {
		return "temporary DNS failure: " + e.Message
	}
	return "permanent DNS failure: " + e.Message
}
