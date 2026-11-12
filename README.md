# Git签名推送证书核对

攻击者可重放旧推送证书，或在证书之外夹带分支更新。为Go Git服务的接收钩子实现可导入的signed push校验包：解析certificate version 0.1的pusher、pushee、nonce与更新行，nonce仅用一次，证书更新行与实际更新逐项相等，受保护分支只认许可pusher。用重放和增删ref测试，验签由回调提供。

## 用法

包路径为 `example.org/netsec/q0049`，无第三方依赖。校验顺序为：解析证书、核对仓库（可选）、逐项核对更新、回调验签、受保护 ref 授权、一次性消费 nonce。只有全部检查通过后才消费 nonce，因此畸形、未签名、更新不符或越权的推送不会提前烧掉合法 nonce。

```go
package main

import (
	"log"
	"os"

	netsec "example.org/netsec/q0049"
)

func main() {
	verifier, err := netsec.NewVerifier(netsec.Config{
		// 验签由服务方提供；这里应调用 gpg/ssh-keygen 或签名库，
		// payload 是证书中被签名覆盖的原始字节。
		VerifySignature: func(payload, signature []byte) error {
			return verifyWithYourSigner(payload, signature)
		},
		// 生产环境请使用持久化、跨进程共享的 NonceStore。
		Nonces: newPersistentNonceStore(),
		Pushee: "ssh://git.example.org/repo.git",
		ProtectedRefs: map[string][]string{
			"refs/heads/main":      {"Alice <alice@example.com>"},
			"refs/heads/release/*": {"Release Bot <bot@example.com>"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// pre-receive 钩子的 stdin 是 "<old> <new> <ref>" 更新行；
	// GIT_PUSH_CERT 是完整的签名推送证书。
	certificate := []byte(os.Getenv("GIT_PUSH_CERT"))
	if _, err := verifier.VerifyReader(certificate, os.Stdin); err != nil {
		log.Fatal(err)
	}
}
```

`ParseCertificate` 可供需要单独审计证书字段的钩子直接调用；`ParseUpdates` 解析钩子 stdin；`CompareUpdates` 也可单独复用。`UpdateMismatchError`、`UnauthorizedPusherError` 等错误均支持 `errors.Is`/`errors.As`。

## 测试

```sh
go test ./... -race
```

测试覆盖：证书 0.1 字段/更新行解析、nonce 重放、并发下仅一次消费、证书之外新增或删除 ref、证书更新被隐藏、更新乱序/改写、受保护分支及删除操作、pusher 越权、pushee 不符、验签失败和 nonce 存储故障。
