# 名称约束只能在 CA 上

`example.org/netsec/q0277` 是一个可导入的 Go 库，用于校验证书路径并执行
RFC 5280 名称约束。库中的测试包含一个攻击场景：末端证书自带一条更宽的
`permittedSubtrees`，试图放宽上级 CA 已经施加的限制。校验结果必须失败。

## 安全规则

- 证书路径按“末端证书 → 中间 CA → 根 CA”的顺序传入；反向链接的路径也会
  被自动识别并翻转。
- 名称约束只允许出现在 CA 证书上。非 CA 证书带有名称约束扩展时，立即返回
  `ErrNameConstraintsOnNonCA`；无论该扩展是否标记为 critical。
- 每个 CA 的名称约束独立作用于它下面的所有证书。因此下级 CA 或末端证书上
  更宽的约束不能改写、覆盖或放宽已经生效的约束。
- 同一类型中，多个 `permittedSubtrees` 在单个 CA 内取并集，不同 CA 之间取
  交集；`excludedSubtrees` 逐级累加。
- 支持 DNS、RFC 822 邮箱、IP 地址和 URI 名称约束。无法比较的受约束名称
  （例如没有主机名的 URI、使用 IP 主机的 URI）会被拒绝。
- 未知的 critical 扩展、错误的签名、过期的证书、非 CA 签发者以及违反
  `pathLenConstraint` 的路径都会被拒绝。

## 用法

```go
import (
	"crypto/x509"

	netsec "example.org/netsec/q0277"
)

// chain[0] 是末端证书，chain[len(chain)-1] 是根证书。
chain := []*x509.Certificate{leaf, intermediate, root}
if err := netsec.VerifyChain(chain); err != nil {
	// 名称约束违规时可用 errors.Is 判断具体原因。
}
```

主要入口：

- `Verify` / `VerifyChain` / `VerifyPath` / `VerifyCertificatePath`：
  完整路径校验。
- `VerifyChainWithOptions`：可指定校验时间与约束比较上限。
- `VerifyCertificate` / `VerifyCertificateWithPools`：先使用标准库
  `crypto/x509` 构建并校验证书路径，再对每条路径执行本库更严格的名称约束
  规则。
- `CheckNameConstraints`：只执行名称约束部分，便于调用方在自行完成签名
  校验后复用。
- `HasNameConstraints`：检查证书是否携带名称约束扩展。

## 测试

```sh
go test ./...
```
