# 跨信任等级缓存污染

文件检测服务同时缓存快速筛查和完整分析的结论，快速未发现不能替代完整无风险。实现供Go服务调用的结果缓存包，键绑定内容摘要和检测规则版本，结论附已执行检查集合及未完成项；只有请求检查集合被完整覆盖才可复用，多份结论可合并但任何阳性证据不能被阴性覆盖。用并发完成、取消和版本升级测试合并的幂等性及缺项不会被包装成通过。

## `netsec` 缓存包

### 核心不变量

- **键绑定**：`Key{ContentDigest, RulesVersion}` 同时绑定内容摘要与规则版本；
  规则升级后旧版本条目永远不会被新查询命中，不同版本各自独立存储。
- **显式缺项**：`Conclusion.Results` 只包含真正执行过的检查，未完成的检查进入
  `Pending`。`Get` 返回 `Missing` 列表，只有其为空（请求集合被完整覆盖）时
  结论才可复用，缺项绝不被包装成通过。
- **三态结论**：`Status`/`Decision` 只可能是 `incomplete`、`clean`、`threat`；
  存在未完成项即为 `incomplete`，阳性结果始终压过阴性。
- **合并规则**：`Merge` 仅允许相同 `Key` 的结论合并（跨版本返回
  `ErrKeyMismatch`）；同一检查冲突时阳性主导并累加证据，阴性永远无法抹掉
  既有阳性证据；合并满足幂等、交换、结合律。
- **并发安全**：`Cache` 内部加锁并对存取做深拷贝；`Analyze` 并发执行检查，
  遵循 `context.Context` 取消，未完成检查记入 `Pending` 而非伪造阴性结果。

### 典型用法

```go
cache := netsec.NewCache()
key := netsec.Key{ContentDigest: "sha256:...", RulesVersion: "2026.11.0"}

// 命中时直接复用；未覆盖的检查才会并发执行，部分结果自动回写缓存。
conclusion, missing, err := cache.Ensure(ctx, key, requestedChecks,
    func(ctx context.Context, check netsec.CheckID) (netsec.CheckResult, error) {
        return scanner.Run(ctx, check)
    })
if err != nil {
    // 取消或检查失败：conclusion 仍可用，missing 列出未完成项
}
switch conclusion.Decision(requestedChecks) {
case netsec.StatusThreat:      // 发现阳性证据
case netsec.StatusIncomplete:  // 存在未完成检查，不能视为无风险
case netsec.StatusClean:       // 请求集合全部执行且均为阴性
}
```

### 测试

`go test -race ./...` 覆盖：合并幂等性与阳性主导、快速筛查不可冒充完整通过、
并发完成、中途取消与重试只补跑缺项、规则升级条目隔离、并发 `Put`/`Get` 竞态。
