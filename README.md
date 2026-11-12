# AuthorizationPolicy主体与明文

网格里 AuthorizationPolicy 的 notPrincipals 对未走 mTLS 的明文请求会因为没有主体而成立，攻击者因此绕过“除某服务外均放行”的写法。为Go实现可导入的策略评估包：按 CUSTOM、DENY、ALLOW 的顺序评估，工作负载没有任何策略时放行，只有 ALLOW 策略而无规则命中时拒绝，空 ALLOW 规则集拒绝一切；principals 取自客户端证书的 SPIFFE 身份，requestPrincipals 取自 JWT 的 iss/sub，二者不可互换；明文请求没有主体时 notPrincipals 视为不成立并在结果里给出告警。用明文、JWT 与证书混用和多策略叠加验证。

## 包结构

可导入包 `netsec`（module `example.org/netsec/q0321`），零第三方依赖：

- `evaluator.go`：`Evaluator` 与 `Evaluate`，固定按 CUSTOM → DENY → ALLOW 评估。
- `rule.go`：`Source`/`Rule` 匹配，包含/排除列表语义，无身份时负匹配失败并产生 `Warning`。
- `types.go`：策略、规则、请求身份与 CUSTOM 授权器类型。
- `match.go`：`Exact`/`Prefix`/`Suffix`/`Contains`/`Presence` 字符串匹配。

## 评估语义

- 工作负载没有任何策略：默认放行。
- 只有 ALLOW 策略且无规则命中：拒绝；ALLOW 策略规则集为空：拒绝一切。
- DENY 策略首个命中规则立即拒绝，优先于所有 ALLOW。
- CUSTOM 命中规则后交给注册的外部授权器；未注册 provider 时失败关闭（拒绝并告警）。
- `principals`/`notPrincipals` 只看客户端证书里的 SPIFFE 身份（`Request.PeerPrincipals`）。
- `requestPrincipals`/`notRequestPrincipals` 只看 JWT 的 `iss/sub`（`JWTPrincipal` 构造），两套身份不可互换。
- 明文请求没有对端身份时，`notPrincipals` 判定为不匹配（而非“空值满足负匹配”），并在 `Result.Warnings` 中留下告警；无 JWT 时 `notRequestPrincipals` 同理。

## 使用示例

```go
evaluator := netsec.NewEvaluator()

// “除 service-b 外均放行”：明文请求不会再绕过。
evaluator.AddPolicy("orders", netsec.Policy{
    Name: "allow-except-b",
    Rules: []netsec.Rule{{
        Name:    "not-b",
        Sources: []netsec.Source{{NotPrincipals: []netsec.StringMatch{netsec.Exact(spiffeB)}}},
    }},
})

// 明文：PeerPrincipals 为空 → notPrincipals 不成立 → DENY + 告警。
result := evaluator.Evaluate(netsec.Request{Workload: "orders"})
// result.Decision == netsec.DecisionDeny，len(result.Warnings) == 1

// mTLS：SPIFFE 身份取自已验证的客户端证书。
result = evaluator.Evaluate(netsec.Request{
    Workload:       "orders",
    PeerPrincipals: []string{"spiffe://cluster.local/ns/team/sa/service-a"},
})

// JWT 身份用 iss/sub 表示，与证书身份分开评估。
jwtIdentity := netsec.JWTPrincipal("https://issuer.example.com", "subject-a")
```

## 验证

```sh
go test -race -v ./...
```

测试覆盖明文绕过与告警、JWT/证书不可互换、同一来源要求双身份（AND）、空 ALLOW 规则集、无命中默认拒绝、DENY 覆盖 ALLOW、CUSTOM 顺序与失败关闭，以及多策略叠加场景。
