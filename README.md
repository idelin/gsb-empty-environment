# App Attest 断言校验

可导入的 Go 包 `netsec`，用于在请求鉴权流程中校验 iOS App Attest 断言。
实现固定采用本题约定：

- `authenticatorData[0:32] = SHA-256(teamID + "." + bundleID)`
- `authenticatorData[32] = 0x00`
- `authenticatorData[33:37] = 大端 uint32 计数器`
- `clientDataHash = SHA-256(challenge || SHA-256(requestBody))`
- 签名输入为 `SHA-256(authenticatorData || clientDataHash)`，算法为 ECDSA P-256 ASN.1
- 挑战由 `IssueChallenge` 生成，60 秒有效，消费一次后立即失效

计数器只在所有检查（挑战、rpIdHash、标志位、请求体绑定、签名、计数器单调性）
通过后，才用 `CompareAndSwapCounter` 原子推进。任何失败都不会修改计数器。

## 用法

```go
import netsec "example.org/netsec/q0562"

store := netsec.NewMemoryStore()
verifier, err := netsec.NewVerifier(teamID, bundleID, store)
if err != nil {
	// handle configuration error
}

// 鉴权接口生成挑战并返回给客户端。
challenge, err := verifier.IssueChallenge()
if err != nil {
	// handle challenge generation error
}

// 收到断言后校验。PublicKey 必须来自服务端为 keyID 保存的注册结果，
// 不能直接信任客户端提交的公钥。
err = verifier.Verify(netsec.VerifyRequest{
	KeyID:     keyID,
	PublicKey: registeredPublicKey,
	Challenge: challenge,
	Body:      rawRequestBody,
	Assertion: netsec.Assertion{
		AuthenticatorData: authenticatorData,
		ClientDataHash:    clientDataHash,
		Signature:         signature,
	},
})
if err != nil {
	// reject the request
}
```

多实例部署时实现 `CounterStore` 和 `ChallengeStore` 的共享持久化版本，确保
`CompareAndSwapCounter` 与 `ConsumeChallenge` 在数据库或缓存层保持原子性。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖：正常推进、旧计数器重放、rpIdHash 不符、挑战重用、请求体替换、
挑战过期、签名/标志位/布局失败后计数器保持不变，以及并发断言只能成功一次。
