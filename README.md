# UCAN 能力只减不增

委托链上的后一跳能力比前一跳更大时，资源仍被访问，这是典型的权限提升漏洞。
本包 `netsec`（module `example.org/netsec/q0138`）是一个仅依赖 Go 标准库的
可导入库，供资源服务校验 UCAN 风格的能力委托凭证链。

## 校验规则

对链上每一对相邻凭证（父 → 子）强制以下不变量，任一不满足即拒绝：

- **能力只减不增**：子凭证声明的每条能力都必须被父凭证的某条能力覆盖
  （`CapabilitiesSubset`），资源与动作均支持末尾 `*` 通配。
- **过期只减不增**：子凭证的 `exp` 不得晚于父凭证的 `exp`（相等允许），
  违反返回 `ErrExpiryExtended`。
- **受众首尾相接**：子凭证的签发者 `iss` 必须等于父凭证的受众 `aud`，
  且子凭证的 `prf` 必须精确引用父凭证编码，违反返回 `ErrAudienceMismatch`。
- **逐跳验签**：每张凭证用其签发者的 Ed25519 公钥独立验证签名，
  篡改返回 `ErrInvalidSignature`。
- **根信任**：链首凭证的签发者必须在资源服务配置的 `TrustedIssuers` 内，
  否则返回 `ErrUntrustedRoot`。
- **全局时效**：所有凭证在校验时刻均未过期，否则返回 `ErrExpired`。

## 资源服务用法

```go
import netsec "example.org/netsec/q0138"

// tokens 按委托顺序排列：根凭证在前，末端访问者的凭证在最后。
chain := netsec.Chain{Tokens: tokens}
err := netsec.VerifyAccess(chain,
    netsec.Capability{Resource: "fs:/home/alice/docs/report", Action: "crud/read"},
    netsec.VerifyOptions{
        TrustedIssuers: map[string]bool{rootDID: true},
        Now:            time.Now(), // 可注入固定时钟便于测试
    })
```

`VerifyAccess` 先用 `ValidateChain` 校验链路不变量，再确认末端能力覆盖本次
请求；错误均为 `errors.go` 中的哨兵错误，可用 `errors.Is` 精确判别。

## 攻击场景（已被测试覆盖）

第二跳把能力改大（如把单文件只读扩大为整个家目录的 `crud/*`）并把过期
时间改晚时，`ValidateChain` 返回 `ErrCapabilityEscalated` /
`ErrExpiryExtended`，资源访问被拒绝。见 `chain_test.go` 中的
`TestSecondHopEscalationAndLaterExpiryTogetherRejected`。

## 测试

```sh
go test -race ./...
```
