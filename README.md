# SAML受众条件布尔语义

联合登录服务把多个受众限制简单合并后，原本受限声明会被接受。实现供Go认证宿主调用的条件求值包，输入已验签的声明树；同一限制内受众OR，不同限制之间AND，代理次数每转发一次减一，时间采用半开区间并仅在入口应用一次偏差。返回满足或拒绝的节点证据。以穷举小受众集合核对嵌套条件、多目标代理与零次转发限制，任何未知条件必须拒绝。

## 用法

```go
evaluator := netsec.NewEvaluator(time.Now(), 30*time.Second)
tree := &netsec.Node{
    ID: "assertion",
    Conditions: []netsec.Condition{
        // 同一限制内 OR；两个限制之间由节点/All 做 AND。
        &netsec.AudienceRestriction{Audiences: []string{"sp-a", "sp-b"}},
        &netsec.ValidityWindow{NotBefore: notBefore, NotOnOrAfter: expiry},
        &netsec.ProxyRestriction{Count: &maxHops},
    },
    Children: []*netsec.Node{{ID: "proxy-target"}},
}

report := evaluator.Evaluate(tree, "sp-a")
if !report.Accepted {
    // report.Evidences 给出每个节点满足或拒绝的证据与路径。
}
```

- `All`/`Any` 支持任意嵌套；未知条件使用 `UnknownCondition`，一律拒绝。
- 时间窗为半开区间 `[NotBefore, NotOnOrAfter)`，偏差只在 `NewEvaluator` 入口应用一次。
- 代理计数每进入一个子节点减一，取路径上最严格的限制；`Count=0` 禁止任何转发。

## 测试

`go test ./...` 以穷举小受众集合覆盖嵌套 AND/OR，并覆盖多目标代理、零次转发、
半开时间边界、单次偏差以及未知条件拒绝。
