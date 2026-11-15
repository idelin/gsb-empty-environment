// Package pathclass 按 gRPC :path 判定 RPC 的暴露级别。
//
// gRPC 的 :path 形如 "/服务/方法"，服务名取第一个 "/" 与最后一个
// "/" 之间的部分。任何不符合该形态的路径（多余段、百分号编码、
// 空服务名、空方法名）以及受保护服务的大小写变体都判为 Reject，
// 由门控以 UNIMPLEMENTED 拒绝，避免暴露服务是否存在。
package pathclass

import "strings"

// Verdict 表示对 RPC 路径的判定结果。
type Verdict int

const (
	// Allow 放行：普通服务，任何监听上的连接都可访问。
	Allow Verdict = iota
	// Protected 受保护：仅允许来自内部监听的连接访问。
	Protected
	// Reject 拒绝：路径非法或为受保护服务的大小写变体，
	// 无论来自哪个监听都返回 UNIMPLEMENTED。
	Reject
)

// 受保护服务前缀，精确大小写匹配。
var protectedPrefixes = []string{
	"grpc.reflection.",
	"grpc.channelz.",
	"grpc.admin.",
	"grpc.testing.",
}

// 受保护的健康检查服务，精确全名匹配。
const healthService = "grpc.health.v1.Health"

// Classify 判定 fullMethod（即 :path，形如 "/服务/方法"）的暴露级别。
func Classify(fullMethod string) Verdict {
	service, ok := splitService(fullMethod)
	if !ok {
		return Reject
	}
	if isProtected(service) {
		return Protected
	}
	if isProtectedFold(service) {
		// 大小写变体（如 /GRPC.REFLECTION...）：一律拒绝，
		// 不允许绕过门控，也不暴露真实服务的存在。
		return Reject
	}
	return Allow
}

// splitService 校验路径严格形如 "/服务/方法" 并返回服务名。
func splitService(path string) (string, bool) {
	if len(path) < 2 || path[0] != '/' {
		return "", false
	}
	if strings.IndexByte(path, '%') >= 0 {
		return "", false // 百分号编码一律拒绝
	}
	rest := path[1:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", false
	}
	service, method := rest[:slash], rest[slash+1:]
	if service == "" || method == "" {
		return "", false
	}
	if strings.IndexByte(method, '/') >= 0 {
		return "", false // 多余段
	}
	return service, true
}

func isProtected(service string) bool {
	if service == healthService {
		return true
	}
	for _, prefix := range protectedPrefixes {
		if strings.HasPrefix(service, prefix) {
			return true
		}
	}
	return false
}

func isProtectedFold(service string) bool {
	if strings.EqualFold(service, healthService) {
		return true
	}
	lower := strings.ToLower(service)
	for _, prefix := range protectedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
