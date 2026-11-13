# 恢复绑定覆盖转录

PSK绑定值没有覆盖ClientHello到绑定值之前的转录。每个绑定值必须等于对应位置的转录哈希；少算扩展或少算前面的身份都失败。覆盖完整前缀的绑定值接受。可导入的Go库供TLS栈调用。从转录中去掉一个扩展再验绑定值。

## 使用

```go
import (
	"crypto/sha256"

	netsec "example.org/netsec/q0242"
)

// 验证 ClientHello 中所有 PSK binder。
if err := netsec.VerifyClientHelloBinders(clientHelloRaw, sha256.New); err != nil {
	// binder 没有覆盖完整前缀，或 ClientHello 格式错误。
}

// 计算并写回 binder（输入中的占位值长度必须与哈希长度一致）。
filled, err := netsec.FillClientHelloBinders(clientHelloRaw, sha256.New)

// 也可以直接对已截断的转录前缀计算/验证。
prefix := clientHelloRaw[:binderValueOffset]
value := netsec.ComputeBinder(prefix, sha256.New)
ok := netsec.VerifyBinder(prefix, value, sha256.New)
```

无密钥的转录哈希默认让每个 binder 覆盖从 ClientHello 握手消息起点到该 binder 值之前的完整前缀；带 binder key 时默认使用 RFC 8446 的“到身份列表末尾”前缀。验证同时接受两种前缀，也可用 `StandardCoverage` 或 `PositionalCoverage` 强制指定。两种前缀都包含所有前面的扩展和全部身份。`RemoveExtension` 和 `RemoveIdentity` 会调整外层长度字段，可用于负向回归测试：删除一个扩展或一个较早身份后，原有 binder 必须验证失败。

如果 TLS 栈已经导出 binder key，可传入 `netsec.BinderKey(key)`；此时 binder 为 `HMAC-Hash(key, Transcript-Hash(prefix))`。
