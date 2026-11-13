package netsec

import "strings"

type sensitiveResourceKind int

const (
	notSensitive sensitiveResourceKind = iota
	sensitiveBaseResource
	sensitiveServiceAccountToken
)

func classifySensitiveResource(resource string) sensitiveResourceKind {
	if resource == "" {
		return notSensitive
	}
	if resource == "serviceaccounts/token" || strings.HasPrefix(resource, "serviceaccounts/token/") {
		return sensitiveServiceAccountToken
	}
	base := resource
	if index := strings.IndexByte(resource, '/'); index >= 0 {
		base = resource[:index]
	}
	switch base {
	case "secrets", "configmaps", "tokenreviews":
		return sensitiveBaseResource
	default:
		return notSensitive
	}
}

func isSensitiveProbe(probe Probe) bool {
	request := normalizeProbe(probe)
	return classifySensitiveResource(request.resource) != notSensitive
}
