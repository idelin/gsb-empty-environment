# batchbuf — 可共享底层缓冲区的批次切片与引用释放

纯标准库实现（Python 3.11），测试使用 `unittest`。不依赖任何现成查询引擎。

## 结构

| 文件 | 职责 |
|---|---|
| `batchbuf/ledger.py` | 按物理缓冲区唯一计费的账本，支持额度（quota）与峰值统计 |
| `batchbuf/buffers.py` | 引用计数 + 借用（borrow）的物理缓冲区，明确的回收线性化点 |
| `batchbuf/table.py` | 带列类型（INT64/FLOAT64/BOOL）的批次、零拷贝切片、写时复制 |
| `batchbuf/errors.py` | `ResourceClosedError` / `QuotaExceededError` / `DoubleReleaseError` / `BorrowError` |
| `tests/test_batchbuf.py` | 19 个 unittest 用例，含可控交错并发检查 |
| `demo.py` | 运行样例 |

## 核心契约

- **唯一计费**：账本按物理缓冲区 id 计费。两个切片共享同一缓冲区只计一次；
  写时复制产生的临时私有副本在存活期间照常计费，峰值因此守恒。
- **额度原子拒绝**：`Ledger.charge_many` 在锁内先算总额再入账，额度不足时
  抛出 `QuotaExceededError`，*任何状态改变之前*拒绝，无部分入账。
- **关闭后拒绝访问**：`Batch.close()` / `Column.close()` 后任何读写、切片抛
  `ResourceClosedError`；`close` 幂等，循环重复调用安全（只释放一次）。
- **租约契约**：关闭父批次只释放父批次自己的引用；此前切出的切片继续持有
  租约、完全可用，物理缓冲区在最后一个视图关闭后才回收。
- **异常路径不重复释放**：构建/切片/COW 中途失败时，已占资源经 `try/except`
  恰好释放一次；`Buffer.release` 双重释放直接抛 `DoubleReleaseError`。
- **借用期不回收**：`with buf.borrowed():` 期间即使引用计数归零也不回收；
  待最后一个借用者归还后，在同一锁内完成回收（线性化点）。
- **线性化点**：所有引用/借用/回收状态迁移在缓冲区锁内完成；账本所有
  变更在账本锁内完成；`close` 的标志翻转在批次/列锁内完成。
- **null ≠ NaN**：null 由独立有效性位图表示；NaN 是 FLOAT64 的合法载荷值。
  整数求和走 Python 任意精度 int，精确无舍入。

## 运行样例（`python3.11 demo.py`）

```
[batch built (8 rows x 2 cols)] used=144B peak=144B live_buffers=4
[3 slices taken (zero-copy)] used=144B peak=144B live_buffers=4
  nested id values: [1, 2]
[COW write on left slice] used=180B peak=180B live_buffers=6
  original id[0] = 0 | left id[0] = 999
  parent closed; right slice still reads: [4.5, 5.5, 6.5, 7.5]
[parent batch closed] used=180B peak=180B live_buffers=6
  right val[0..1]: [4.5, 5.5] (None is null, not NaN)
  exact int sum 2^62 + 2^62 = 9223372036854775808
  quota rejected cleanly: charge of 64 bytes would exceed quota 181 (used=180)
[after rejected build] used=180B peak=198B live_buffers=6
  borrowed during close: reclaimed=False (must be False)
  after borrower left:   reclaimed=True (must be True)
[all views closed] used=0B peak=216B live_buffers=0
  access after close denied: batch is closed
```

## 测试（`python3.11 -m unittest discover -s tests`）

19 个用例全部通过：

```
Ran 19 tests in 1.085s

OK
```

覆盖点：

- **共享计费**：两切片/嵌套切片共享缓冲区只计一次；独立遍历存活视图复核账本
  （`test_ledger_crosscheck_by_traversing_live_views`）。
- **关闭语义**：关闭后拒绝访问、循环重复 close 安全、父批次关闭后切片租约保持、
  回收后访问/获取/借用均拒绝、双重释放抛错。
- **额度**：超额在任何状态改变前拒绝；构建失败部分列恰好释放一次；
  COW 超额原子失败（列保持共享旧缓冲区）。
- **写时复制**：写入者隔离、临时副本计费、有效性位图同样 COW、独占时就地写。
- **类型**：null 与 NaN 区分、整数精确求和（2^62+2^62=2^63）、类型检查。
- **并发可控交错**：事件对齐的"借用中释放最后引用不回收"；8 线程 × 300 轮
  barrier 对齐借用无重复释放、无借用期回收；峰值字节守恒（peak == max(used 轨迹)）。
