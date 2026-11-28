package jar_test

import (
	"errors"
	"fmt"
	"net/http"

	"formal.local/w28-0d0ffd9ddb/jar"
)

// 授权端点如何把 JAR 校验器接入普通 net/http 服务。
func ExampleValidator_ParseHTTPRequest() {
	registry := jar.NewRegistry()
	// 真实宿主从密钥管理系统载入客户端公钥并按 kid 注册。
	client := jar.NewRegisteredClient("gov-client-001", true)
	registry.Register(client)

	validator := jar.NewValidator(jar.Policy{
		Issuer:                     "https://as.example.gov.cn",
		RequireSignedRequestObject: true,
		MaxLifetime:                300_000_000_000, // 300s
		Leeway:                     30_000_000_000,  // 30s
		AllowedAlgs:                jar.DefaultAllowedAlgs,
	}, registry, jar.NewMemoryReplayCache())

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authzReq, err := validator.ParseHTTPRequest(req)
		if err != nil {
			switch {
			case errors.Is(err, jar.ErrPlaintextDowngrade):
				http.Error(w, "signed request object required", http.StatusBadRequest)
			case errors.Is(err, jar.ErrOuterInnerConflict):
				http.Error(w, "unsigned parameter tampering detected", http.StatusBadRequest)
			case errors.Is(err, jar.ErrReplayedJTI):
				http.Error(w, "request object replay detected", http.StatusBadRequest)
			default:
				http.Error(w, "invalid request object", http.StatusBadRequest)
			}
			return
		}
		// authzReq.RedirectURI / Scope / State 等均来自已验签 JWT 内层。
		fmt.Printf("trusted client=%s redirect=%s signed=%t\n",
			authzReq.ClientID, authzReq.RedirectURI, authzReq.Signed)
	})
	_ = handler
	// Output:
}
