# pagealloc

固定大小页的持久化分配器（纯 Go 标准库）。支持连续区间申请、事务与回滚、
基于预写日志（WAL）的崩溃恢复。所有文件存放在调用方指定的目录中。

## 功能

- `Txn.Alloc(n)`：首次适配申请 `n` 个**连续**页，返回起始页号；已占用页不会被重复分配。
- `Txn.Free(id)` / `Txn.FreeRange(start, count)`：释放页；相邻空闲区间自动合并。
- 事务语义：操作在 `Commit` 后生效并对后续事务可见；`Rollback` 丢弃全部操作，
  已申请的页立即可重用。事务内释放的页在提交前不可被本事务重新申请。
- 严格校验：双重释放（含事务内重复释放）返回 `ErrDoubleFree`；
  越界释放返回 `ErrOutOfRange`；空间不足返回 `ErrNoSpace`。
- 崩溃恢复：重新 `Open` 时自动重放已提交日志、丢弃未提交日志，总页数守恒。

## 文件布局（调用方通过 `Options.Dir` 指定目录）

| 文件 | 内容 |
|---|---|
| `pages.state` | 页大小、总页数、空闲区间列表 + CRC32 |
| `pages.journal` | 当前事务的操作记录 + commit 记录（带 CRC32） |

## 提交流程与故障注入点

```
1. 写 journal 操作记录 + fsync        ── Hooks.AfterJournalOpsSync
2. 写 journal commit 记录 + fsync     ── Hooks.AfterJournalCommitSync   ← 原子提交点
3. 应用到内存，状态写 tmp+rename+fsync ── Hooks.AfterStatePersist
4. 删除 journal + 目录 fsync
```

`Hooks` 是故障注入点：任一钩子返回错误即模拟该时刻崩溃，磁盘上已完成的
写入保持原样。测试用它覆盖三个崩溃点（见 `TestCrashRecovery`）。

## 崩溃恢复与断电保证的边界

**恢复规则**（`Open` 时执行）：

- journal 存在且 commit 记录完整、CRC 校验通过 → 事务已提交，按幂等语义重放
  （状态文件经 rename 原子替换，要么全含该事务、要么全不含，重放不会产生部分应用）。
- journal 缺失、半截、无 commit 记录或 CRC 不匹配 → 事务整体丢弃，等价于回滚。
- 处理完毕删除 journal 并 fsync 目录。

**保证范围**：

- 已返回成功的 `Commit` 在崩溃后必然生效；未完成的 `Commit` 要么完整生效、
  要么完整回滚，不会出现中间态；任何情况下总页数守恒、无重复分配。
- 状态文件通过 `tmp + fsync + rename + 目录 fsync` 原子替换，可防撕裂写；
  journal 的 commit 记录带 CRC32，可检测不完整/损坏的日志。

**边界（不保证的场景）**：

- 依赖 OS/硬件兑现 `fsync` 承诺。若磁盘/RAID 控制器对易失缓存撒谎
  （`fsync` 返回成功但数据未落盘），掉电仍可能丢数据——这是所有
  本地持久化系统的共同边界。
- 不防护静默数据损坏（bit rot）、误定向写、文件系统自身损坏；
  CRC 只能检测，不能修复。
- 假定单进程单写者（进程内事务已串行化）；未实现跨进程文件锁，
  多进程同时打开同一目录不在保证范围内。
- `rename` 原子性依赖同一文件系统内的 POSIX 语义。

## 使用示例

```go
a, _ := pagealloc.Open(pagealloc.Options{Dir: "/path/to/data", PageCount: 1024})
tx := a.Begin()
start, err := tx.Alloc(3)   // 申请 3 个连续页
if err != nil { tx.Rollback(); ... }
tx.Commit()                  // 提交后生效；崩溃可恢复
```

完整演示：`go run ./cmd/pagedemo`，输出：

```
数据目录: /var/folders/.../pagedemo3838716059
新建分配器: 总页数=16 页大小=4096B 空闲=16
申请 3 页 [0,3) 后回滚, 空闲=16 (页可重用)
提交: 段A=[0,3) 段B=[3,5), 空闲=11
释放相邻段后合并, 可一次申请 5 连续页 [0,5)
释放已占用页 0 -> <nil>; 再次释放 -> pagealloc: double free
越界释放页 100 -> pagealloc: page id out of range
提交 4 页 [1,5) 时崩溃: simulated power loss
重启恢复后: 空闲=11 (事务已重放, 总页数=16 守恒)
```

## 测试

`go test ./...`，结果（Go 1.24.2, darwin/arm64）：

```
--- PASS: TestRollbackReuse        申请3页回滚后可重用
--- PASS: TestCoalesce             相邻空闲区合并
--- PASS: TestNoReallocate         已占用页不重复分配，耗尽报 ErrNoSpace
--- PASS: TestDoubleFree           事务内/跨事务双重释放拒绝
--- PASS: TestOutOfRangeFree       越界释放拒绝
--- PASS: TestFreeRange            区间释放与部分空闲整体拒绝
--- PASS: TestPersistence          重开后状态保持
--- PASS: TestCrashRecovery        三个注入点崩溃恢复 + 页数守恒
--- PASS: TestTornJournal          撕裂/坏 CRC 日志按未提交处理
--- PASS: TestPageCountMismatch    页数不一致拒绝打开
--- PASS: TestConcurrentTxns       并发事务串行化无重复分配
--- PASS: ExampleAllocator         文档示例
ok  	pagealloc	8.415s
```
