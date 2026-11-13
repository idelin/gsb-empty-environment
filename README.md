# 初始显式策略

调用方要求初始显式策略，路径在末端没有具体策略也通过。该初始值为真时，末端必须留下至少一个可接受策略。末端有可接受策略时放行。可导入的Go库供证书路径调用。构造末端策略为空的路径。

## 用法

证书链按 `leaf -> intermediate -> trust anchor` 顺序传入。`RequireExplicitPolicy`
为真且末端没有任何可接受策略时，`PoliciesValid` 返回 `false`。

```go
ok := netsec.PoliciesValid(chain, netsec.VerifyOptions{
	CertificatePolicies:   []netsec.OID{policy},
	RequireExplicitPolicy: true,
})
```
