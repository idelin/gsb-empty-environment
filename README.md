# slicedbuf

纯标准库（Python 3.11）实现的批次切片引擎：多个批次切片共享底层物理缓冲区，
按物理缓冲区唯一计费，支持嵌套切片、写时复制（COW）与多线程借用，
释放与关闭都有明确的线性化点。不依赖任何现成查询引擎。

## 结构

- `slicedbuf/ledger.py` — 配额账本：每个物理缓冲区按 id 唯一计费；
  预留超额时在任何状态改变前抛 `QuotaExceeded`；重复释放抛
  `DoubleReleaseError`；`close()` 幂等，关闭后拒绝新预留。
- `slicedbuf/buffer.py` — 物理缓冲区：引用计数 + 借用租约。
  回收的线性化点位于缓冲区锁内"引用与借用同时归零"的那一次
  `release()`/退借，账本在同一临界区内扣减，因此不会重复释放、
  不会在借用期内回收。
- `slicedbuf/table.py` — 带列类型的批次表（`int64`/`float64`/`bool`）。
  null 是有效性位图清零，NaN 是存储的浮点值，两者严格区分；
  整数按 int64 精确存储与计算。切片零拷贝共享缓冲区；
  写共享视图时先复制（临时拷贝计入账本），额度不足则在任何
  状态改变前拒绝。
- `slicedbuf/check.py` — 不变量复核：独立遍历存活视图，重算账本
  （字节守恒、峰值不低于存活、存活视图租约的缓冲区未被回收）。

## 契约要点

- 两个切片共享同一物理缓冲区只计一次费。
- 释放原批次不会使存活切片失效（切片持有自己的引用租约）。
- `release()`/`close()` 幂等，循环重复调用安全；关闭后访问抛 `ClosedError`。
- 构造、切片、COW 均为全有或全无：异常路径精确释放已获取资源，不重复释放。

## 运行

```bash
python3.11 demo.py        # 运行样例
python3.11 -m unittest -v # 运行测试（22 个用例）
```

## 样例输出（python3.11 demo.py）

```
== build batch (2 typed columns, null vs NaN) ==
after build                                                live=  72B peak=  72B buffers=4 freed=0

== zero-copy slices share physical buffers (counted once) ==
after 3 slices                                             live=  72B peak=  72B buffers=4 freed=0
nested slice reads: [3] [nan]

== releasing the parent batch keeps slices valid ==
after parent close (x2, idempotent)                        live=  72B peak=  72B buffers=4 freed=0
left still reads: [1, 2]

== copy-on-write: temp copy is accounted, parent unaffected ==
after COW set on right                                     live=  90B peak=  90B buffers=6 freed=0
right id: [99, 4] | left id: [1, 2]

== quota: insufficient quota rejected before any state change ==
rejected: need 32B, live 36B, quota 36B
state unchanged: [1, 2, 3, 4] live: 36 B

== threads borrow buffers; no reclaim during borrow ==
4 borrowers each summed id: [3, 3, 3, 3]

== independent traversal re-audits the ledger ==
audit ok                                                   live=  90B peak=  90B buffers=6 freed=0

== release everything; ledger drains to zero ==
final                                                      live=   0B peak=  90B buffers=0 freed=6
ledger closed twice safely

ALL CHECKS PASSED
```

## 测试结果（python3.11 -m unittest）

```
Ran 22 tests in 0.015s

OK
```

覆盖：共享缓冲区唯一计费、峰值字节守恒（随机交错 300 步对账）、
额度不足前置拒绝、部分构建失败精确释放一次、账本关闭幂等且拒绝新预留、
嵌套切片窗口正确、父批次关闭后切片存活、重复 close 安全、关闭后拒绝访问、
null 与 NaN 区分、int64 精确与溢出拒绝、COW 隔离与临时拷贝计费、
COW 额度原子性、可控交错下借用期内不回收（Event 编排）、
并发释放恰好一次、并发借用读取一致、独立遍历复核账本。
