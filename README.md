# pagealloc — 固定大小页分配器

管理固定大小页的申请与释放，支持连续区间申请、事务回滚、崩溃恢复。
纯 Go 标准库实现，状态文件放在调用方指定的目录中。

## 功能

- **连续区间申请**：`Tx.Alloc(n)` 以 first-fit 找到连续 n 个空闲页，返回起始页号。
- **相邻空闲区合并**：释放时自动与左右相邻空闲区间合并，避免碎片化空洞。
- **事务**：`Begin / Alloc / Free / Commit / Rollback`。回滚丢弃全部改动，
  申请过的页立即可重用；只有 `Commit` 才会落盘。
- **非法释放拒绝**：双重释放（含部分重叠、释放未占用页）返回 `ErrDoubleFree`，
  越界释放返回 `ErrOutOfRange`。
- **已占用页不重复分配**：占用信息即“不在空闲表中”，分配只从空闲表切割。
- **故障注入**：`FaultInjector` 可在提交路径的 4 个关键点注入一次 `ErrCrash`，
  用于测试崩溃恢复（见下）。

## 使用

```go
a, _ := pagealloc.Open("/path/to/dir", 1024) // 1024 页，状态存于该目录
tx, _ := a.Begin()
start, err := tx.Alloc(3)  // 申请连续 3 页
tx.Free(start, 3)          // 释放
tx.Commit()                // 或 tx.Rollback()
```

运行样例：`go run ./cmd/demo -dir /tmp/pagedata`；测试：`go test ./...`。

## 持久化与崩溃恢复

每次 `Commit` 把“总页数 + 空闲区间表（含 CRC32 校验）”按以下顺序写入：

1. 写入临时文件 `pages.state.tmp`；
2. `fsync` 临时文件（数据落盘）；
3. `rename` 为 `pages.state`（POSIX 原子替换）；
4. `fsync` 目录（目录项落盘）。

恢复（`Open`）时：删除残留的临时文件，加载 `pages.state` 并校验 magic 与 CRC32。

### 进程崩溃保证

- 崩溃只可能发生在两次 fsync 之间的提交路径上；由于 rename 的原子性，
  `pages.state` 永远是**完整的旧版本或完整的新版本**，不会出现撕裂/半写状态。
- 崩溃时未提交的事务整体丢失（符合事务语义）；已返回成功的提交不丢失。
- 恢复后总页数守恒：空闲页 + 占用页 = 总页数（有测试覆盖）。

### 断电保证的边界

- **保证**：`Commit` 返回成功后，即使立即断电，状态也可恢复——数据与目录项
  都已 fsync。断电发生在 `Commit` 中途时，恢复出旧版本或新版本之一，绝不撕裂；
  CRC32 兜底识别存储层返回的损坏数据（此时 `Open` 报错而非静默接受）。
- **不保证（边界）**：
  - 文件系统/磁盘自身不尊重 fsync 或 rename 原子性（如某些挂载选项、
    写缓存说谎的硬件）时，上述保证失效；
  - 多进程并发打开同一目录未加锁，需调用方保证单写者；
  - 状态文件被外部删除或目录本身丢失不在恢复范围内。

## 故障注入点

| 注入点 | 位置 | 崩溃后果 |
|---|---|---|
| `FaultBeforeWrite` | 写临时文件前 | 无文件变化，恢复旧状态 |
| `FaultBeforeFileSync` | 临时文件 fsync 前 | 临时文件残留（恢复时清理），恢复旧状态 |
| `FaultBeforeRename` | rename 前 | 临时文件已落盘但未生效，恢复旧状态 |
| `FaultBeforeDirSync` | 目录 fsync 前 | rename 已生效，恢复新状态 |

## 文件

- `allocator.go` — 分配器、事务、持久化、故障注入
- `allocator_test.go` — 全部测试（`go test ./...`）
- `cmd/demo/main.go` — 运行样例
