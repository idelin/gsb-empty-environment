# 状态覆盖证据防伪

安全测试插件上报协议覆盖时，不能仅声明访问过某个状态。写供Go测试宿主调用的覆盖核验包，每个状态转换必须附前状态摘要、实际输入摘要、输出和模型版本，宿主用确定性转换回调重放后才计入；断链保留待定，冲突隔离。用跨模型拼接、遗漏拒绝转换与重复回执测试覆盖统计不会被虚报，模型回调异常不算已覆盖。

## 包：`coververify`

`coververify` 是供 Go 测试宿主调用的覆盖核验子包（模块 `example.org/netsec/q0407`）。
插件不能仅靠“访问过某状态”的声明取得覆盖，每条上报都必须携带四类证据，并经宿主重放确认。

### 宿主接入

1. 为每个模型版本注册确定性重放回调 `TransitionFunc`（同一前状态+输入必须恒等输出）。
2. 用 `AnchorChain` 锚定每条状态机链的创世摘要，并绑定唯一模型版本。
3. 对每条上报回执调用 `Verifier.Submit`，依据返回的 `Status` 记账。

```go
v := coververify.NewVerifier()
v.RegisterModel("model-a@1", deterministicCallback)
anchor := coververify.SumDigest([]byte("genesis"))
if err := v.AnchorChain("chain-1", "model-a@1", anchor); err != nil {
    log.Fatal(err)
}

res := v.Submit(coververify.Receipt{
    ChainID:         "chain-1",
    ModelVersion:    "model-a@1",
    PrevStateDigest: prevDigest, // 前状态摘要
    InputDigest:     inputDigest, // 实际输入摘要
    Output:          claimedOutput,
    ReceiptID:       "r-0001",
})
// 只有 res.Status == coververify.StatusAccepted 才计入覆盖。
_ = v.Stats() // Total == 各分类计数之和，可用于校验无回执被吞或虚增
```

### 核验与防伪规则

- **证据齐全**：链 ID、模型版本、前状态摘要、输入摘要、输出缺一即 `Rejected`，零摘要与空输出同样拒绝。
- **确定性重放**：宿主用模型回调按前状态摘要和输入摘要重算输出，输出摘要不一致即 `Rejected`；回调返回错误或 panic 记 `CallbackError`，绝不计为已覆盖。
- **链接成链**：回执前状态必须等于链当前前沿，否则断链进入 `Pending` 保留待定；后续回执补齐缺口时自动重放并级联提升。
- **冲突隔离**：同一转换键出现不同权威输出、或向已绑定模型的链拼接其它模型（跨模型拼接）时，该键被隔离为 `Conflict` 且不计覆盖，已隔离键再次上报仍为冲突，不影响同链其它转换和其它链。
- **重复去重**：完全重复的回执记 `Duplicate`，待定期内重复证据也不重复计数，覆盖统计不会被刷高。

### 测试

`go test ./... -race` 覆盖正常链、伪造输出/前状态/输入摘要、断链保留与补齐、
遗漏中间转换、重复回执、跨模型拼接、回调错误与 panic、键级冲突隔离、
待定期回调失效保留、并发上报账目守恒等场景。
