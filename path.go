package netsec

import "strings"

// gatedServicePrefixes 是受门控的管理类服务前缀，按大小写敏感匹配。
var gatedServicePrefixes = []string{
	"grpc.reflection.",
	"grpc.channelz.",
	"grpc.admin.",
	"grpc.testing.",
}

// gatedHealthService 是受门控的健康检查服务名（精确匹配）。
const gatedHealthService = "grpc.health.v1.Health"

// classifyPath 校验 gRPC :path 必须严格形如 "/服务/方法"，并提取服务名。
// 多余路径段、百分号编码、空服务名或空方法名都视为非法。
func classifyPath(path string) (service string, ok bool) {
	if len(path) < 2 || path[0] != '/' {
		return "", false
	}
	if strings.IndexByte(path, '%') >= 0 {
		// 拒绝一切百分号编码，避免用编码绕过服务名匹配。
		return "", false
	}
	rest := path[1:]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		// 缺少方法分隔符，或服务名为空（"//方法"）。
		return "", false
	}
	service, method := rest[:slash], rest[slash+1:]
	if method == "" || strings.IndexByte(method, '/') >= 0 {
		// 空方法名，或方法名中含多余路径段。
		return "", false
	}
	return service, true
}

// isGatedService 报告服务名是否属于受门控的管理类服务。
// 匹配大小写敏感：大小写变体（如 GRPC.REFLECTION.*）不在此列，
// 会按未知服务处理，同样得到 UNIMPLEMENTED。
func isGatedService(service string) bool {
	if service == gatedHealthService {
		return true
	}
	for _, prefix := range gatedServicePrefixes {
		if strings.HasPrefix(service, prefix) {
			return true
		}
	}
	return false
}
