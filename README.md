# 合约反例最小输入域

安全库的断言失败输入很大，压缩字节可能改变触发的前置条件。实现供Go测试宿主调用的结构化缩减包，输入为整数区间、字符串长度和字段存在条件，优先最少存在字段再最短总字节，保留原失败断言ID；每次候选须先满足公开前置条件。最多十六可选字段，穷举小域核对全局最小与预算未完成标记。

## `shrink` 包

包路径 `example.org/netsec/q0497/shrink`，由 Go 测试宿主驱动：

1. `NewSchema` 声明整数闭区间、字符串长度域（每长度一个代表值）和可选字段存在条件，最多 `MaxOptionalFields`(16) 个可选字段，存在掩码占两个字节。
2. `NewEnumerator` 按“存在可选字段数升序 → `TotalBytes` 升序 → 确定性并列序”惰性产出候选；不满足 `Precondition` 的候选在回放前被丢弃且不消耗预算。
3. `Shrink` 在预算内回放 `Reproduce`，首个仍失败的候选即全局最小；预算耗尽且未穷举时 `Result.Incomplete` 为真。
4. 每个 `Candidate` 都通过 `AssertionID()` 原样保留失败断言 ID。

```go
schema, _ := shrink.NewSchema("packet", "ASSERT-42", []shrink.Field{
    {Integer: &shrink.IntegerField{Name: "code", Min: 0, Max: 5}},
    {Optional: true, String: &shrink.StringField{Name: "label", MaxLen: 8}},
})
result, err := shrink.Shrink(shrink.Request{
    Schema:       schema,
    Precondition: func(c *shrink.Candidate) bool { return true },
    Reproduce:    func(c *shrink.Candidate) bool { /* 回放断言 */ return false },
    Budget:       1000,
})
_ = result
```

`go test ./...` 内含独立暴力枚举交叉核对、前置条件守卫、预算未完成标记、确定性与 16 可选字段上限用例。
