# 进程内存哈希稳定边界

防篡改扫描器对运行进程按页哈希，不能把不同修改代次的页拼成一个合法映像。实现供Go探针调用的扫描包，页读取返回内容和写代，扫描前后取得映像提交代；只有一致快照才提交摘要树，否则只重读受影响页并受预算限制。用可暂停读页器验证反复写入、页移除和同内容不同映射代次。

## 用法

探针侧实现 `PageSource` 接口（`CommitGen` / `ListPages` / `ReadPage` / `PageWriteGen`），代次必须来自同一单调计数器：

```go
res, err := netsec.NewScanner(src, netsec.Options{
    RepairBudget: 64, // 首遍之后允许的内容重读页数上限
    MaxRounds:    16, // 修复轮次兜底上限
}).Scan()
if errors.Is(err, netsec.ErrRepairBudgetExhausted) {
    // 映像持续变动，本次未提交任何摘要树
}
if res.Committed {
    // res.Root 是 res.CommitGen 这一代映像的 Merkle 根
}
```

一致性判定：扫描前后映像提交代相同，且每页自读取后写代未变（修复轮内用 `PageWriteGen` 廉价校验），才提交摘要树；否则只对写代变化、被解除映射或新映射的页做内容重读。`PausableSource` + `Gate` 用于在探针的集成测试中注入并发写、页移除和同内容重映射。
