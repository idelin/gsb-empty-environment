# manifest

Go 清单比较库：根据**路径、类型、内容摘要（sha256）和链接目标**比较两棵文件树，
生成可重放的更新计划，并用虚拟树执行器核对结果与目标完全一致。

## 核心概念

- `Manifest` — 路径 → `Entry{Type, Digest, Link}`，目录可隐式推导。
- `BuildPlan(old, want, opts)` — 生成有序、幂等的 `Plan`。
- `Executor` — 把计划应用到 `FS`（内置内存实现 `VTree`）。
- `Verify(fs, root, want)` — 精确核对：无缺失、无多余、无不匹配。

## 计划顺序保证

1. **文件变目录**：阻塞在所需目录路径上的文件/符号链接先被 `move-aside`
   移入隔离区（quarantine），内容永不删除。
2. **目录变文件**：先按深度从深到浅删除旧后代，再移除目录本身，最后写入新文件。
3. 所有删除先于所有创建/写入。

## 重放与失败恢复

每个操作都是幂等的：已满足的操作自动跳过。删除类操作带有旧身份守卫
（类型 + 摘要），目录仅在为空时才移除——因此计划中途失败后从头重放，
**不会误删计划已写入的新内容**，也不会触碰计划生成后被用户改动的文件。
被移走的旧内容保留在隔离区，可审计、可恢复。

## 用法

```go
plan, err := manifest.BuildPlan(old, want, manifest.Options{
    Content: func(path string) ([]byte, error) { return fetch(path) },
})
if err != nil { ... }

tree := manifest.NewVTree() // 或任何实现 manifest.FS 的后端
ex := manifest.Executor{FS: tree, Root: "/tree", Quarantine: "/quarantine"}
if err := ex.Execute(plan); err != nil { ... }  // 失败后可直接再次 Execute
if err := manifest.Verify(tree, "/tree", want); err != nil { ... }
```

## 测试

```sh
go test ./...
```

测试覆盖：空树到 populated、增删改、文件↔目录↔符号链接互换、
路径互换、计划二次执行（幂等）、在计划的每个操作之后模拟崩溃并重放、
删除守卫保护已变更/未知内容、Verify 检测缺失/多余/不匹配。
