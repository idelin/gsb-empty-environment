# batchproc

一个带版本冻结与断点恢复的 Go 批处理库（包 `batchproc/batch`）。

## 核心语义

- **冻结**：`Start` 时把显式输入清单（manifest）和每项内容摘要（SHA-256）
  冻结进检查点；目录中新增的文件不会自动纳入本批次。
- **重启核对**：`Resume` 先对未处理项重新计算摘要，与冻结值比对；
  被删除或替换的项及其全部后继被隔离，独立项继续处理。
- **结果绑定**：已处理结果永久绑定原摘要；源文件之后再被替换也不重算。
- **显式新批次**：新批次必须使用新的 batch ID（复用返回 `ErrBatchExists`），
  重新冻结、重新计算，绝不复用其他批次的结果。
- **显式依赖**：manifest 中声明 `DependsOn`；处理某项前其所有依赖的同批
  结果必须已确认，且依赖结果绑定的摘要必须与冻结时记录的一致，
  否则该项被隔离（不在同一批次内混用输入版本）。
- **检查点**：每个切点（冻结后、每项状态变更、输出确认）都持久化
  输入摘要、依赖摘要与输出摘要；`Resume` 时重哈希已存输出做确认，
  不一致返回 `ErrCheckpointCorrupt`。

## 用法

```go
fs := batch.NewMemFS() // 或适配真实目录的 FS 实现
store := batch.NewMemStore()
runner := batch.NewRunner(fs, store, func(ctx context.Context, item batch.Item, content []byte, deps map[string]batch.Result) ([]byte, error) {
    // 处理逻辑；deps 只含本批次已确认的依赖结果
    return content, nil
})

manifest := []batch.Item{
    {Path: "a.txt"},
    {Path: "b.txt", DependsOn: []string{"a.txt"}},
}
rep, err := runner.Start(ctx, "batch-1", manifest) // 冻结并执行
rep, err = runner.Resume(ctx, "batch-1")           // 崩溃/中断后恢复
```

## 测试

```
go test ./...
```

测试基于 `MemFS` 虚拟目录，覆盖：删除、替换（重启前 / 运行中）、
重启版本核对、显式新批次、新增文件不纳入、结果绑定原摘要、
依赖确认与版本一致性、输出确认防篡改，以及对每个检查点保存切点和
处理器切点的故障注入（恢复后与全量重算结果逐项比对）。
