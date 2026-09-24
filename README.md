# treeindex

维护 **对象 ID → 多级路径** 的持久化索引。纯 Go 标准库实现，所有状态文件
都位于调用方 `Open(dir)` 指定的目录中。

## 功能

- **原子批次提交**：`Commit([]Op)` 先在内存工作副本上顺序校验整批操作，
  任一失败则整批拒绝，内存与磁盘均保持不变。
- **循环检测**：移动子树前沿父链检查，把 A 移到它自己的子目录 B
  （或 A 自身、更深层后代）会被拒绝（`ErrCycle`）。
- **兄弟重名检测**：校验在批次的工作副本上顺序进行，同批内先后的同名
  节点互相可见，因此重名冲突不会因批次顺序而漏检。
- **路径缓存按版本失效**：每次成功提交 `version+1`，缓存条目携带版本号，
  版本不匹配即自动重算。
- **ID 稳定**：ID 与名称解耦，重命名/移动只改 `Name`/`Parent`，ID 不变。

## 存储布局（dir 内）

| 文件 | 作用 |
|---|---|
| `snapshot.json` | 已 checkpoint 的全量状态（版本号 + 全部节点） |
| `journal.log`   | 已提交但未 checkpoint 的批次（WAL） |
| `*.tmp`         | 写入中的临时文件，恢复时直接丢弃 |

## 提交流程

1. 在内存深拷贝上顺序应用并校验整个批次；失败 → 返回错误，无任何副作用。
2. 写 `journal.tmp` → `fsync` → 原子 `rename` 为 `journal.log` → `fsync` 目录。
   **rename 完成即提交点**。
3. 应用到内存，`version++`。
4. checkpoint：写 `snapshot.tmp` → `fsync` → 原子 `rename` → `fsync` 目录
   → 删除 `journal.log` → `fsync` 目录。

## 崩溃恢复

`Open` 时：

1. 删除残留的 `*.tmp`（未完成的写入，必然不完整）。
2. 加载 `snapshot.json`（没有则从空根开始）。
3. 若 `journal.log` 存在：该批次已越过提交点，重放之并立即 checkpoint。

因此崩溃只可能有两种结果：批次完整生效（日志已提交，重放恢复），
或批次完全不存在（日志未提交，临时文件被丢弃）。**不存在半应用的批次。**

## 断电保证的边界

保证成立的前提：

- 文件系统遵守 `fsync` 语义（数据真正落盘，而非仅进入易失缓存）；
- 同一文件系统内 `rename` 是原子的；
- 不发生磁盘数据腐化（bit rot）、介质损坏。

具体边界：

- `Commit` 返回 `nil` 的批次：保证在断电后仍然生效。
- 在提交点（journal rename + 目录 fsync）之前断电：批次保证完全消失。
- 在提交点之后、`Commit` 返回之前断电：调用方看到的是错误/未知结果，
  但重启后批次**已经生效**——这是 WAL 的标准“未知结局”语义，
  调用方若重试需自行保证幂等（例如使用确定性 ID，靠 `ErrExists` 去重）。
- 快照写入期间断电：`snapshot.tmp` 被丢弃，`journal.log` 仍是权威数据。
- 不保证跨机器复制、不防御文件系统本身谎报 `fsync` 的硬件。

## 故障注入点

通过 `WithHook(point, fn)` 在关键点注入错误以模拟崩溃/断电：

- `journal.beforeTmpWrite` / `journal.beforeRename`：提交点之前（批次丢失）
- `journal.afterCommit`：提交点之后、内存应用之前（恢复后生效）
- `snapshot.beforeWrite` / `journal.beforeRemove`：checkpoint 期间（重放恢复）

恢复路径（`Open` 内部）不触发注入点。

## 运行

```sh
go test ./...      # 测试
go run ./cmd/demo  # 演示（数据写入 ./demodata，可用参数指定其他目录）
```
