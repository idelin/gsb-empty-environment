# 策略令牌被用来执行未授权意图

服务发现与编排系统用ACL令牌控制操作，攻击者拿低权限令牌提交高权限意图，系统只按令牌存在性放行。请实现供Go编排服务调用的可导入策略执行组件。令牌绑定策略、命名空间、服务身份与操作类型，意图提交前逐项匹配；提权、跨命名空间与过期令牌拒绝。意图执行与策略变更审计。用低权限令牌提权、跨命名空间、过期令牌、合法意图和策略更新验证操作不越授权。

## 用法

```go
sink := &netsec.MemorySink{}
enforcer := netsec.NewEnforcer(sink)

// 策略变更本身需授权（actor 令牌须持有目标命名空间的 policy:write）
enforcer.UpsertPolicy("admin-token", netsec.Policy{
    ID: "web-read", Namespace: "prod", ServiceIdentity: "web",
    Operations: []netsec.Operation{netsec.OpServiceRead},
})
enforcer.RegisterToken(netsec.Token{
    ID: "low-token", PolicyIDs: []string{"web-read"},
    ExpiresAt: time.Now().Add(time.Hour),
})

// 意图提交前逐项匹配：令牌有效性 → 命名空间 → 服务身份 → 操作类型
decision, err := enforcer.Execute(intent, func() error {
    return orchestrator.Apply(intent) // 仅授权通过后才会执行
})
```

- `policy.go` — 令牌/策略/意图模型与逐项匹配规则；`WildcardIdentity = "*"` 仅放行服务身份维度，命名空间不设通配。
- `enforcer.go` — `Enforcer`：授权、执行、策略增删（需 `policy:write` 授权），拒绝原因细分为提权/跨命名空间/身份不符/过期/吊销/未知令牌。
- `audit.go` — `AuditSink` 接口与内存实现；每次意图评估与每次策略变更（含被拒绝的变更）均落审计。
- `enforcer_test.go` — 覆盖低权限提权、跨命名空间、过期令牌、合法意图、策略更新（含越权变更被拒）及吊销/未知令牌。

验证：`go vet ./... && go test -race ./...`
